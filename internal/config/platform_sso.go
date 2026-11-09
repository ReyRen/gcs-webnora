package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// PlatformSSOConfig describes the toolchain's documented token/userinfo protocol.
// It is independent of OIDC: no authorization-code or discovery endpoint is used.
type PlatformSSOConfig struct {
	Enabled            bool     `yaml:"enabled"`
	OnlyLogin          bool     `yaml:"only_login"`
	BaseURL            string   `yaml:"base_url"`
	ClientID           string   `yaml:"client_id"`
	ClientSecret       string   `yaml:"-" json:"-"`
	ParentOrigins      []string `yaml:"parent_origins"`
	LinkExistingEmails []string `yaml:"link_existing_emails"`
}

func applyPlatformSSOEnv(cfg *Config) {
	if cfg.PlatformSSO == nil {
		cfg.PlatformSSO = &PlatformSSOConfig{}
	}
	c := cfg.PlatformSSO
	if v := os.Getenv("PLATFORM_SSO_ENABLED"); v != "" {
		c.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PLATFORM_SSO_ONLY_LOGIN"); v != "" {
		c.OnlyLogin = strings.EqualFold(v, "true")
	}
	for key, dest := range map[string]*string{
		"PLATFORM_SSO_BASE_URL": &c.BaseURL, "PLATFORM_SSO_CLIENT_ID": &c.ClientID,
		"PLATFORM_SSO_CLIENT_SECRET": &c.ClientSecret,
	} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			*dest = v
		}
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	for key, dest := range map[string]*[]string{
		"PLATFORM_SSO_PARENT_ORIGINS":       &c.ParentOrigins,
		"PLATFORM_SSO_LINK_EXISTING_EMAILS": &c.LinkExistingEmails,
	} {
		if v, ok := os.LookupEnv(key); ok {
			*dest = nil
			for _, item := range strings.Split(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					*dest = append(*dest, item)
				}
			}
		}
	}
}

func validatePlatformSSO(c *PlatformSSOConfig) error {
	if c == nil {
		return nil
	}
	if c.OnlyLogin && !c.Enabled {
		return fmt.Errorf("platform_sso.only_login requires platform_sso.enabled")
	}
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("platform_sso.base_url must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if c.ClientID == "" || len(c.ParentOrigins) == 0 {
		return fmt.Errorf("platform_sso requires client_id and parent_origins")
	}
	for _, origin := range c.ParentOrigins {
		v, err := url.Parse(origin)
		if err != nil || v.Host == "" || strings.Contains(v.Host, "*") || v.User != nil || v.Path != "" || v.RawQuery != "" || v.Fragment != "" ||
			(v.Scheme != "https" && v.Scheme != "http" && origin != "dsh-app://app") {
			return fmt.Errorf("platform_sso.parent_origins must contain exact HTTP(S) origins or dsh-app://app")
		}
	}
	return nil
}

func (cfg *Config) PlatformSSOOnlyLogin() bool {
	return cfg != nil && cfg.PlatformSSO != nil && cfg.PlatformSSO.Enabled && cfg.PlatformSSO.OnlyLogin
}
