package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserModelCatalogCountsBindingsAcrossBusinessTenants(t *testing.T) {
	db := setupModelUsageTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, memory_config TEXT, default_settings TEXT, retrieval_config TEXT, chat_history_config TEXT, deleted_at DATETIME)`).Error)
	repo := NewUserModelCatalogRepository(db)
	kb := makeKB(nil)
	kb.TenantID = 29
	kb.EmbeddingModelID = "personal-model"
	require.NoError(t, db.Create(kb).Error)
	require.NoError(t, db.Exec(`INSERT INTO tenants (id, memory_config, default_settings) VALUES (31, '{}', '{"chat_model_id":"personal-model"}')`).Error)
	counts, err := repo.CountReferences(context.Background(), "personal-model")
	require.NoError(t, err)
	require.Equal(t, int64(1), counts.KnowledgeBases)
	require.Equal(t, int64(1), counts.TenantSettings)
	require.True(t, counts.InUse())
	require.NoError(t, db.Delete(kb).Error)
	require.NoError(t, db.Exec("UPDATE tenants SET deleted_at = CURRENT_TIMESTAMP").Error)
	counts, err = repo.CountReferences(context.Background(), "personal-model")
	require.NoError(t, err)
	require.False(t, counts.InUse())
}
