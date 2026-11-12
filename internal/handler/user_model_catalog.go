package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListAvailableModels returns the authenticated user's complete usable directory.
// An optional authorized knowledge base contributes only its existing bindings,
// so locked selectors can display them without exposing the owner's directory.
func (h *ModelHandler) ListAvailableModels(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.catalog.ListAvailableModels(ctx)
	if err != nil { _ = c.Error(err); return }
	if id := c.Query("knowledge_base_id"); id != "" {
		kb, err := h.kbService.GetKnowledgeBaseByIDOnly(ctx, id)
		if err != nil { _ = c.Error(err); return }
		allowed, err := access.NewKBPermissions(ctx, h.kbShares).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
		if err != nil { _ = c.Error(err); return }
		if !allowed { _ = c.Error(apperrors.NewForbiddenError("无权访问该知识库")); return }
		seen := make(map[string]bool, len(rows))
		for _, row := range rows { seen[row.Model.ID] = true }
		boundCtx := types.WithKnowledgeBaseModels(ctx, kb)
		for _, modelID := range kb.ModelIDs() {
			if modelID == "" || seen[modelID] { continue }
			model, err := h.catalog.GetBoundModel(boundCtx, modelID)
			if err != nil { _ = c.Error(err); return }
			rows = append(rows, types.AvailableModel{Model: model})
			seen[modelID] = true
		}
	}
	data := make([]dto.AvailableModelResponse, 0, len(rows))
	for _, row := range rows { data = append(data, dto.NewAvailableModelResponse(ctx, row)) }
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
