package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/golang-jwt/jwt/v5"
)

type platformTestUsers struct {
	interfaces.UserRepository
	users map[string]*types.User
}

func (r *platformTestUsers) CreateUser(_ context.Context, u *types.User) error {
	r.users[u.ID] = u
	return nil
}
func (r *platformTestUsers) UpdateUser(_ context.Context, u *types.User) error {
	r.users[u.ID] = u
	return nil
}
func (r *platformTestUsers) GetUserByID(_ context.Context, id string) (*types.User, error) {
	if u := r.users[id]; u != nil {
		return u, nil
	}
	return nil, apprepo.ErrUserNotFound
}
func (r *platformTestUsers) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	for _, u := range r.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, apprepo.ErrUserNotFound
}
func (r *platformTestUsers) GetUserByUsername(_ context.Context, name string) (*types.User, error) {
	for _, u := range r.users {
		if u.Username == name {
			return u, nil
		}
	}
	return nil, apprepo.ErrUserNotFound
}

type platformTestTokens struct {
	stubAuthTokenRepo
	identities map[string]*types.PlatformSSOIdentity
	sessions   map[string]*types.PlatformSSOSession
	mu         sync.Mutex
}

func (r *platformTestTokens) GetPlatformSSOIdentity(_ context.Context, p, id string) (*types.PlatformSSOIdentity, error) {
	return r.identities[p+id], nil
}
func (r *platformTestTokens) BindPlatformSSOIdentity(_ context.Context, i *types.PlatformSSOIdentity) error {
	r.identities[i.Provider+i.Subject] = i
	return nil
}
func (r *platformTestTokens) CreatePlatformSSOSession(_ context.Context, s *types.PlatformSSOSession) error {
	r.sessions[s.ID] = s
	return nil
}
func (r *platformTestTokens) WithPlatformSSOSession(_ context.Context, id string, fn func(*types.PlatformSSOSession) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil {
		return types.ErrPlatformSSOInvalid
	}
	candidate := *s
	if err := fn(&candidate); err != nil {
		return err
	}
	*s = candidate
	return nil
}

func platformTestJWT(t *testing.T, client, id string, exp time.Time) string {
	t.Helper()
	v, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"client_id": client, "userId": json.Number(id), "exp": exp.Unix()}).SignedString([]byte("upstream-test-signature"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func platformTestService(t *testing.T, handler http.HandlerFunc) (*userService, *platformTestTokens) {
	t.Helper()
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	t.Setenv("SYSTEM_AES_KEY", "12345678901234567890123456789012")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	repo := &platformTestTokens{stubAuthTokenRepo: stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}, identities: map[string]*types.PlatformSSOIdentity{}, sessions: map[string]*types.PlatformSSOSession{}}
	s := &userService{config: &config.Config{PlatformSSO: &config.PlatformSSOConfig{Enabled: true, BaseURL: server.URL, ClientID: "weknora", ClientSecret: "backend-secret"}},
		tokenRepo: repo, userRepo: &platformTestUsers{users: map[string]*types.User{}}}
	return s, repo
}

func platformWriteUser(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":"0000","content":{"userId":446,"userAccount":"中文账号","userName":"平台用户"}}`))
}

func TestPlatformSSOPersonalWorkspaceUsesVerifiedEmail(t *testing.T) {
	for _, tc := range []struct{ account, name, want string }{
		{"new.user@test.com", "平台用户", "new.user@test.com"},
		{"内部账号", "平台用户", "平台用户"},
		{"placeholder@platform-sso.invalid", "平台用户", "平台用户"},
	} {
		t.Run(tc.account, func(t *testing.T) {
			s, store := platformTestService(t, func(w http.ResponseWriter, _ *http.Request) { platformWriteUser(w) })
			tenants := &provisioningTenantService{}
			s.tenantService = tenants
			user, fresh, err := s.platformSSOUser(t.Context(), store, s.config.PlatformSSO,
				&platformSSOUser{ID: "new-user", Account: tc.account, Name: tc.name}, types.TenantProvisioningCreatePersonal)
			if err != nil || !fresh {
				t.Fatalf("provisioning: fresh=%v err=%v", fresh, err)
			}
			if tenants.createdName != tc.want || user.Username == tc.want || user.TenantID != 99 {
				t.Fatalf("workspace=%q user=%q tenant=%d", tenants.createdName, user.Username, user.TenantID)
			}
		})
	}
}

func TestPlatformSSOLoginBindingRefreshAndRevocation(t *testing.T) {
	ctx := context.Background()
	var outage atomic.Bool
	var revoked atomic.Bool
	s, repo := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/UserLoginDisplayInfo" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("incorrect upstream userinfo request")
		}
		if outage.Load() {
			w.WriteHeader(503)
			return
		}
		if revoked.Load() {
			w.WriteHeader(401)
			return
		}
		platformWriteUser(w)
	})
	access := platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))
	first, err := s.LoginWithPlatformSSO(ctx, &types.PlatformSSOLoginRequest{AccessToken: access, RefreshToken: "upstream-refresh", ExpectedUserID: "446"}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(first.User.Email, "@platform-sso.invalid") || first.User.IsSystemAdmin {
		t.Fatal("invalid automatic provisioning")
	}
	second, err := s.LoginWithPlatformSSO(ctx, &types.PlatformSSOLoginRequest{AccessToken: access}, types.TenantProvisioningTenantless)
	if err != nil || first.User.ID != second.User.ID {
		t.Fatalf("identity was not reused: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("different SSO sessions received the same local token")
	}
	if _, _, err = s.ValidateToken(ctx, first.Token); err != nil {
		t.Fatal(err)
	}
	newAccess, newRefresh, err := s.RefreshToken(ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if repo.tokens[newAccess].PlatformSSOSessionID == nil || repo.tokens[newRefresh].PlatformSSOSessionID == nil {
		t.Fatal("local refresh lost SSO association")
	}
	sid := *repo.tokens[newAccess].PlatformSSOSessionID
	if !strings.HasPrefix(repo.sessions[sid].AccessToken, "enc:v1:") || !strings.HasPrefix(repo.sessions[sid].RefreshToken, "enc:v1:") {
		t.Fatal("upstream tokens stored in plaintext")
	}
	repo.sessions[sid].CheckedAt = time.Now().Add(-time.Minute)
	outage.Store(true)
	if _, _, err = s.ValidateToken(ctx, newAccess); !errors.Is(err, types.ErrPlatformSSOUnavailable) {
		t.Fatalf("outage misclassified: %v", err)
	}
	if repo.sessions[sid].IsRevoked {
		t.Fatal("temporary outage revoked the session")
	}
	outage.Store(false)
	revoked.Store(true)
	if _, _, err = s.ValidateToken(ctx, newAccess); !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatalf("revoked upstream session accepted: %v", err)
	}
	if _, _, err = s.RefreshToken(ctx, newRefresh); !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatalf("revoked session refreshed: %v", err)
	}
}

func TestPlatformSSOOnlyRejectsLegacySessionsAndKeepsProvisioning(t *testing.T) {
	ctx := context.Background()
	s, _ := platformTestService(t, func(w http.ResponseWriter, r *http.Request) { platformWriteUser(w) })
	legacyUser := &types.User{ID: "legacy-user", Email: "legacy@example.com", IsActive: true}
	s.userRepo.(*platformTestUsers).users[legacyUser.ID] = legacyUser
	access, refresh, err := s.generateTokensForTenant(ctx, legacyUser, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.config.PlatformSSO.OnlyLogin = true
	if _, _, err := s.ValidateToken(ctx, access); !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatalf("legacy access token accepted: %v", err)
	}
	if _, _, err := s.RefreshToken(ctx, refresh); !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatalf("legacy refresh token accepted: %v", err)
	}
	if response, err := s.Login(ctx, &types.LoginRequest{Email: legacyUser.Email}); err != nil || response.Success {
		t.Fatalf("local password login accepted: %v", err)
	}
	login, err := s.LoginWithPlatformSSO(ctx, &types.PlatformSSOLoginRequest{
		AccessToken: platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour)),
	}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatalf("SSO automatic provisioning was blocked: %v", err)
	}
	if _, _, err := s.ValidateToken(ctx, login.Token); err != nil {
		t.Fatalf("SSO session rejected: %v", err)
	}
}

func TestPlatformSSORejectsWrongClientSubjectAndBusinessErrors(t *testing.T) {
	var businessFailure bool
	s, _ := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if businessFailure {
			_, _ = w.Write([]byte(`{"code":"9999","content":{"userId":446}}`))
			return
		}
		platformWriteUser(w)
	})
	for _, test := range []struct{ client, id, expected string }{{"another-client", "446", ""}, {"weknora", "999", ""}, {"weknora", "446", "999"}} {
		_, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: platformTestJWT(t, test.client, test.id, time.Now().Add(time.Hour)), ExpectedUserID: test.expected}, types.TenantProvisioningTenantless)
		if !errors.Is(err, types.ErrPlatformSSOInvalid) {
			t.Fatalf("accepted mismatched identity: %v", err)
		}
	}
	businessFailure = true
	_, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))}, types.TenantProvisioningTenantless)
	if !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatal("HTTP 200 with a failed business code accepted")
	}
}

func TestPlatformSSORotatingRefreshIsSerialized(t *testing.T) {
	var refreshes atomic.Int32
	var newToken string
	s, repo := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			refreshes.Add(1)
			if err := r.ParseMultipartForm(65536); err != nil {
				t.Error(err)
			}
			if r.FormValue("grant_type") != "refresh_token" || r.FormValue("client_secret") != "backend-secret" || r.FormValue("refresh_token") != "original-refresh" {
				t.Error("invalid refresh request")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": newToken, "refresh_token": "rotated-refresh"})
			return
		}
		platformWriteUser(w)
	})
	oldToken := platformTestJWT(t, "weknora", "446", time.Now().Add(10*time.Second))
	newToken = platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))
	login, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: oldToken, RefreshToken: "original-refresh"}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatal(err)
	}
	sid := repo.tokens[login.Token].PlatformSSOSessionID
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.validatePlatformSSOSession(context.Background(), sid, login.User.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("upstream refresh count=%d, want 1", refreshes.Load())
	}
}

func TestPlatformSSORetainsRotationDuringUserInfoOutage(t *testing.T) {
	var outage atomic.Bool
	var refreshes atomic.Int32
	var newToken string
	s, repo := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = r.ParseMultipartForm(65536)
			expected, next := "original-refresh", "rotated-refresh"
			if refreshes.Add(1) > 1 {
				expected, next = "rotated-refresh", "recovered-refresh"
			}
			if r.FormValue("refresh_token") != expected {
				t.Errorf("refresh token = %q, want %q", r.FormValue("refresh_token"), expected)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": newToken, "refresh_token": next})
			return
		}
		if outage.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		platformWriteUser(w)
	})
	oldToken := platformTestJWT(t, "weknora", "446", time.Now().Add(10*time.Second))
	newToken = platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))
	login, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: oldToken, RefreshToken: "original-refresh"}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatal(err)
	}
	sid := repo.tokens[login.Token].PlatformSSOSessionID
	verifiedExpiry := repo.sessions[*sid].ExpiresAt
	outage.Store(true)
	if err := s.validatePlatformSSOSession(context.Background(), sid, login.User.ID); !errors.Is(err, types.ErrPlatformSSOUnavailable) {
		t.Fatalf("unverified rotated token authorized: %v", err)
	}
	session := repo.sessions[*sid]
	refresh, err := secutils.DecryptStoredSecret(session.RefreshToken)
	if err != nil || refresh != "rotated-refresh" || session.IsRevoked || !session.ExpiresAt.Equal(verifiedExpiry) {
		t.Fatal("rotation was lost or unverified expiry was trusted")
	}
	outage.Store(false)
	if err := s.validatePlatformSSOSession(context.Background(), sid, login.User.ID); err != nil {
		t.Fatalf("session did not recover using the rotated refresh token: %v", err)
	}
}

func TestPlatformSSONeverFollowsCredentialRedirects(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	s, _ := platformTestService(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) })
	_, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))}, types.TenantProvisioningTenantless)
	if err == nil || reached.Load() {
		t.Fatal("credential-bearing redirect followed")
	}
}

func TestPlatformSSOExistingAccountRequiresExplicitBinding(t *testing.T) {
	s, repo := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"0000","content":{"userId":446,"userAccount":"existing@example.com","userName":"Existing"}}`))
	})
	users := s.userRepo.(*platformTestUsers)
	users.users["existing"] = &types.User{ID: "existing", Email: "existing@example.com", Username: "Existing", PasswordHash: "unchanged-password-hash", IsActive: true}
	req := &types.PlatformSSOLoginRequest{AccessToken: platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))}
	if _, err := s.LoginWithPlatformSSO(context.Background(), req, types.TenantProvisioningTenantless); err == nil {
		t.Fatal("silently linked an existing local user")
	}
	if len(repo.identities) != 0 {
		t.Fatal("failed login created an identity binding")
	}
	s.config.PlatformSSO.LinkExistingEmails = []string{"existing@example.com"}
	result, err := s.LoginWithPlatformSSO(context.Background(), req, types.TenantProvisioningTenantless)
	if err != nil || result.User.ID != "existing" {
		t.Fatalf("approved binding failed: %v", err)
	}
	if result.User.PasswordHash != "unchanged-password-hash" || len(users.users) != 1 {
		t.Fatal("existing user was modified or duplicated")
	}
}

func TestPlatformSSONoRefreshDoesNotRejectAnUnexpiredToken(t *testing.T) {
	s, repo := platformTestService(t, func(w http.ResponseWriter, r *http.Request) { platformWriteUser(w) })
	result, err := s.LoginWithPlatformSSO(context.Background(), &types.PlatformSSOLoginRequest{AccessToken: platformTestJWT(t, "weknora", "446", time.Now().Add(20*time.Second))}, types.TenantProvisioningTenantless)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ValidateToken(context.Background(), result.Token); err != nil {
		t.Fatalf("valid token rejected prematurely: %v", err)
	}
	session := repo.sessions[*repo.tokens[result.Token].PlatformSSOSessionID]
	session.ExpiresAt = time.Now().Add(-time.Second)
	if _, _, err := s.ValidateToken(context.Background(), result.Token); !errors.Is(err, types.ErrPlatformSSOInvalid) {
		t.Fatalf("expired SSO token accepted: %v", err)
	}
}
