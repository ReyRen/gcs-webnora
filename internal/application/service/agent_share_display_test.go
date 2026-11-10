package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSharedAgentInfoResolvesLiveAccountDisplayName(t *testing.T) {
	u := &types.User{ID: "owner", Username: "owner-12345678", Email: "owner@test.com"}
	s := &agentShareService{userRepo: &platformTestUsers{users: map[string]*types.User{u.ID: u}}}
	share := &types.AgentShare{ID: "old-share", SharedByUserID: u.ID, SourceTenantID: 42,
		Agent: &types.CustomAgent{ID: "agent"}}
	for _, email := range []string{"owner@test.com", "updated@test.com"} {
		u.Email = email
		info := s.sharedAgentInfo(t.Context(), share, types.OrgRoleViewer, map[string]bool{})
		require.Equal(t, email, info.SharedByUsername)
		require.Equal(t, u.ID, info.SharedByUserID)
		require.Equal(t, types.OrgRoleViewer, info.Permission)
	}
	require.Equal(t, "owner-12345678", u.Username)
}
