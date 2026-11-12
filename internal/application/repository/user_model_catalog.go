package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type userModelCatalogRepository struct{ db *gorm.DB }

// NewUserModelCatalogRepository reads exact application bindings. Its unscoped
// lookup must only be used after the service checks a resource binding grant.
func NewUserModelCatalogRepository(db *gorm.DB) interfaces.UserModelCatalogRepository {
	return &userModelCatalogRepository{db: db}
}

func (r *userModelCatalogRepository) GetBoundModel(ctx context.Context, id string) (*types.Model, error) {
	var model types.Model
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &model, nil
}

func (r *userModelCatalogRepository) CountReferences(ctx context.Context, id string) (types.ModelReferenceCounts, error) {
	var counts types.ModelReferenceCounts
	if err := scopeKnowledgeBasesByModelID(r.db.WithContext(ctx).Model(&types.KnowledgeBase{}), id).
		Count(&counts.KnowledgeBases).Error; err != nil {
		return counts, err
	}
	if err := scopeCustomAgentsByModelID(r.db.WithContext(ctx).Model(&types.CustomAgent{}), id).
		Count(&counts.Agents).Error; err != nil {
		return counts, err
	}
	query := r.db.WithContext(ctx).Model(&types.Tenant{})
	fields := []struct{ column, key string }{
		{"retrieval_config", "rerank_model_id"}, {"chat_history_config", "embedding_model_id"},
		{"memory_config", "embedding_model_id"}, {"memory_config", "extract_model_id"},
		{"default_settings", "chat_model_id"}, {"default_settings", "utility_model_id"},
		{"default_settings", "embedding_model_id"}, {"default_settings", "rerank_model_id"},
		{"default_settings", "vlm_model_id"}, {"default_settings", "asr_model_id"},
	}
	conditions := make([]string, 0, len(fields))
	args := make([]interface{}, 0, len(fields))
	for _, field := range fields {
		if r.db.Dialector.Name() == "postgres" {
			conditions = append(conditions, field.column+"->>'"+field.key+"' = ?")
		} else {
			conditions = append(conditions, "json_extract("+field.column+", '$."+field.key+"') = ?")
		}
		args = append(args, id)
	}
	err := query.Where(strings.Join(conditions, " OR "), args...).Count(&counts.TenantSettings).Error
	return counts, err
}
