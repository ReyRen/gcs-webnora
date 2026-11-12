package repository

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func modelReferenceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupModelUsageTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.Model{}, &types.Tenant{}, &types.User{}, &types.TenantMember{}, &modelReferencePolicy{}))
	require.NoError(t, db.Create(&modelReferencePolicy{ID: 1}).Error)
	for _, id := range []uint64{1, 2, 3} {
		require.NoError(t, db.Create(&types.Tenant{ID: id, Name: "fixture"}).Error)
	}
	require.NoError(t, db.Create(&types.User{ID: "owner", Username: "owner", Email: "owner@example.test", PasswordHash: "fixture", TenantID: 1, IsActive: true}).Error)
	require.NoError(t, db.Create(&types.User{ID: "collaborator", Username: "collaborator", Email: "collaborator@example.test", PasswordHash: "fixture", TenantID: 2, IsActive: true}).Error)
	for _, member := range []*types.TenantMember{
		{UserID: "owner", TenantID: 1, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive},
		{UserID: "owner", TenantID: 2, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive},
		{UserID: "collaborator", TenantID: 2, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive},
	} {
		require.NoError(t, db.Create(member).Error)
	}
	for _, model := range []*types.Model{
		{ID: "personal", TenantID: 1, Name: "personal", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		{ID: "private", TenantID: 3, Name: "private", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
	} {
		require.NoError(t, db.Create(model).Error)
	}
	return db
}

func TestModelReferenceWritesRejectForgedIDsInEveryResource(t *testing.T) {
	db := modelReferenceDB(t)
	require.NoError(t, InstallModelReferenceValidation(db, &config.Config{}))
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)), types.Caller{TenantID: 2, UserID: "owner", Role: types.TenantRoleOwner})
	kb := makeKB(nil)
	kb.TenantID = 2
	kb.EmbeddingModelID = ""
	kb.SummaryModelID = "personal"
	require.NoError(t, db.WithContext(ctx).Create(kb).Error)
	collaborator := types.WithCaller(ctx, types.Caller{TenantID: 2, UserID: "collaborator", Role: types.TenantRoleOwner})
	retained := *kb
	retained.Name = "retained binding"
	require.NoError(t, db.WithContext(collaborator).Save(&retained).Error)
	retained.VLMConfig = types.VLMConfig{Enabled: false, ModelID: "private"}
	forgedCtx := types.WithModelBindings(collaborator, "private")
	require.Error(t, db.WithContext(forgedCtx).Save(&retained).Error, "a resource grant cannot authorize a new reference")
	var stored types.KnowledgeBase
	require.NoError(t, db.First(&stored, "id = ?", kb.ID).Error)
	require.Empty(t, stored.VLMConfig.ModelID)
	agent := &types.CustomAgent{ID: "forged-agent", TenantID: 2, Name: "forged", Config: types.CustomAgentConfig{ModelID: "private"}}
	require.Error(t, db.WithContext(forgedCtx).Create(agent).Error)
	require.Error(t, db.WithContext(forgedCtx).Model(&types.Tenant{}).Where("id = ?", 2).Update("default_settings", &types.DefaultSettings{ChatModelID: "private"}).Error)
	require.NoError(t, db.WithContext(ctx).Model(&types.Tenant{}).Where("id = ?", 2).Update("default_settings", &types.DefaultSettings{ChatModelID: "personal"}).Error)
}

func TestModelReferencePolicyRequiresLegacyReviewAndPreservesActivatedData(t *testing.T) {
	db := modelReferenceDB(t)
	// Only the real deployment initializer activates the policy. Direct writes
	// here deliberately construct the pre-upgrade database being audited.
	kb := makeKB(nil)
	kb.TenantID = 2
	kb.EmbeddingModelID = ""
	kb.SummaryModelID = "personal"
	require.NoError(t, db.Create(kb).Error)
	require.ErrorContains(t, EnsureModelReferencePolicy(db), "migration review")
	kb.TenantID = 1
	require.NoError(t, db.Save(kb).Error)
	require.NoError(t, db.Exec("CREATE TABLE knowledges (id TEXT PRIMARY KEY, tenant_id INTEGER, embedding_model_id TEXT, deleted_at DATETIME)").Error)
	require.NoError(t, EnsureModelReferencePolicy(db))
	require.NoError(t, EnsureModelReferencePolicy(db))
	require.NoError(t, InstallModelReferenceValidation(db, &config.Config{}))
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)), types.Caller{TenantID: 2, UserID: "owner", Role: types.TenantRoleOwner})
	kb.ID = "authorized-cross-reference"
	kb.TenantID = 2
	require.NoError(t, db.WithContext(ctx).Create(kb).Error)
	require.NoError(t, EnsureModelReferencePolicy(db), "validated new references survive a restart")
	down, err := os.ReadFile("../../../migrations/sqlite/000055_user_model_reference_policy.down.sql")
	require.NoError(t, err)
	require.Error(t, db.Exec(string(down)).Error)
	var policy modelReferencePolicy
	require.NoError(t, db.First(&policy, 1).Error)
	require.Equal(t, 1, policy.PolicyVersion)
}

func TestModelReferenceWritesRecheckMembershipAndKeepKeysScoped(t *testing.T) {
	db := modelReferenceDB(t)
	require.NoError(t, InstallModelReferenceValidation(db, &config.Config{}))
	ctx := types.WithCaller(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2)), types.Caller{TenantID: 2, UserID: "owner", Role: types.TenantRoleOwner})
	create := func(ctx context.Context, id, model string) error {
		kb := makeKB(nil)
		kb.ID, kb.TenantID, kb.EmbeddingModelID, kb.SummaryModelID = id, 2, "", model
		return db.WithContext(ctx).Create(kb).Error
	}
	key := types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 7})
	require.Error(t, create(key, "key-cross-owner", "personal"), "creator membership cannot widen a scoped API key")
	require.NoError(t, db.Model(&types.Model{}).Where("id = ?", "personal").Update("tenant_id", 2).Error)
	require.NoError(t, create(key, "key-own-source", "personal"))
	require.NoError(t, db.Model(&types.Model{}).Where("id = ?", "personal").Update("tenant_id", 1).Error)
	require.NoError(t, db.Where("user_id = ? AND tenant_id = ?", "owner", 1).Delete(&types.TenantMember{}).Error)
	require.Error(t, create(ctx, "revoked-membership", "personal"), "an already-issued session cannot retain revoked model access")
	require.NoError(t, db.Model(&types.User{}).Where("id = ?", "owner").Update("can_access_all_tenants", true).Error)
	require.Error(t, create(ctx, "global-disabled", "private"), "the user flag alone does not enable global access")
	other := modelReferenceDB(t)
	require.NoError(t, InstallModelReferenceValidation(other, &config.Config{Tenant: &config.TenantConfig{EnableCrossTenantAccess: true}}))
	require.NoError(t, other.Model(&types.User{}).Where("id = ?", "owner").Update("can_access_all_tenants", true).Error)
	kb := makeKB(nil)
	kb.EmbeddingModelID, kb.SummaryModelID, kb.TenantID = "", "private", 2
	require.NoError(t, other.WithContext(ctx).Create(kb).Error)
}
