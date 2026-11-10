package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserDisplayNamePrefersRealEmailAndPreservesIdentity(t *testing.T) {
	for _, tc := range []struct{ email, want string }{
		{" renyuan@test.com ", "renyuan@test.com"},
		{"", "renyuan-fff22cde"},
		{"id@PLATFORM-SSO.INVALID", "renyuan-fff22cde"},
		{"not an email", "renyuan-fff22cde"},
	} {
		u := &User{ID: "user-1", Username: "renyuan-fff22cde", Email: tc.email}
		require.Equal(t, tc.want, u.DisplayName())
		require.Equal(t, "renyuan-fff22cde", u.Username)
		encoded, err := json.Marshal(u)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"username":"renyuan-fff22cde"`)
	}
	var missing *User
	require.Empty(t, missing.DisplayName())
}
