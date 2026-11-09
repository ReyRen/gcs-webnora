package service

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Run only against an isolated throwaway database created by the validation
// command. This exercises the actual PostgreSQL row lock across service instances.
func TestPlatformSSOPostgresSerializedRefresh(t *testing.T) {
	dsn := os.Getenv("PLATFORM_SSO_TEST_DSN")
	if dsn == "" {
		t.Skip("PLATFORM_SSO_TEST_DSN is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.PlatformSSOSession{}, &types.PlatformSSOIdentity{}); err != nil {
		t.Fatal(err)
	}
	defer db.Migrator().DropTable(&types.PlatformSSOSession{}, &types.PlatformSSOIdentity{})
	var refreshes atomic.Int32
	var rotated string
	s, _ := platformTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			refreshes.Add(1)
			time.Sleep(50 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": rotated, "refresh_token": "next-refresh"})
			return
		}
		platformWriteUser(w)
	})
	rotated = platformTestJWT(t, "weknora", "446", time.Now().Add(time.Hour))
	tokens := apprepo.NewAuthTokenRepository(db)
	s.tokenRepo = tokens
	store := tokens.(interfaces.PlatformSSORepository)
	at, _ := secutils.EncryptAESGCM(platformTestJWT(t, "weknora", "446", time.Now().Add(5*time.Second)), secutils.GetAESKey())
	rt, _ := secutils.EncryptAESGCM("previous-refresh", secutils.GetAESKey())
	session := &types.PlatformSSOSession{ID: "postgres-refresh-test", Provider: s.config.PlatformSSO.BaseURL, Subject: "446", UserID: "user", AccessToken: at, RefreshToken: rt, ExpiresAt: time.Now().Add(5 * time.Second), CheckedAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.CreatePlatformSSOSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A different service object and repository per call, sharing only SQL.
			replica := &userService{config: s.config, tokenRepo: apprepo.NewAuthTokenRepository(db)}
			if err := replica.validatePlatformSSOSession(context.Background(), &session.ID, "user"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("cross-replica refreshes=%d, want 1", refreshes.Load())
	}
	var persisted types.PlatformSSOSession
	if err := db.First(&persisted, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	secret, err := secutils.DecryptStoredSecret(persisted.RefreshToken)
	if err != nil || secret != "next-refresh" {
		t.Fatal("rotated refresh token was not persisted atomically")
	}
}
