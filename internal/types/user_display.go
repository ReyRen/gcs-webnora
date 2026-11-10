package types

import (
	"net/mail"
	"strings"
)

// DisplayName resolves public actor/creator labels without changing login identity.
// Generated SSO placeholder addresses are never useful display labels.
func (u *User) DisplayName() string {
	if u == nil {
		return ""
	}
	email := strings.TrimSpace(u.Email)
	if !strings.HasSuffix(strings.ToLower(email), "@platform-sso.invalid") {
		if address, err := mail.ParseAddress(email); err == nil && address.Address == email {
			return email
		}
	}
	return strings.TrimSpace(u.Username)
}
