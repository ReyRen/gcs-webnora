package config

import "testing"

func TestPlatformSSOConfiguration(t *testing.T) {
	t.Setenv("PLATFORM_SSO_ENABLED", "true")
	t.Setenv("PLATFORM_SSO_ONLY_LOGIN", "true")
	t.Setenv("PLATFORM_SSO_BASE_URL", "http://172.18.127.67:8018/DistributedAISystem/")
	t.Setenv("PLATFORM_SSO_CLIENT_ID", "gcs-webnora")
	t.Setenv("PLATFORM_SSO_PARENT_ORIGINS", "dsh-app://app,https://172.18.127.67")
	cfg := &Config{}
	applyPlatformSSOEnv(cfg)
	if !cfg.PlatformSSOOnlyLogin() {
		t.Fatal("exclusive SSO environment setting was ignored")
	}
	if err := validatePlatformSSO(cfg.PlatformSSO); err != nil {
		t.Fatal(err)
	}
	if cfg.PlatformSSO.BaseURL != "http://172.18.127.67:8018/DistributedAISystem" {
		t.Fatal("base URL not normalized")
	}
	if len(cfg.PlatformSSO.ParentOrigins) != 2 || cfg.PlatformSSO.ParentOrigins[0] != "dsh-app://app" {
		t.Fatal("desktop parent origin was not retained")
	}
	for _, invalid := range []string{"*", "null", "https://*.example.com", "https://172.18.127.67/anything", "https://user:pass@172.18.127.67", "javascript:alert(1)",
		"dsh-app://other", "dsh-app://app/", "dsh-app://app:443", "dsh-app://user@app", "dsh-app://app?x=1", "file://app"} {
		cfg.PlatformSSO.ParentOrigins = []string{invalid}
		if validatePlatformSSO(cfg.PlatformSSO) == nil {
			t.Fatalf("accepted origin %q", invalid)
		}
	}
	if validatePlatformSSO(&PlatformSSOConfig{OnlyLogin: true}) == nil {
		t.Fatal("exclusive SSO accepted while SSO was disabled")
	}
}
