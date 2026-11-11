package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type harnessModelService struct {
	interfaces.ModelService
	model *types.Model
	chat  chat.Chat
	seen  uint64
}

func (s *harnessModelService) GetChatModelSnapshot(ctx context.Context, id string) (*types.Model, chat.Chat, error) {
	s.seen = types.MustTenantIDFromContext(ctx)
	if id != s.model.ID || s.seen != s.model.TenantID {
		return nil, nil, fmt.Errorf("not found")
	}
	return s.model, s.chat, nil
}

type harnessChat struct {
	called   bool
	messages []api.Message
	options  *api.Options
	chunks   []types.StreamResponse
}

func (s *harnessChat) Chat(context.Context, []api.Message, *api.Options) (*types.ChatResponse, error) {
	return nil, fmt.Errorf("streaming test only")
}

func (s *harnessChat) GetModelName() string { return "test-model" }
func (s *harnessChat) GetModelID() string   { return "model1" }

func (s *harnessChat) ChatStream(_ context.Context, messages []api.Message, options *api.Options) (<-chan types.StreamResponse, error) {
	s.called = true
	s.messages = messages
	s.options = options
	ch := make(chan types.StreamResponse, len(s.chunks))
	for _, chunk := range s.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func harnessRequest(t *testing.T, svc *harnessModelService, tenant uint64, revision time.Time) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenant))
	})
	router.POST("/models/:id/harness-chat", NewModelHandler(svc).ChatHarnessModel)
	body, err := json.Marshal(HarnessModelRequest{
		ModelRevision: revision,
		Messages: []api.Message{
			{Role: "assistant", ReasoningContent: "reason", ReasoningSignature: "signed", ToolCalls: []api.ToolCall{{ID: "call1", Type: "function", Function: api.FunctionCall{Name: "read", Arguments: `{"path":"report.txt"}`}}}},
			{Role: "tool", ToolCallID: "call1", Content: "document text"},
		},
		Options: api.Options{MaxTokens: 100, Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/models/model1/harness-chat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHarnessModelUsesWorkspaceSnapshotAndKeepsToolHistory(t *testing.T) {
	revision := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	instance := &harnessChat{chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeThinking, Content: "think"},
		{ResponseType: types.ResponseTypeAnswer, Content: "answer"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop", Usage: &types.TokenUsage{TotalTokens: 10}, Data: map[string]interface{}{"reasoning_signature": "signed"}},
	}}
	svc := &harnessModelService{model: &types.Model{ID: "model1", TenantID: 10, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, UpdatedAt: revision, Parameters: types.ModelParameters{APIKey: "private-model-key"}}, chat: instance}
	rec := harnessRequest(t, svc, 10, revision)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, instance.called)
	assert.Equal(t, "call1", instance.messages[1].ToolCallID)
	assert.Equal(t, "signed", instance.messages[0].ReasoningSignature)
	assert.Equal(t, "read", instance.options.Tools[0].Function.Name)
	assert.Contains(t, rec.Body.String(), `"reasoning_signature":"signed"`)
	assert.Contains(t, rec.Body.String(), "event: end")
	assert.NotContains(t, rec.Body.String(), "private-model-key")

	instance.called = false
	rec = harnessRequest(t, svc, 11, revision)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.False(t, instance.called, "another workspace cannot invoke the model")

	rec = harnessRequest(t, svc, 10, revision.Add(-time.Second))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.False(t, instance.called, "stale capabilities must not be combined with new credentials")
}

func TestHarnessModelDoesNotExposeUpstreamFailure(t *testing.T) {
	revision := time.Now().UTC()
	instance := &harnessChat{chunks: []types.StreamResponse{{ResponseType: types.ResponseTypeError, Content: "https://provider/?key=private-key", Data: map[string]interface{}{"credential": "private-key"}, Done: true}}}
	svc := &harnessModelService{model: &types.Model{ID: "model1", TenantID: 10, Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, UpdatedAt: revision}, chat: instance}
	rec := harnessRequest(t, svc, 10, revision)
	assert.Contains(t, rec.Body.String(), "model_request_failed")
	assert.NotContains(t, rec.Body.String(), "private-key")
}

func TestHarnessModelRejectsNonChatConfiguration(t *testing.T) {
	revision := time.Now().UTC()
	instance := &harnessChat{}
	svc := &harnessModelService{model: &types.Model{ID: "model1", TenantID: 10, Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, UpdatedAt: revision}, chat: instance}
	rec := harnessRequest(t, svc, 10, revision)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, instance.called)
}

// TestHarnessModelLiveStream exercises the handler with an explicitly configured
// real provider. It has no database, storage or production-session side effects.
func TestHarnessModelLiveStream(t *testing.T) {
	endpoint, name := os.Getenv("HARNESS_MODEL_TEST_URL"), os.Getenv("HARNESS_MODEL_TEST_NAME")
	if endpoint == "" || name == "" {
		t.Skip("set HARNESS_MODEL_TEST_URL and HARNESS_MODEL_TEST_NAME for real inference")
	}
	model := &types.Model{ID: "model1", TenantID: 10, Name: name, Source: types.ModelSourceRemote,
		Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, UpdatedAt: time.Now().UTC(),
		Parameters: types.ModelParameters{Provider: "generic", BaseURL: endpoint, APIKey: os.Getenv("HARNESS_MODEL_TEST_KEY")}}
	instance, err := chat.NewChat(chat.ConfigFromModel(model, "", ""), nil)
	require.NoError(t, err)
	svc := &harnessModelService{model: model, chat: instance}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(10)))
	})
	router.POST("/models/:id/harness-chat", NewModelHandler(svc).ChatHarnessModel)
	body, err := json.Marshal(HarnessModelRequest{ModelRevision: model.UpdatedAt,
		Messages: []api.Message{{Role: "user", Content: "Reply exactly MODEL_LIVE_OK, without explanation."}},
		Options:  api.Options{MaxTokens: 64, ReasoningEffort: api.ReasoningOff}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/models/model1/harness-chat", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(req.Context(), 90*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	var answer strings.Builder
	finished := false
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") || scanner.Text() == "data: {}" {
			continue
		}
		var chunk types.StreamResponse
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &chunk))
		require.NotEqual(t, types.ResponseTypeError, chunk.ResponseType)
		if chunk.ResponseType == types.ResponseTypeAnswer {
			answer.WriteString(chunk.Content)
			finished = finished || (chunk.Done && chunk.FinishReason == "stop")
		}
	}
	require.NoError(t, scanner.Err())
	assert.True(t, finished)
	assert.Contains(t, answer.String(), "MODEL_LIVE_OK")
	assert.Contains(t, rec.Body.String(), "event: end")
}
