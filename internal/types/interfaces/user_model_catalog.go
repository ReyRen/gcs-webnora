package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// UserModelCatalogService resolves the current user's available models independently
// of the active business tenant. Resource binding grants permit execution only.
type UserModelCatalogService interface {
	ListAvailableModels(ctx context.Context) ([]types.AvailableModel, error)
	GetAvailableModel(ctx context.Context, modelID string) (*types.Model, error)
	GetBoundModel(ctx context.Context, modelID string) (*types.Model, error)
	CountReferences(ctx context.Context, modelID string) (types.ModelReferenceCounts, error)
}

// UserModelCatalogRepository reads exact model bindings after service authorization.
// Unscoped records and global reference names must never be returned directly to clients.
type UserModelCatalogRepository interface {
	GetBoundModel(ctx context.Context, modelID string) (*types.Model, error)
	CountReferences(ctx context.Context, modelID string) (types.ModelReferenceCounts, error)
}
