package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type catalogModels struct {
	interfaces.ModelRepository
	rows    []*types.Model
	deleted bool
}

func (r *catalogModels) List(_ context.Context, tenant uint64, _ types.ModelType, _ types.ModelSource) ([]*types.Model, error) {
	var rows []*types.Model
	for _, row := range r.rows {
		if row.TenantID == tenant || row.IsBuiltin {
			copy := *row
			rows = append(rows, &copy)
		}
	}
	return rows, nil
}

func (r *catalogModels) GetByID(ctx context.Context, tenant uint64, id string) (*types.Model, error) {
	rows, _ := r.List(ctx, tenant, "", "")
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return nil, nil
}

func (r *catalogModels) Delete(context.Context, uint64, string) error { r.deleted = true; return nil }

type catalogBindings struct {
	interfaces.UserModelCatalogRepository
	models *catalogModels
	counts types.ModelReferenceCounts
}

func (r *catalogBindings) GetBoundModel(_ context.Context, id string) (*types.Model, error) {
	for _, model := range r.models.rows {
		if model.ID == id {
			copy := *model
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *catalogBindings) CountReferences(context.Context, string) (types.ModelReferenceCounts, error) {
	return r.counts, nil
}

type catalogMembers struct {
	interfaces.TenantMemberRepository
	rows []*types.TenantMember
}

func (r *catalogMembers) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	return r.rows, nil
}

type catalogUsers struct{ interfaces.UserRepository }

func (r *catalogUsers) GetUserByID(context.Context, string) (*types.User, error) {
	return &types.User{ID: "viewer", TenantID: 1, IsActive: true}, nil
}

type catalogTenants struct{ interfaces.TenantService }

func (r *catalogTenants) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func TestUserModelCatalogPreservesCredentialOwnership(t *testing.T) {
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)),
		types.Caller{TenantID: 2, UserID: "collaborator", Role: types.TenantRoleOwner})
	private := modelCredentialsContext(ctx, &types.Model{TenantID: 1})
	require.Equal(t, uint64(1), types.MustTenantIDFromContext(private))
	require.Equal(t, types.CallerFromContext(ctx), types.CallerFromContext(private))
	builtin := &types.Model{TenantID: 10000, IsBuiltin: true}
	require.Equal(t, uint64(2), types.MustTenantIDFromContext(modelCredentialsContext(ctx, builtin)))
	shared := types.WithExecutionTenant(ctx, 1)
	require.Equal(t, uint64(1), types.MustTenantIDFromContext(modelCredentialsContext(shared, builtin)))
}

func TestUserModelCatalogPreservesIDsAndRechecksMembership(t *testing.T) {
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)),
		types.Caller{TenantID: 2, UserID: "viewer", Role: types.TenantRoleViewer})
	models := &catalogModels{rows: []*types.Model{
		{ID: "personal", TenantID: 1, Name: "same-model", Status: types.ModelStatusActive},
		{ID: "collaborative", TenantID: 2, Name: "same-model", Status: types.ModelStatusActive},
		{ID: "private", TenantID: 3, Name: "private-model", Status: types.ModelStatusActive},
		{ID: "builtin", TenantID: 10000, IsBuiltin: true, Status: types.ModelStatusActive},
	}}
	members := &catalogMembers{rows: []*types.TenantMember{
		{TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive},
		{TenantID: 2, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive},
		{TenantID: 3, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusSuspended},
	}}
	bindings := &catalogBindings{models: models}
	catalog := NewUserModelCatalogService(models, bindings, members, &catalogUsers{}, &catalogTenants{}, &config.Config{Tenant: &config.TenantConfig{}})
	_, err := catalog.ListAvailableModels(types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true}))
	require.Error(t, err)
	rows, err := catalog.ListAvailableModels(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	byID := make(map[string]types.AvailableModel)
	for _, row := range rows {
		byID[row.Model.ID] = row
	}
	require.True(t, byID["personal"].CanManage)
	require.False(t, byID["collaborative"].CanManage)
	require.False(t, byID["builtin"].CanManage)
	require.NotContains(t, byID, "private")
	model, err := catalog.GetAvailableModel(ctx, "personal")
	require.NoError(t, err)
	require.Equal(t, uint64(1), model.TenantID)
	members.rows = members.rows[:1]
	_, err = catalog.GetAvailableModel(ctx, "collaborative")
	require.ErrorIs(t, err, ErrModelNotFound)

	svc := NewModelService(models, &stubKBRepoForModelDelete{}, &stubAgentRepoForModelDelete{}, nil, nil, nil, catalog)
	_, err = svc.GetUsableModel(ctx, "private")
	require.ErrorIs(t, err, ErrModelNotFound)
	_, err = svc.GetUsableModel(types.WithExecutionTenant(ctx, 3), "private")
	require.ErrorIs(t, err, ErrModelNotFound)
	resourceCtx := types.WithModelBindings(ctx, "private")
	model, err = svc.GetUsableModel(resourceCtx, "private")
	require.NoError(t, err)
	require.Equal(t, "private", model.ID)
	_, err = svc.GetModelByID(resourceCtx, "private")
	require.ErrorIs(t, err, ErrModelNotFound)
	_, err = svc.GetUsableModel(types.WithModelBindings(resourceCtx, "personal"), "private")
	require.ErrorIs(t, err, ErrModelNotFound)
	require.Equal(t, types.CallerFromContext(ctx), types.CallerFromContext(resourceCtx))

	bindings.counts.KnowledgeBases = 1
	ownerCtx := types.WithExecutionTenant(ctx, 1)
	err = svc.DeleteModel(ownerCtx, "personal")
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperrors.ErrModelInUse, appErr.Code)
	require.False(t, models.deleted)
}
