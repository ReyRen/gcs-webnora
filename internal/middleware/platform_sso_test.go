package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
)

func TestPlatformSSOPublicEntryDoesNotRequireALocalLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Auth(nil, nil, nil, nil, nil))
	r.GET("/api/v1/auth/platform-sso/config", func(c *gin.Context) { c.Status(204) })
	r.POST("/api/v1/auth/platform-sso/exchange", func(c *gin.Context) { c.Status(204) })
	r.GET("/api/v1/knowledge-bases", func(c *gin.Context) { c.Status(204) })
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/api/v1/auth/platform-sso/config", 204}, {"POST", "/api/v1/auth/platform-sso/exchange", 204}, {"GET", "/api/v1/knowledge-bases", http.StatusUnauthorized}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.want {
			t.Fatalf("%s %s: status=%d want=%d", tc.method, tc.path, w.Code, tc.want)
		}
	}
	if isNoAuthAPI("/api/v1/auth/platform-sso/exchange", http.MethodGet) || isNoAuthAPI("/api/v1/auth/platform-sso/config", http.MethodPost) {
		t.Fatal("unexpected public SSO method")
	}
}

func TestPlatformSSOOnlyBlocksAlternativeLoginEntrypoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, exclusive := range []bool{false, true} {
		cfg := &config.Config{PlatformSSO: &config.PlatformSSOConfig{Enabled: true, OnlyLogin: exclusive}}
		r := gin.New()
		r.Use(Auth(nil, nil, nil, nil, cfg))
		for _, tc := range []struct {
			method, path string
			mixed, only  int
		}{
			{"POST", "/api/v1/auth/login", 204, 403}, {"POST", "/api/v1/auth/register", 204, 403},
			{"POST", "/api/v1/auth/register-by-invite", 204, 403}, {"POST", "/api/v1/auth/auto-setup", 204, 403},
			{"GET", "/api/v1/auth/oidc/url", 204, 403}, {"GET", "/api/v1/auth/oidc/start", 204, 403},
			{"GET", "/api/v1/auth/oidc/callback", 204, 403}, {"POST", "/api/v1/auth/change-password", 401, 403},
			{"GET", "/api/v1/auth/platform-sso/config", 204, 204}, {"POST", "/api/v1/auth/platform-sso/exchange", 204, 204},
			{"POST", "/api/v1/auth/refresh", 204, 204},
		} {
			r.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			want := tc.mixed
			if exclusive {
				want = tc.only
			}
			if w.Code != want {
				t.Fatalf("exclusive=%t %s %s: status=%d want=%d", exclusive, tc.method, tc.path, w.Code, want)
			}
		}
	}
}
