package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ErrModelNotFound is returned when a model cannot be found in the repository
var ErrModelNotFound = errors.New("model not found")

// modelService implements the model service interface
type modelService struct {
	repo          interfaces.ModelRepository
	kbRepo        interfaces.KnowledgeBaseRepository
	agentRepo     interfaces.CustomAgentRepository
	ollamaService *ollama.OllamaService
	pooler        embedding.EmbedderPooler
	tenantService interfaces.TenantService
	catalog       interfaces.UserModelCatalogService
}

// NewModelService creates a new model service instance
func NewModelService(repo interfaces.ModelRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
	agentRepo interfaces.CustomAgentRepository,
	ollamaService *ollama.OllamaService,
	pooler embedding.EmbedderPooler,
	tenantService interfaces.TenantService,
	catalog interfaces.UserModelCatalogService,
) interfaces.ModelService {
	return &modelService{
		repo:          repo,
		kbRepo:        kbRepo,
		agentRepo:     agentRepo,
		ollamaService: ollamaService,
		pooler:        pooler,
		tenantService: tenantService,
		catalog:       catalog,
	}
}

// decryptAppSecret 解密 AppSecret（如果为空或 cryptoSvc 为空则原样返回）
func (s *modelService) decryptAppSecret(encrypted string) string {
	if encrypted == "" {
		return encrypted
	}
	if key := utils.GetAESKey(); key != nil {
		if encrypted, err := utils.DecryptAESGCM(encrypted, key); err == nil {
			return encrypted
		}
	}
	return encrypted
}

// resolveWeKnoraCloudCredentials 为 WeKnoraCloud 厂商模型补全 AppID/AppSecret。
// 当模型自身参数中未存储凭证时，自动从空间配置中获取（SaveCredentials 保存的凭证）。
func (s *modelService) resolveWeKnoraCloudCredentials(ctx context.Context, params *types.ModelParameters) (appID, appSecret string) {
	appID = params.AppID
	appSecret = s.decryptAppSecret(params.AppSecret)

	if params.Provider != providers.WeKnoraCloudID {
		return
	}
	if appID != "" && appSecret != "" {
		return
	}

	if s.tenantService == nil {
		return
	}
	creds := s.tenantService.GetWeKnoraCloudCredentials(ctx)
	if creds == nil {
		return
	}
	if appID == "" {
		appID = creds.AppID
	}
	if appSecret == "" {
		appSecret = creds.AppSecret
	}
	return
}

// CreateModel creates a new model in the repository
// For local models, it initiates an asynchronous download process
// Remote models are immediately set to active status
func (s *modelService) CreateModel(ctx context.Context, model *types.Model) error {
	logger.Infof(ctx, "Creating model: %s, type: %s, source: %s", model.Name, model.Type, model.Source)

	// Handle remote models (e.g., OpenAI, Azure)
	if model.Source == types.ModelSourceRemote {
		logger.Info(ctx, "Remote model detected, setting status to active")
		model.Status = types.ModelStatusActive

		logger.Info(ctx, "Saving remote model to repository")
		err := s.repo.Create(ctx, model)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_name": model.Name,
				"model_type": model.Type,
			})
			return err
		}

		logger.Infof(ctx, "Remote model created successfully: %s", model.ID)
		return nil
	}

	// Handle local models (e.g., Ollama)
	logger.Info(ctx, "Local model detected, setting status to downloading")
	model.Status = types.ModelStatusDownloading

	logger.Info(ctx, "Saving local model to repository")
	err := s.repo.Create(ctx, model)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_name": model.Name,
			"model_type": model.Type,
		})
		return err
	}

	s.downloadModelAsync(ctx, model)

	logger.Infof(ctx, "Model creation initiated successfully: %s", model.ID)
	return nil
}

// CopyModel clones a tenant-owned model. The stored name stays the upstream
// model identifier. displayName is the label chosen by the caller. Credentials
// are copied from the source row because list responses never include them.
// An active local model is already present, so the copy does not download it
// again. A local model that is still downloading or failed starts a pull.
func (s *modelService) CopyModel(ctx context.Context, sourceID, displayName string) (*types.Model, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, apperrors.NewBadRequestError("model ID cannot be empty")
	}
	displayName = strings.TrimSpace(utils.SanitizeForLog(displayName))
	if displayName == "" {
		return nil, apperrors.NewBadRequestError("display name is required")
	}
	if utf8.RuneCountInString(displayName) > types.ModelDisplayNameMaxLen {
		return nil, apperrors.NewBadRequestError("display name is too long")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	source, err := s.repo.GetByID(ctx, tenantID, sourceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": sourceID,
		})
		return nil, err
	}
	if source == nil {
		return nil, ErrModelNotFound
	}
	if source.IsBuiltin {
		return nil, apperrors.NewBadRequestError("builtin models cannot be copied")
	}

	params, err := cloneModelParameters(source.Parameters)
	if err != nil {
		return nil, err
	}
	model := &types.Model{
		TenantID:    tenantID,
		Name:        source.Name,
		DisplayName: displayName,
		Type:        source.Type,
		Source:      source.Source,
		Description: source.Description,
		Parameters:  params,
	}
	logger.Infof(ctx, "Copying model %s as %q", source.ID, displayName)

	// Remote rows, and local rows that already finished downloading, are
	// configuration copies. Only a local row that is not active needs a pull.
	if model.Source != types.ModelSourceLocal || source.Status == types.ModelStatusActive {
		model.Status = types.ModelStatusActive
		if err := s.repo.Create(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_id":   source.ID,
				"model_name": model.Name,
			})
			return nil, err
		}
		return model, nil
	}

	model.Status = types.ModelStatusDownloading
	if err := s.repo.Create(ctx, model); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   source.ID,
			"model_name": model.Name,
		})
		return nil, err
	}
	s.downloadModelAsync(ctx, model)
	return model, nil
}

func (s *modelService) downloadModelAsync(ctx context.Context, model *types.Model) {
	logger.Infof(ctx, "Starting background download for model: %s", model.Name)
	newCtx := logger.CloneContext(ctx)
	go func() {
		logger.Info(newCtx, "Background download started")
		err := s.ollamaService.PullModel(newCtx, model.Name)
		if err != nil {
			logger.ErrorWithFields(newCtx, err, map[string]interface{}{
				"model_name": model.Name,
			})
			model.Status = types.ModelStatusDownloadFailed
		} else {
			logger.Infof(newCtx, "Model download completed successfully: %s", model.Name)
			model.Status = types.ModelStatusActive
		}
		logger.Infof(newCtx, "Updating model status to: %s", model.Status)
		s.repo.Update(newCtx, model)
	}()
}

func cloneModelParameters(params types.ModelParameters) (types.ModelParameters, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return types.ModelParameters{}, err
	}
	var cloned types.ModelParameters
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return types.ModelParameters{}, err
	}
	return cloned, nil
}

// GetModelByID retrieves a model by its ID
// Returns an error if the model is not found or is in a non-active state
func (s *modelService) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	// Check if ID is empty
	if id == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Fetch model from repository
	model, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  id,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	// Check if model exists
	if model == nil {
		logger.Error(ctx, "Model not found")
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Model found, name: %s, status: %s", model.Name, model.Status)

	// Check model status
	if model.Status == types.ModelStatusActive {
		return model, nil
	}

	if model.Status == types.ModelStatusDownloading {
		logger.Warn(ctx, "Model is currently downloading")
		return nil, errors.New("model is currently downloading")
	}

	if model.Status == types.ModelStatusDownloadFailed {
		logger.Error(ctx, "Model download failed")
		return nil, errors.New("model download failed")
	}

	logger.Error(ctx, "Model status is abnormal")
	return nil, errors.New("abnormal model status")
}

// ListModels returns execution choices: user-available models for interactive
// calls, or configured business defaults for tenant-owned background work.
func (s *modelService) ListModels(ctx context.Context) ([]*types.Model, error) {
	logger.Info(ctx, "Start listing models")
	if _, scopedKey := types.TenantAPIKeyScopeFromContext(ctx); s.catalog != nil && !scopedKey && types.CallerFromContext(ctx).UserID != "" {
		rows, err := s.catalog.ListAvailableModels(ctx)
		if err != nil {
			return nil, err
		}
		models := make([]*types.Model, 0, len(rows))
		for _, row := range rows {
			models = append(models, row.Model)
		}
		return models, nil
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Listing models for tenant ID: %d", tenantID)

	// List models from repository with no additional filters
	models, err := s.repo.List(ctx, tenantID, "", "")
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}
	// GetModelByID is left alone: its result is written back by UpdateModel,
	// and this overlay must never be persisted.
	if settings := s.workspaceDefaultSettings(ctx, tenantID); settings != nil {
		if s.catalog != nil {
			seen := make(map[string]bool, len(models))
			for _, model := range models {
				seen[model.ID] = true
			}
			ids := append([]string{settings.UtilityModelID}, modelSlotIDs(settings)...)
			for _, id := range ids {
				if id == "" || seen[id] {
					continue
				}
				model, err := s.GetUsableModel(ctx, id)
				if err != nil {
					return nil, err
				}
				models = append(models, model)
				seen[id] = true
			}
		}
		settings.ApplyModelDefaults(models)
	}

	logger.Infof(ctx, "Retrieved %d models successfully", len(models))
	return models, nil
}

// workspaceDefaultSettings prefers the tenant the request already loaded and
// reads it from storage for background contexts that carry only the ID.
func (s *modelService) workspaceDefaultSettings(ctx context.Context, tenantID uint64) *types.DefaultSettings {
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant.ID == tenantID {
		return tenant.DefaultSettings
	}
	if s.tenantService == nil {
		return nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	return tenant.DefaultSettings
}

// UpdateModel updates an existing model in the repository
func (s *modelService) UpdateModel(ctx context.Context, model *types.Model) error {
	logger.Info(ctx, "Start updating model")
	logger.Infof(ctx, "Updating model ID: %s, name: %s", model.ID, model.Name)

	// Built-in models are platform-wide. Tenant administrators may view them,
	// but only a system administrator may change their shared configuration.
	tenantID := types.MustTenantIDFromContext(ctx)
	existingModel, err := s.repo.GetByID(ctx, tenantID, model.ID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": model.ID,
		})
		return err
	}
	if existingModel != nil && existingModel.IsBuiltin {
		if !types.IsSystemAdminFromContext(ctx) {
			logger.Warnf(ctx, "Non-system-admin attempted to update builtin model: %s", model.ID)
			return apperrors.NewForbiddenError("only system administrators can update builtin models")
		}
		// A UI edit is an explicit runtime override. Clear YAML ownership so
		// the startup reconciler does not silently replace the saved values.
		model.TenantID = existingModel.TenantID
		model.IsBuiltin = true
		model.ManagedBy = ""
	}

	// Update model in repository
	err = s.repo.Update(ctx, model)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return err
	}

	logger.Infof(ctx, "Model updated successfully: %s", model.ID)
	return nil
}

// UpdateModelCredentials writes one or more credential fields on the model's
// Parameters jsonb. Models are not pooled per-instance the way MCP clients
// are (each call to GetEmbeddingModel/GetChatModel rebuilds the client from
// the current Parameters), so no explicit cache invalidation is required —
// the next call will pick up the new credential automatically.
func (s *modelService) UpdateModelCredentials(
	ctx context.Context, id string, apiKey, appSecret *string,
) (*types.Model, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	existing, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrModelNotFound
	}
	if existing.IsBuiltin && !types.IsSystemAdminFromContext(ctx) {
		return nil, apperrors.NewForbiddenError(
			"only system administrators can modify builtin model credentials")
	}

	changed := false
	if apiKey != nil && *apiKey != "" && *apiKey != existing.Parameters.APIKey {
		existing.Parameters.APIKey = *apiKey
		changed = true
	}
	if appSecret != nil && *appSecret != "" && *appSecret != existing.Parameters.AppSecret {
		existing.Parameters.AppSecret = *appSecret
		changed = true
	}
	if !changed {
		return existing, nil
	}
	if existing.IsBuiltin {
		// Credential changes are also runtime overrides of YAML-managed data.
		existing.ManagedBy = ""
	}
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Model credentials updated: id=%s", id)
	return existing, nil
}

// ClearModelCredential removes a single credential field. Idempotent.
func (s *modelService) ClearModelCredential(ctx context.Context, id, field string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	existing, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrModelNotFound
	}
	if existing.IsBuiltin && !types.IsSystemAdminFromContext(ctx) {
		return apperrors.NewForbiddenError(
			"only system administrators can modify builtin model credentials")
	}

	changed := false
	switch field {
	case "api_key":
		if existing.Parameters.APIKey != "" {
			existing.Parameters.APIKey = ""
			changed = true
		}
	case "app_secret":
		if existing.Parameters.AppSecret != "" {
			existing.Parameters.AppSecret = ""
			changed = true
		}
	default:
		return errors.New("unknown credential field: " + field)
	}
	if !changed {
		return nil
	}
	if existing.IsBuiltin {
		existing.ManagedBy = ""
	}
	if err := s.repo.Update(ctx, existing); err != nil {
		return err
	}
	logger.Infof(ctx, "Model credential cleared by user: id=%s field=%s", id, field)
	return nil
}

// DeleteModel removes a model from the repository
func (s *modelService) DeleteModel(ctx context.Context, id string) error {
	logger.Info(ctx, "Start deleting model")
	logger.Infof(ctx, "Deleting model ID: %s", id)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	// Check if the model is builtin - builtin models cannot be deleted
	existingModel, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": id,
		})
		return err
	}
	if existingModel == nil {
		return ErrModelNotFound
	}
	if existingModel.IsBuiltin {
		logger.Warnf(ctx, "Attempted to delete builtin model: %s", id)
		return apperrors.NewBadRequestError("builtin models cannot be deleted")
	}

	counts := types.ModelReferenceCounts{}
	if s.catalog != nil {
		var err error
		counts, err = s.catalog.CountReferences(ctx, id)
		if err != nil {
			return err
		}
	}
	usage, err := s.getModelUsageDetails(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": id,
		})
		return err
	}
	usage.KnowledgeBaseTotal = max(usage.KnowledgeBaseTotal, counts.KnowledgeBases)
	usage.AgentTotal = max(usage.AgentTotal, counts.Agents)
	if usage.InUse() || counts.InUse() {
		kbCount := usage.KnowledgeBaseTotal
		if kbCount == 0 {
			kbCount = int64(len(usage.KnowledgeBases))
		}
		agentCount := usage.AgentTotal
		if agentCount == 0 {
			agentCount = int64(len(usage.Agents))
		}
		memoryInUse := len(usage.LongTermMemory.Bindings) > 0
		logger.Warnf(ctx, "Model %s is in use: kb=%d agent=%d memory=%t", id, kbCount, agentCount, memoryInUse)
		message := formatModelInUseMessage(kbCount, agentCount, memoryInUse)
		if counts.TenantSettings > 0 {
			message = "模型正在被业务配置使用，请先调整知识库、应用或业务默认设置中的引用"
		}
		return apperrors.NewModelInUseError(
			message,
			usage,
		)
	}

	// Delete model from repository
	err = s.repo.Delete(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  id,
			"tenant_id": tenantID,
		})
		return err
	}

	logger.Infof(ctx, "Model deleted successfully: %s", id)
	return nil
}

func (s *modelService) getModelUsageDetails(
	ctx context.Context, tenantID uint64, modelID string,
) (types.ModelUsageDetails, error) {
	details := types.ModelUsageDetails{
		KnowledgeBases: make([]types.ModelUsageResource, 0),
		Agents:         make([]types.ModelUsageResource, 0),
		LongTermMemory: types.ModelUsageMemory{Bindings: make([]types.ModelUsageBinding, 0)},
	}

	kbCount, err := s.kbRepo.CountByModelID(ctx, tenantID, modelID)
	if err != nil {
		return details, err
	}
	details.KnowledgeBaseTotal = kbCount
	if kbCount > 0 {
		details.KnowledgeBases, err = s.kbRepo.ListModelUsages(ctx, tenantID, modelID)
		if err != nil {
			return details, err
		}
		if details.KnowledgeBases == nil {
			details.KnowledgeBases = make([]types.ModelUsageResource, 0)
		}
	}

	agentCount, err := s.agentRepo.CountByModelID(ctx, tenantID, modelID)
	if err != nil {
		return details, err
	}
	details.AgentTotal = agentCount
	if agentCount > 0 {
		details.Agents, err = s.agentRepo.ListModelUsages(ctx, tenantID, modelID)
		if err != nil {
			return details, err
		}
		if details.Agents == nil {
			details.Agents = make([]types.ModelUsageResource, 0)
		}
	}

	if s.tenantService == nil {
		return details, nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return details, err
	}
	if tenant == nil || tenant.MemoryConfig == nil {
		return details, nil
	}

	// Both memory model pins have to be checked. Deleting either one leaves
	// the workspace pointing at a model that no longer exists.
	if strings.TrimSpace(tenant.MemoryConfig.EmbeddingModelID) == modelID {
		details.LongTermMemory.Bindings = append(
			details.LongTermMemory.Bindings,
			types.ModelUsageBindingEmbeddingModel,
		)
	}
	if strings.TrimSpace(tenant.MemoryConfig.ExtractModelID) == modelID {
		details.LongTermMemory.Bindings = append(
			details.LongTermMemory.Bindings,
			types.ModelUsageBindingExtractModel,
		)
	}
	return details, nil
}

// GetEmbeddingModel retrieves and initializes an embedding model instance
// Takes a model ID and returns an Embedder interface implementation
func (s *modelService) GetEmbeddingModel(ctx context.Context, modelId string) (embedding.Embedder, error) {
	// Get the model details
	model, err := s.GetUsableModel(ctx, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelId,
		})
		return nil, err
	}

	logger.Infof(ctx, "Getting embedding model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(modelCredentialsContext(ctx, model), &model.Parameters)

	embedder, err := embedding.NewEmbedder(embedding.ConfigFromModel(model, appID, appSecret), s.pooler, s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	logger.Info(ctx, "Embedding model initialized successfully")
	return embedder, nil
}

// GetEmbeddingModelForTenant retrieves and initializes an embedding model for a specific tenant
// This is used for cross-tenant knowledge base sharing where the embedding model from
// the source tenant must be used to ensure vector compatibility
func (s *modelService) GetEmbeddingModelForTenant(ctx context.Context, modelId string, tenantID uint64) (embedding.Embedder, error) {
	// Check if model ID is empty
	if modelId == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, errors.New("model ID cannot be empty")
	}

	// Fetch model from repository using the specified tenant ID
	model, err := s.GetUsableModel(types.WithExecutionTenant(ctx, tenantID), modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		logger.Error(ctx, "Model not found for specified tenant")
		return nil, ErrModelNotFound
	}

	if model.Status != types.ModelStatusActive {
		logger.Errorf(ctx, "Model is not active, status: %s", model.Status)
		return nil, errors.New("model is not active")
	}

	logger.Infof(ctx, "Getting cross-tenant embedding model: %s, source: %s, tenant: %d", model.Name, model.Source, tenantID)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(modelCredentialsContext(types.WithExecutionTenant(ctx, tenantID), model), &model.Parameters)

	embedder, err := embedding.NewEmbedder(embedding.ConfigFromModel(model, appID, appSecret), s.pooler, s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
			"tenant_id":  tenantID,
		})
		return nil, err
	}

	logger.Info(ctx, "Cross-tenant embedding model initialized successfully")
	return embedder, nil
}

// GetRerankModel retrieves and initializes a reranking model instance
// Takes a model ID and returns a Reranker interface implementation
func (s *modelService) GetRerankModel(ctx context.Context, modelId string) (rerank.Reranker, error) {
	// Get the model details
	model, err := s.GetUsableModel(ctx, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelId,
		})
		return nil, err
	}

	logger.Infof(ctx, "Getting rerank model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(modelCredentialsContext(ctx, model), &model.Parameters)

	reranker, err := rerank.NewReranker(rerank.ConfigFromModel(model, appID, appSecret))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	logger.Info(ctx, "Rerank model initialized successfully")
	return reranker, nil
}

// GetChatModel retrieves and initializes a chat model instance
// Takes a model ID and returns a Chat interface implementation
func (s *modelService) GetChatModel(ctx context.Context, modelId string) (chat.Chat, error) {
	_, instance, err := s.GetChatModelSnapshot(ctx, modelId)
	return instance, err
}

// GetChatModelSnapshot binds model metadata and its runtime to one repository read.
func (s *modelService) GetChatModelSnapshot(ctx context.Context, modelId string) (*types.Model, chat.Chat, error) {
	// Check if model ID is empty
	if modelId == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Resolve the current user or authorized resource binding before snapshotting.
	model, err := s.GetUsableModel(types.WithExecutionTenant(ctx, tenantID), modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, nil, err
	}

	if model == nil {
		logger.Error(ctx, "Chat model not found")
		return nil, nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting chat model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(modelCredentialsContext(ctx, model), &model.Parameters)

	chatModel, err := chat.NewChat(chat.ConfigFromModel(model, appID, appSecret), s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return model, nil, err
	}

	return model, chatModel, nil
}

// GetVLMModel retrieves and initializes a vision language model instance.
func (s *modelService) GetVLMModel(ctx context.Context, modelId string) (vlm.VLM, error) {
	if modelId == "" {
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	model, err := s.GetUsableModel(types.WithExecutionTenant(ctx, tenantID), modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting VLM model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(modelCredentialsContext(ctx, model), &model.Parameters)

	vlmModel, err := vlm.NewVLM(vlm.ConfigFromModel(model, appID, appSecret), s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	return vlmModel, nil
}

// GetASRModel retrieves and initializes an automatic speech recognition model instance.
func (s *modelService) GetASRModel(ctx context.Context, modelId string) (asr.ASR, error) {
	if modelId == "" {
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	model, err := s.GetUsableModel(types.WithExecutionTenant(ctx, tenantID), modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting ASR model: %s, source: %s", model.Name, model.Source)

	sttModel, err := asr.NewASR(asr.ConfigFromModel(model))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	return sttModel, nil
}

func formatModelInUseMessage(kbCount, agentCount int64, memory bool) string {
	var parts []string
	if kbCount > 0 {
		parts = append(parts, fmt.Sprintf("%d knowledge base(s)", kbCount))
	}
	if agentCount > 0 {
		parts = append(parts, fmt.Sprintf("%d agent(s)", agentCount))
	}
	if memory {
		parts = append(parts, "long-term memory")
	}
	joined := strings.Join(parts, " and ")
	return fmt.Sprintf(
		"model is used by %s; reconfigure or remove those references before deleting",
		joined,
	)
}

// GetUsableModel resolves execution and selection rights independently of CRUD.
// A resource grant covers only its persisted model IDs, never the owner's catalog.
func (s *modelService) GetUsableModel(ctx context.Context, id string) (*types.Model, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrModelNotFound
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	model, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	caller := types.CallerFromContext(ctx)
	scopeChanged := caller.TenantID != 0 && caller.TenantID != tenantID
	if model != nil && !model.IsBuiltin && scopeChanged && !types.HasModelBinding(ctx, id) {
		model = nil
	}
	if model == nil && s.catalog != nil {
		if types.HasModelBinding(ctx, id) {
			model, err = s.catalog.GetBoundModel(ctx, id)
		} else {
			bound, checkErr := false, error(nil)
			if !scopeChanged {
				bound, checkErr = s.isBusinessModelBinding(ctx, id)
			}
			if checkErr != nil {
				return nil, checkErr
			}
			if bound {
				model, err = s.catalog.GetBoundModel(types.WithModelBindings(ctx, id), id)
			} else if types.CallerFromContext(ctx).UserID != "" {
				model, err = s.catalog.GetAvailableModel(ctx, id)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if model == nil {
		return nil, ErrModelNotFound
	}
	if model.Status != types.ModelStatusActive {
		return nil, apperrors.NewBadRequestError("模型不可用")
	}
	return model, nil
}

func modelSlotIDs(settings *types.DefaultSettings) []string {
	ids := make([]string, 0)
	for _, id := range settings.ModelSlots() {
		ids = append(ids, id)
	}
	return ids
}

func (s *modelService) isBusinessModelBinding(ctx context.Context, id string) (bool, error) {
	if s.tenantService == nil {
		return false, nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, types.MustTenantIDFromContext(ctx))
	if err != nil {
		return false, err
	}
	if tenant == nil {
		return false, nil
	}
	if tenant.DefaultSettings != nil {
		for _, modelID := range append([]string{tenant.DefaultSettings.UtilityModelID}, modelSlotIDs(tenant.DefaultSettings)...) {
			if id == modelID {
				return true, nil
			}
		}
	}
	if tenant.MemoryConfig != nil && (id == tenant.MemoryConfig.EmbeddingModelID || id == tenant.MemoryConfig.ExtractModelID) {
		return true, nil
	}
	if tenant.RetrievalConfig != nil && id == tenant.RetrievalConfig.RerankModelID {
		return true, nil
	}
	if tenant.ChatHistoryConfig != nil && id == tenant.ChatHistoryConfig.EmbeddingModelID {
		return true, nil
	}
	return false, nil
}

// Shared builtins use the business invocation credentials; privately configured
// models retain their source credentials when selected by another application.
func modelCredentialsContext(ctx context.Context, model *types.Model) context.Context {
	if model.IsBuiltin {
		return ctx
	}
	return types.WithExecutionTenant(ctx, model.TenantID)
}
