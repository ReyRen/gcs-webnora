package service

import (
	"context"
	"sort"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type userModelScope struct {
	tenantID uint64
	role     types.TenantRole
}

type userModelCatalogService struct {
	models           interfaces.ModelRepository
	bindings         interfaces.UserModelCatalogRepository
	members          interfaces.TenantMemberRepository
	users            interfaces.UserRepository
	tenants          interfaces.TenantService
	allowCrossTenant bool
}

// NewUserModelCatalogService provides the user's model directory without changing
// model ownership, credentials, application selections or tenant memberships.
func NewUserModelCatalogService(
	models interfaces.ModelRepository,
	bindings interfaces.UserModelCatalogRepository,
	members interfaces.TenantMemberRepository,
	users interfaces.UserRepository,
	tenants interfaces.TenantService,
	cfg *config.Config,
) interfaces.UserModelCatalogService {
	return &userModelCatalogService{
		models: models, bindings: bindings, members: members, users: users, tenants: tenants,
		allowCrossTenant: cfg.Tenant != nil && cfg.Tenant.EnableCrossTenantAccess,
	}
}

func (s *userModelCatalogService) userScopes(ctx context.Context) (*types.User, []userModelScope, error) {
	if _, scopedKey := types.TenantAPIKeyScopeFromContext(ctx); scopedKey {
		return nil, nil, apperrors.NewForbiddenError("API Key 仅能使用其授权空间的模型")
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID == "" {
		return nil, nil, apperrors.NewUnauthorizedError("请登录平台后读取可用模型")
	}
	user, err := s.users.GetUserByID(ctx, caller.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil || !user.IsActive {
		return nil, nil, apperrors.NewUnauthorizedError("用户账号不可用")
	}
	memberships, err := s.members.ListByUser(ctx, caller.UserID)
	if err != nil {
		return nil, nil, err
	}
	roles := make(map[uint64]types.TenantRole, len(memberships))
	for _, member := range memberships {
		if member != nil && member.Status == types.TenantMemberStatusActive && member.Role.IsValid() {
			roles[member.TenantID] = member.Role
		}
	}
	if user.CanAccessAllTenants && s.allowCrossTenant {
		tenants, err := s.tenants.ListAllTenants(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, tenant := range tenants {
			if tenant != nil {
				roles[tenant.ID] = types.TenantRoleOwner
			}
		}
	}
	scopes := make([]userModelScope, 0, len(roles))
	for id, role := range roles {
		scopes = append(scopes, userModelScope{tenantID: id, role: role})
	}
	sort.Slice(scopes, func(i, j int) bool {
		if (scopes[i].tenantID == user.TenantID) != (scopes[j].tenantID == user.TenantID) {
			return scopes[i].tenantID == user.TenantID
		}
		return scopes[i].tenantID < scopes[j].tenantID
	})
	return user, scopes, nil
}

// ListAvailableModels returns one entry per authorized model ID. Models from a
// shared knowledge base alone are not added to this general-purpose directory.
func (s *userModelCatalogService) ListAvailableModels(ctx context.Context) ([]types.AvailableModel, error) {
	user, scopes, err := s.userScopes(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]types.AvailableModel, 0)
	seen := make(map[string]bool)
	appendModels := func(models []*types.Model, canManage bool, builtinOnly bool) {
		for _, model := range models {
			if model == nil || seen[model.ID] || builtinOnly && !model.IsBuiltin {
				continue
			}
			seen[model.ID] = true
			manage := canManage
			if model.IsBuiltin {
				manage = user.IsSystemAdmin
			}
			rows = append(rows, types.AvailableModel{Model: model, CanManage: manage})
		}
	}
	for _, scope := range scopes {
		models, err := s.models.List(ctx, scope.tenantID, "", "")
		if err != nil {
			return nil, err
		}
		appendModels(models, scope.role.HasPermission(types.TenantRoleAdmin), false)
	}
	if len(scopes) == 0 {
		models, err := s.models.List(ctx, 0, "", "")
		if err != nil {
			return nil, err
		}
		appendModels(models, false, true)
	}
	// Defaults remain an application choice in the active business tenant.
	activeTenant, _ := types.TenantIDFromContext(ctx)
	models := make([]*types.Model, 0, len(rows))
	for _, row := range rows {
		if row.Model.TenantID != activeTenant && !row.Model.IsBuiltin {
			row.Model.IsDefault = false
			row.Model.DefaultFor = nil
		}
		models = append(models, row.Model)
	}
	if activeTenant != 0 {
		tenant, err := s.tenants.GetTenantByID(ctx, activeTenant)
		if err != nil {
			return nil, err
		}
		if tenant != nil && tenant.DefaultSettings != nil {
			tenant.DefaultSettings.ApplyModelDefaults(models)
		}
	}
	return rows, nil
}

// GetAvailableModel resolves only models the current user may use directly.
// It does not grant edit, credential, copy or deletion rights.
func (s *userModelCatalogService) GetAvailableModel(ctx context.Context, modelID string) (*types.Model, error) {
	_, scopes, err := s.userScopes(ctx)
	if err != nil {
		return nil, err
	}
	for _, scope := range scopes {
		model, err := s.models.GetByID(ctx, scope.tenantID, modelID)
		if err != nil {
			return nil, err
		}
		if model != nil {
			return model, nil
		}
	}
	model, err := s.models.GetByID(ctx, 0, modelID)
	if err != nil {
		return nil, err
	}
	if model != nil && model.IsBuiltin {
		return model, nil
	}
	return nil, ErrModelNotFound
}

// GetBoundModel resolves only the exact model of an already-authorized resource.
func (s *userModelCatalogService) GetBoundModel(ctx context.Context, modelID string) (*types.Model, error) {
	if !types.HasModelBinding(ctx, modelID) {
		return nil, apperrors.NewForbiddenError("无权使用该业务绑定的模型")
	}
	model, err := s.bindings.GetBoundModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	if model == nil {
		return nil, ErrModelNotFound
	}
	return model, nil
}

// CountReferences protects application bindings without disclosing resource names.
func (s *userModelCatalogService) CountReferences(ctx context.Context, modelID string) (types.ModelReferenceCounts, error) {
	return s.bindings.CountReferences(ctx, modelID)
}
