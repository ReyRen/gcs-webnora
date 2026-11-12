package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type availableCatalogFixture struct {
	interfaces.UserModelCatalogService
	boundReads int
}

func (s *availableCatalogFixture) ListAvailableModels(context.Context) ([]types.AvailableModel, error) {
	return []types.AvailableModel{{Model: &types.Model{ID: "personal", TenantID: 2}, CanManage: true}}, nil
}

func (s *availableCatalogFixture) GetBoundModel(ctx context.Context, id string) (*types.Model, error) {
	if !types.HasModelBinding(ctx, id) {
		panic("missing exact resource grant")
	}
	s.boundReads++
	return &types.Model{ID: id, TenantID: 3, Parameters: types.ModelParameters{BaseURL: "https://private.example", APIKey: "private-key"}}, nil
}

type catalogKBFixture struct {
	interfaces.KnowledgeBaseService
}

func (s *catalogKBFixture) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: "shared-kb", TenantID: 3, EmbeddingModelID: "bound-embedding"}, nil
}

type catalogShareFixture struct {
	interfaces.KBShareService
	allowed bool
}

func (s *catalogShareFixture) CheckTenantKBPermission(context.Context, string, uint64, types.TenantRole) (types.OrgMemberRole, bool, error) {
	return types.OrgRoleViewer, s.allowed, nil
}

func TestUserModelCatalogSharedBindingsDisappearAfterRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	catalog := &availableCatalogFixture{}
	shares := &catalogShareFixture{allowed: true}
	h := &ModelHandler{catalog: catalog, kbService: &catalogKBFixture{}, kbShares: shares}
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)),
		types.Caller{TenantID: 2, UserID: "collaborator", Role: types.TenantRoleOwner})
	request := func() (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", "/models/available?knowledge_base_id=shared-kb", nil).WithContext(ctx)
		h.ListAvailableModels(c)
		return c, recorder
	}
	c, response := request()
	require.Empty(t, c.Errors)
	var body struct {
		Data []struct {
			ID        string `json:"id"`
			CanManage bool   `json:"can_manage"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data, 2)
	require.Equal(t, "bound-embedding", body.Data[1].ID)
	require.False(t, body.Data[1].CanManage)
	require.NotContains(t, response.Body.String(), "private.example")
	require.NotContains(t, response.Body.String(), "private-key")
	require.Equal(t, 1, catalog.boundReads)
	shares.allowed = false
	c, _ = request()
	require.NotEmpty(t, c.Errors)
	require.Equal(t, 1, catalog.boundReads, "revocation must reject before a bound model lookup")
}
