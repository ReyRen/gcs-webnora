package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// HarnessModelRequest carries a provider-neutral model call. Model identity,
// endpoint and credentials come exclusively from the authenticated workspace.
type HarnessModelRequest struct {
	Messages      []api.Message `json:"messages" binding:"required,min=1"`
	Options       api.Options   `json:"options"`
	ModelRevision time.Time     `json:"model_revision" binding:"required"`
}

// ChatHarnessModel godoc
// @Summary      调用统一配置的对话模型
// @Description  使用当前已授权空间的模型执行流式推理；不返回模型凭据。配置版本变更时拒绝过期请求。
// @Tags         模型管理
// @Accept       json
// @Produce      text/event-stream
// @Param        id       path      string               true  "模型 ID"
// @Param        request  body      HarnessModelRequest  true  "模型请求"
// @Success      200      {string}  string "StreamResponse SSE；末尾为 event: end"
// @Failure      400      {object} errors.AppError
// @Failure      404      {object} errors.AppError
// @Failure      409      {object} errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id}/harness-chat [post]
func (h *ModelHandler) ChatHarnessModel(c *gin.Context) {
	ctx := c.Request.Context()
	// This route accepts complete conversation histories, including inline images.
	// Bound the wire envelope before binding rather than buffering an unbounded body.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	var req HarnessModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid model request"))
		return
	}
	model, instance, err := h.service.GetChatModelSnapshot(ctx, c.Param("id"))
	if model == nil {
		_ = c.Error(errors.NewNotFoundError("Model not found"))
		return
	}
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model_id": model.ID})
		_ = c.Error(errors.NewBadRequestError("Model configuration is unavailable"))
		return
	}
	if model.Type != types.ModelTypeKnowledgeQA || model.Status != types.ModelStatusActive {
		_ = c.Error(errors.NewBadRequestError("Select an available chat model"))
		return
	}
	if !model.UpdatedAt.Equal(req.ModelRevision) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "model_configuration_changed"})
		return
	}
	for _, message := range req.Messages {
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			_ = c.Error(errors.NewBadRequestError("Invalid message role"))
			return
		}
	}
	if _, ok := api.ParseReasoningEffort(string(req.Options.ReasoningEffort)); !ok {
		_ = c.Error(errors.NewBadRequestError("Invalid reasoning effort"))
		return
	}
	stream, err := instance.ChatStream(ctx, req.Messages, &req.Options)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model_id": model.ID})
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": "model_request_failed"})
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-stream:
			if !ok {
				// The client separately requires a successful model finish reason.
				// An exhausted HTTP body alone must never settle a partial answer.
				_, _ = fmt.Fprint(c.Writer, "event: end\ndata: {}\n\n")
				c.Writer.Flush()
				return
			}
			if chunk.ResponseType == types.ResponseTypeError {
				// Upstream errors can contain URLs or credentials. Keep diagnostics
				// server-side and return only a stable error code to the caller.
				logger.Warnf(ctx, "Unified model stream failed for model %s", model.ID)
				chunk.Content = "model_request_failed"
				chunk.Data = nil
			}
			encoded, encodeErr := json.Marshal(chunk)
			if encodeErr != nil {
				logger.ErrorWithFields(ctx, encodeErr, map[string]interface{}{"model_id": model.ID})
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "event: chunk\ndata: %s\n\n", encoded); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
