package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Serializes first-time provisioning within a process. The database identity
// primary key also prevents cross-replica identity duplication.
var platformSSOProvisionMu sync.Mutex

type platformSSOUser struct{ ID, Account, Name string }
type platformSSOClaims struct {
	Subject   string
	ExpiresAt time.Time
}

func (s *userService) platformSSOConfig() (*config.PlatformSSOConfig, error) {
	if s.config == nil || s.config.PlatformSSO == nil || !s.config.PlatformSSO.Enabled {
		return nil, types.ErrPlatformSSOInvalid
	}
	c := s.config.PlatformSSO
	if c.BaseURL == "" || c.ClientID == "" || len(secutils.GetAESKey()) != 32 {
		return nil, fmt.Errorf("%w: missing SSO configuration or SYSTEM_AES_KEY", types.ErrPlatformSSOUnavailable)
	}
	return c, nil
}

func (s *userService) platformSSOStore() (interfaces.PlatformSSORepository, error) {
	r, ok := s.tokenRepo.(interfaces.PlatformSSORepository)
	if !ok {
		return nil, fmt.Errorf("%w: SSO persistence unavailable", types.ErrPlatformSSOUnavailable)
	}
	return r, nil
}

func platformSSOClient() *http.Client {
	c := newOIDCHTTPClient()
	c.Timeout = 10 * time.Second
	// Never forward bearer tokens or client credentials to a redirected endpoint.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func platformSSORequest(ctx context.Context, c *config.PlatformSSOConfig, path, token, contentType string, body io.Reader, dst any) error {
	endpoint := strings.TrimRight(c.BaseURL, "/") + path
	if err := validateOIDCEndpoint("platform_sso", endpoint, true); err != nil {
		return fmt.Errorf("%w: SSO endpoint not allowed", types.ErrPlatformSSOUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("%w: invalid SSO request", types.ErrPlatformSSOUnavailable)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := platformSSOClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: SSO request failed", types.ErrPlatformSSOUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || (path == "/oauth/token" && resp.StatusCode == 400) {
		return types.ErrPlatformSSOInvalid
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: SSO HTTP %d", types.ErrPlatformSSOUnavailable, resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: invalid SSO response", types.ErrPlatformSSOUnavailable)
	}
	return nil
}

func platformSSOUserInfo(ctx context.Context, c *config.PlatformSSOConfig, token string) (*platformSSOUser, error) {
	var response struct {
		Code    json.RawMessage `json:"code"`
		Content struct {
			ID      json.RawMessage `json:"userId"`
			Account string          `json:"userAccount"`
			Name    string          `json:"userName"`
		} `json:"content"`
	}
	if err := platformSSORequest(ctx, c, "/UserLoginDisplayInfo", token, "", nil, &response); err != nil {
		return nil, err
	}
	if strings.Trim(string(response.Code), "\"") != "0000" {
		return nil, types.ErrPlatformSSOInvalid
	}
	id := strings.Trim(string(response.Content.ID), "\"")
	if !platformSSOValidID(id) {
		return nil, types.ErrPlatformSSOInvalid
	}
	return &platformSSOUser{ID: id, Account: strings.TrimSpace(response.Content.Account), Name: strings.TrimSpace(response.Content.Name)}, nil
}

func platformSSOValidID(id string) bool {
	if id == "" || len(id) > 64 || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Only called AFTER online verification by the configured SSO server. Decoding a
// JWT is not authentication. Bind that verified token to our client and subject.
func platformSSOReadClaims(token, clientID, subject string) (*platformSSOClaims, error) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser(jwt.WithJSONNumber()).ParseUnverified(token, claims); err != nil {
		return nil, types.ErrPlatformSSOInvalid
	}
	if claims["client_id"] != clientID || fmt.Sprint(claims["userId"]) != subject {
		return nil, types.ErrPlatformSSOInvalid
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil || !exp.After(time.Now()) {
		return nil, types.ErrPlatformSSOInvalid
	}
	return &platformSSOClaims{Subject: subject, ExpiresAt: exp.Time}, nil
}

func (s *userService) LoginWithPlatformSSO(ctx context.Context, req *types.PlatformSSOLoginRequest, mode types.TenantProvisioningMode) (*types.LoginResponse, error) {
	c, err := s.platformSSOConfig()
	if err != nil {
		return nil, err
	}
	if req == nil || len(req.AccessToken) == 0 || len(req.AccessToken) > 32768 || len(req.RefreshToken) > 32768 {
		return nil, types.ErrPlatformSSOInvalid
	}
	store, err := s.platformSSOStore()
	if err != nil {
		return nil, err
	}
	info, err := platformSSOUserInfo(ctx, c, req.AccessToken)
	if err != nil {
		return nil, err
	}
	if req.ExpectedUserID != "" && req.ExpectedUserID != info.ID {
		return nil, types.ErrPlatformSSOInvalid
	}
	claims, err := platformSSOReadClaims(req.AccessToken, c.ClientID, info.ID)
	if err != nil {
		return nil, err
	}
	user, _, err := s.platformSSOUser(ctx, store, c, info, mode)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, types.ErrPlatformSSOInvalid
	}
	access, err := secutils.EncryptAESGCM(req.AccessToken, secutils.GetAESKey())
	if err != nil {
		return nil, err
	}
	refresh, err := secutils.EncryptAESGCM(req.RefreshToken, secutils.GetAESKey())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	session := &types.PlatformSSOSession{ID: uuid.NewString(), Provider: c.BaseURL, Subject: info.ID, UserID: user.ID,
		AccessToken: access, RefreshToken: refresh, ExpiresAt: claims.ExpiresAt, CheckedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePlatformSSOSession(ctx, session); err != nil {
		return nil, fmt.Errorf("%w: persist SSO session", types.ErrPlatformSSOUnavailable)
	}
	user.PlatformSSOSessionID = &session.ID
	tenantID := s.resolveLoginTenantID(ctx, user)
	at, rt, err := s.generateTokensForTenant(ctx, user, tenantID)
	if err != nil {
		return nil, err
	}
	var tenant *types.Tenant
	if tenantID != 0 {
		tenant, err = s.tenantService.GetTenantByID(ctx, tenantID)
		if err != nil {
			return nil, err
		}
	}
	return &types.LoginResponse{Success: true, User: user, ActiveTenant: tenant, Memberships: s.buildMembershipsForUser(ctx, user, tenant), Token: at, RefreshToken: rt}, nil
}

func (s *userService) platformSSOUser(ctx context.Context, store interfaces.PlatformSSORepository, c *config.PlatformSSOConfig, info *platformSSOUser, mode types.TenantProvisioningMode) (*types.User, bool, error) {
	platformSSOProvisionMu.Lock()
	defer platformSSOProvisionMu.Unlock()
	identity, err := store.GetPlatformSSOIdentity(ctx, c.BaseURL, info.ID)
	if err != nil {
		return nil, false, fmt.Errorf("%w: look up SSO identity", types.ErrPlatformSSOUnavailable)
	}
	if identity != nil {
		u, err := s.userRepo.GetUserByID(ctx, identity.UserID)
		return u, false, err
	}
	// Explicit, operator-controlled linking only: an email-like account must not
	// silently take over an existing local user or a system-administrator account.
	var user *types.User
	for _, allowed := range c.LinkExistingEmails {
		if allowed == info.Account {
			user, err = s.userRepo.GetUserByEmail(ctx, allowed)
			if err != nil && !isUserLookupNotFound(err) {
				return nil, false, err
			}
			break
		}
	}
	fresh := user == nil
	if fresh {
		email := uuid.NewString() + "@platform-sso.invalid"
		if address, err := mail.ParseAddress(info.Account); err == nil && address.Address == info.Account {
			if existing, err := s.userRepo.GetUserByEmail(ctx, info.Account); err != nil && !isUserLookupNotFound(err) {
				return nil, false, err
			} else if existing != nil {
				return nil, false, fmt.Errorf("existing local account requires an administrator-approved SSO binding")
			}
			email = info.Account
		}
		password, err := generateRandomString(18)
		if err != nil {
			return nil, false, err
		}
		// Auto-provisioning must also work under the complex password policy.
		password = "Aa1!" + password
		name := sanitizeUsernameCandidate(info.Name)
		if name == "" {
			name = "platform"
		}
		if len([]rune(name)) > 35 {
			name = string([]rune(name)[:35])
		}
		user, err = s.Register(ctx, &types.RegisterRequest{Username: name + "-" + uuid.NewString()[:8], Email: email, Password: password, TenantProvisioning: mode})
		if err != nil {
			return nil, false, err
		}
		onlySSO := true
		user.Preferences.OidcOnlyLogin = &onlySSO
		if err := s.userRepo.UpdateUser(ctx, user); err != nil {
			return nil, false, err
		}
	}
	if !user.IsActive {
		return nil, false, types.ErrPlatformSSOInvalid
	}
	identity = &types.PlatformSSOIdentity{Provider: c.BaseURL, Subject: info.ID, UserID: user.ID, Account: info.Account, DisplayName: info.Name, CreatedAt: time.Now()}
	if err := store.BindPlatformSSOIdentity(ctx, identity); err != nil {
		// A cross-replica race is an error, never an excuse to associate by email.
		if fresh {
			_ = s.userRepo.DeleteUser(ctx, user.ID)
			if mode == types.TenantProvisioningCreatePersonal && user.TenantID != 0 {
				_ = s.tenantService.DeleteTenant(ctx, user.TenantID)
			}
		}
		return nil, false, fmt.Errorf("%w: bind SSO identity; retry login", types.ErrPlatformSSOUnavailable)
	}
	return user, fresh, nil
}

func (s *userService) validatePlatformSSOSession(ctx context.Context, id *string, userID string) error {
	if id == nil {
		if s.config.PlatformSSOOnlyLogin() {
			return types.ErrPlatformSSOInvalid
		}
		return nil
	}
	c, err := s.platformSSOConfig()
	if err != nil {
		return err
	}
	store, err := s.platformSSOStore()
	if err != nil {
		return err
	}
	var invalid bool
	var verificationErr error
	err = store.WithPlatformSSOSession(ctx, *id, func(session *types.PlatformSSOSession) error {
		if session.IsRevoked || session.UserID != userID || session.Provider != c.BaseURL {
			invalid = true
			return nil
		}
		now := time.Now()
		if now.Before(session.ExpiresAt.Add(-30*time.Second)) && now.Sub(session.CheckedAt) < 30*time.Second {
			return nil
		}
		access, err := secutils.DecryptStoredSecret(session.AccessToken)
		if err != nil {
			return fmt.Errorf("%w: decrypt SSO session", types.ErrPlatformSSOUnavailable)
		}
		if now.After(session.ExpiresAt.Add(-30 * time.Second)) {
			if session.RefreshToken == "" || c.ClientSecret == "" {
				if !now.Before(session.ExpiresAt) {
					session.IsRevoked = true
					invalid = true
					return nil
				}
			} else {
				previousRefresh := session.RefreshToken
				err := refreshPlatformSSOSession(ctx, c, session)
				if err == nil {
					session.CheckedAt, session.UpdatedAt = now, now
					return nil // The refreshed token was already verified online.
				}
				if errors.Is(err, types.ErrPlatformSSOInvalid) {
					session.IsRevoked = true
					invalid = true
					return nil
				}
				if session.RefreshToken != previousRefresh && errors.Is(err, types.ErrPlatformSSOUnavailable) {
					// Rotation invalidated the old refresh token. Commit the new pair
					// even when userinfo is temporarily unavailable, without authorizing
					// this request or extending the last verified expiry.
					verificationErr = err
					return nil
				}
				return err
			}
		}
		info, err := platformSSOUserInfo(ctx, c, access)
		if err != nil {
			if errors.Is(err, types.ErrPlatformSSOInvalid) {
				session.IsRevoked = true
				invalid = true
				return nil
			}
			return err
		}
		if info.ID != session.Subject {
			session.IsRevoked = true
			invalid = true
			return nil
		}
		if _, err := platformSSOReadClaims(access, c.ClientID, session.Subject); err != nil {
			session.IsRevoked = true
			invalid = true
			return nil
		}
		session.CheckedAt = now
		session.UpdatedAt = now
		return nil
	})
	if err != nil {
		if errors.Is(err, types.ErrPlatformSSOInvalid) || errors.Is(err, types.ErrPlatformSSOUnavailable) {
			return err
		}
		return fmt.Errorf("%w: read SSO session", types.ErrPlatformSSOUnavailable)
	}
	if invalid {
		return types.ErrPlatformSSOInvalid
	}
	return verificationErr
}

func refreshPlatformSSOSession(ctx context.Context, c *config.PlatformSSOConfig, session *types.PlatformSSOSession) error {
	refresh, err := secutils.DecryptStoredSecret(session.RefreshToken)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for key, value := range map[string]string{"grant_type": "refresh_token", "refresh_token": refresh, "client_id": c.ClientID, "client_secret": c.ClientSecret} {
		if err := w.WriteField(key, value); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := platformSSORequest(ctx, c, "/oauth/token", "", w.FormDataContentType(), &body, &response); err != nil {
		return err
	}
	if response.AccessToken == "" || response.RefreshToken == "" || len(response.AccessToken) > 32768 || len(response.RefreshToken) > 32768 {
		return types.ErrPlatformSSOInvalid
	}
	access, err := secutils.EncryptAESGCM(response.AccessToken, secutils.GetAESKey())
	if err != nil {
		return err
	}
	rt, err := secutils.EncryptAESGCM(response.RefreshToken, secutils.GetAESKey())
	if err != nil {
		return err
	}
	session.AccessToken, session.RefreshToken = access, rt
	// New claims are only trusted after the new access token is verified online.
	info, err := platformSSOUserInfo(ctx, c, response.AccessToken)
	if err != nil {
		return err
	}
	if info.ID != session.Subject {
		return types.ErrPlatformSSOInvalid
	}
	claims, err := platformSSOReadClaims(response.AccessToken, c.ClientID, session.Subject)
	if err != nil {
		return err
	}
	session.ExpiresAt = claims.ExpiresAt
	return nil
}
