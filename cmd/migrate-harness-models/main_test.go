package main

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestImportIdentityAndReuse(t *testing.T) {
	e := entry{UserID: "369", Route: "ds-utcs", Model: "same-name", Parameters: types.ModelParameters{APIKey: "private", BaseURL: "http://model/v1"}}
	row := types.Model{Name: e.Model, Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive, Parameters: e.Parameters}
	assert.True(t, reusable(row, e))
	row.Parameters.APIKey = "different-private-key"
	assert.False(t, reusable(row, e), "same name cannot merge different credentials")
	row.Parameters = e.Parameters
	row.Parameters.ContextWindow = 8192
	assert.False(t, reusable(row, e), "preserve explicit capability differences")
	other := e
	other.UserID = "568"
	assert.NotEqual(t, stableID(e), stableID(other))
	other = e
	other.Route = "another-route"
	assert.NotEqual(t, stableID(e), stableID(other))
	assert.Len(t, stableID(e), 40)
}
