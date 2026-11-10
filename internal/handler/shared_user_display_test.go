package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type displayTestUsers struct {
	interfaces.UserService
	user *types.User
}

func (s *displayTestUsers) GetUserByID(context.Context, string) (*types.User, error) {
	return s.user, nil
}
func (s *displayTestUsers) GetUsersByIDs(context.Context, []string) (map[string]*types.User, error) {
	return map[string]*types.User{s.user.ID: s.user}, nil
}

type displayTestOrg struct {
	interfaces.OrganizationService
	denied bool
}

func (s *displayTestOrg) GetTenantMember(context.Context, string, uint64) (*types.OrganizationTenantMember, error) {
	if s.denied {
		return nil, errors.New("not a member")
	}
	return &types.OrganizationTenantMember{Role: types.OrgRoleViewer}, nil
}

type displayTestKBShares struct {
	interfaces.KBShareService
	share *types.KnowledgeBaseShare
}

func (s *displayTestKBShares) ListSharesByOrganization(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	return []*types.KnowledgeBaseShare{s.share}, nil
}

type displayTestAgentShares struct {
	interfaces.AgentShareService
	share *types.AgentShare
}

func (s *displayTestAgentShares) ListSharesByOrganization(context.Context, string) ([]*types.AgentShare, error) {
	return []*types.AgentShare{s.share}, nil
}

func TestSharedActorLabelsResolveCurrentAccountWithoutChangingHistoricShares(t *testing.T) {
	gin.SetMode(gin.TestMode)
	u := &types.User{ID: "owner", Username: "renyuan-fff22cde", Email: "renyuan@test.com"}
	users := &displayTestUsers{user: u}
	org := &displayTestOrg{}
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	kb := &types.KnowledgeBaseShare{ID: "old-share", KnowledgeBaseID: "kb", SharedByUserID: u.ID, SourceTenantID: 42, Permission: types.OrgRoleEditor, CreatedAt: past}
	agent := &types.AgentShare{ID: "old-share", AgentID: "agent", SharedByUserID: u.ID, SourceTenantID: 42, Permission: types.OrgRoleEditor, CreatedAt: past}
	h := &OrganizationHandler{orgService: org, userService: users,
		shareService: &displayTestKBShares{share: kb}, agentShareService: &displayTestAgentShares{share: agent}}
	for _, call := range []func(*gin.Context){h.ListOrgShares, h.ListOrgAgentShares} {
		for _, tc := range []struct{ email, want string }{
			{"renyuan@test.com", "renyuan@test.com"}, {"updated@test.com", "updated@test.com"},
			{"id@platform-sso.invalid", "renyuan-fff22cde"},
		} {
			u.Email = tc.email
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest("GET", "/", nil)
			c.Params = gin.Params{{Key: "id", Value: "org"}}
			c.Set(types.TenantIDContextKey.String(), uint64(42))
			call(c)
			require.Empty(t, c.Errors)
			var body struct {
				Data struct {
					Shares []struct {
						SharedByUsername string `json:"shared_by_username"`
						SharedByUserID   string `json:"shared_by_user_id"`
						MyPermission     string `json:"my_permission"`
					} `json:"shares"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(r.Body.Bytes(), &body))
			require.Len(t, body.Data.Shares, 1)
			require.Equal(t, tc.want, body.Data.Shares[0].SharedByUsername)
			require.Equal(t, "owner", body.Data.Shares[0].SharedByUserID)
			require.Equal(t, "viewer", body.Data.Shares[0].MyPermission)
		}
	}
	require.Equal(t, "renyuan-fff22cde", u.Username)
	require.Equal(t, past, kb.CreatedAt)
	require.Equal(t, past, agent.CreatedAt)
	org.denied = true
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Request = httptest.NewRequest("GET", "/", nil)
	h.ListOrgShares(c)
	require.NotEmpty(t, c.Errors)
	require.NotContains(t, r.Body.String(), "shared_by_username")
}

func TestCreatorLabelsUseTheSameAccountDisplayName(t *testing.T) {
	u := &types.User{ID: "creator", Username: "creator-12345678", Email: "creator@test.com"}
	users := &displayTestUsers{user: u}
	kbs := []*types.KnowledgeBase{{ID: "kb", CreatorID: u.ID}}
	agents := []*types.CustomAgent{{ID: "agent", CreatedBy: u.ID}}
	enrichKBCreatorNames(t.Context(), users, kbs)
	enrichAgentCreatorNames(t.Context(), users, agents)
	require.Equal(t, u.Email, kbs[0].CreatorName)
	require.Equal(t, u.Email, agents[0].CreatorName)
	settings := []*types.SystemSetting{{LastModifiedBy: u.ID}}
	(&SystemHandler{userSvc: users}).enrichSettingsModifiedBy(t.Context(), settings)
	require.Equal(t, u.Email, settings[0].LastModifiedByName)
}
