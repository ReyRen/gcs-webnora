package types

import "context"

type modelBindingContextKey struct{}

// WithModelBindings permits model execution for exact persisted references of
// an authorized resource. Callers must authorize that resource before adding
// its model IDs. The grant does not change the caller or grant model management.
func WithModelBindings(ctx context.Context, modelIDs ...string) context.Context {
	ids := make(map[string]struct{}, len(modelIDs))
	for _, id := range modelIDs {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	return context.WithValue(ctx, modelBindingContextKey{}, ids)
}

// HasModelBinding reports whether an authorized resource grants execution of id.
func HasModelBinding(ctx context.Context, id string) bool {
	ids, _ := ctx.Value(modelBindingContextKey{}).(map[string]struct{})
	_, allowed := ids[id]
	return allowed
}

// WithKnowledgeBaseModels permits the persisted model references of a knowledge
// base already authorized for the current operation.
func WithKnowledgeBaseModels(ctx context.Context, kb *KnowledgeBase) context.Context {
	return WithModelBindings(WithExecutionTenant(ctx, kb.TenantID), kb.ModelIDs()...)
}

// ModelIDs lists persisted model references without changing index bindings.
func (kb *KnowledgeBase) ModelIDs() []string {
	ids := []string{kb.EmbeddingModelID, kb.SummaryModelID, kb.ImageProcessingConfig.ModelID,
		kb.VLMConfig.ModelID, kb.ASRConfig.ModelID}
	if kb.QuestionGenerationConfig != nil {
		ids = append(ids, kb.QuestionGenerationConfig.ModelID)
	}
	if kb.WikiConfig != nil {
		ids = append(ids, kb.WikiConfig.SynthesisModelID)
	}
	if kb.AutoTagConfig != nil {
		ids = append(ids, kb.AutoTagConfig.ModelID)
	}
	if kb.ProfileConfig != nil {
		ids = append(ids, kb.ProfileConfig.ModelID)
	}
	return ids
}

// WithAgentModels permits only the persisted configuration of an agent already
// authorized for this run. Request overrides still require user-directory access.
func WithAgentModels(ctx context.Context, agent *CustomAgent) context.Context {
	return WithModelBindings(ctx, agent.ModelIDs()...)
}

// ModelIDs lists the model references persisted in an agent configuration.
func (agent *CustomAgent) ModelIDs() []string {
	ids := []string{agent.Config.ModelID, agent.Config.RerankModelID, agent.Config.VLMModelID,
		agent.Config.ASRModelID, agent.Config.QueryUnderstandModelID}
	if agent.Config.QuestionSuggestions != nil {
		ids = append(ids, agent.Config.QuestionSuggestions.FollowUps.ModelID)
	}
	return ids
}

// ModelIDs lists the business model choices stored in tenant settings.
func (tenant *Tenant) ModelIDs() []string {
	var ids []string
	if tenant.DefaultSettings != nil {
		ids = append(ids, tenant.DefaultSettings.UtilityModelID)
		for _, id := range tenant.DefaultSettings.ModelSlots() {
			ids = append(ids, id)
		}
	}
	if tenant.MemoryConfig != nil {
		ids = append(ids, tenant.MemoryConfig.ExtractModelID, tenant.MemoryConfig.EmbeddingModelID)
	}
	if tenant.RetrievalConfig != nil {
		ids = append(ids, tenant.RetrievalConfig.RerankModelID)
	}
	if tenant.ChatHistoryConfig != nil {
		ids = append(ids, tenant.ChatHistoryConfig.EmbeddingModelID)
	}
	return ids
}
