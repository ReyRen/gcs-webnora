package dto

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAvailableModelUsesSourcePermissionForRedaction(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantRoleContextKey, types.TenantRoleOwner)
	model := &types.Model{ID: "foreign-viewer", TenantID: 2, Parameters: types.ModelParameters{
		BaseURL: "https://private.test/v1", APIKey: "private-secret", CustomHeaders: map[string]string{"X-Private": "private-header"},
	}}
	row := NewAvailableModelResponse(ctx, types.AvailableModel{Model: model, CanManage: false})
	require.Empty(t, row.Parameters.BaseURL)
	require.Empty(t, row.Parameters.CustomHeaders)
	require.False(t, row.CanManage)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private-secret")
	require.NotContains(t, string(encoded), "private-header")
	row = NewAvailableModelResponse(ctx, types.AvailableModel{Model: model, CanManage: true})
	require.Equal(t, model.Parameters.BaseURL, row.Parameters.BaseURL)
	require.True(t, row.CanManage)
}
