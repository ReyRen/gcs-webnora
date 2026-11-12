package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var resourceModelColumns = map[string][]string{
	"KnowledgeBase": {"embedding_model_id", "summary_model_id", "image_processing_config", "vlm_config", "asr_config", "wiki_config", "auto_tag_config", "question_generation_config", "profile_config"},
	"CustomAgent":   {"config"},
	"Tenant":        {"default_settings", "memory_config", "retrieval_config", "chat_history_config"},
}

// InstallModelReferenceValidation checks newly selected models at persistence,
// including disabled configuration fields and generic JSON settings updates.
// Existing references stay usable; private resource grants cannot authorize a
// new reference in another resource. Trusted identity-free maintenance is not
// an interactive selection path. Wire and scoped-key writes retain their caller.
func InstallModelReferenceValidation(db *gorm.DB, cfg *config.Config) error {
	allowGlobal := cfg.Tenant != nil && cfg.Tenant.EnableCrossTenantAccess
	check := func(create bool) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			if tx.Error != nil || tx.Statement.Schema == nil {
				return
			}
			name := tx.Statement.Schema.Name
			columns, relevant := resourceModelColumns[name]
			if !relevant {
				return
			}
			caller := types.CallerFromContext(tx.Statement.Context)
			_, key := types.TenantAPIKeyScopeFromContext(tx.Statement.Context)
			if caller.UserID == "" && !key {
				return
			}
			if err := validateResourceModelWrite(tx, name, columns, create, allowGlobal); err != nil {
				tx.AddError(err)
			}
		}
	}
	if err := db.Callback().Create().Before("gorm:create").Register("weknora:model-reference-access", check(true)); err != nil {
		return err
	}
	return db.Callback().Update().Before("gorm:update").Register("weknora:model-reference-access", check(false))
}

func resourceModelIDs(name string, raw []byte) (string, []string, error) {
	switch name {
	case "KnowledgeBase":
		var value types.KnowledgeBase
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", nil, err
		}
		return value.ID, value.ModelIDs(), nil
	case "CustomAgent":
		var value types.CustomAgent
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", nil, err
		}
		return value.ID, value.ModelIDs(), nil
	case "Tenant":
		var value types.Tenant
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", nil, err
		}
		id := ""
		if value.ID != 0 {
			id = fmt.Sprint(value.ID)
		}
		return id, value.ModelIDs(), nil
	}
	return "", nil, fmt.Errorf("unsupported model reference resource")
}

func resourceModelRows(query *gorm.DB, name string) ([]json.RawMessage, error) {
	var value interface{}
	switch name {
	case "KnowledgeBase":
		value = &[]types.KnowledgeBase{}
	case "CustomAgent":
		value = &[]types.CustomAgent{}
	case "Tenant":
		value = &[]types.Tenant{}
	}
	if err := query.Find(value).Error; err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	err = json.Unmarshal(raw, &rows)
	return rows, err
}

func validateResourceModelWrite(tx *gorm.DB, name string, columns []string, create, allowGlobal bool) error {
	// SQL expressions cannot introduce model IDs outside this validation path.
	if values, ok := tx.Statement.Dest.(map[string]interface{}); ok {
		relevant := false
		for key, value := range values {
			field := tx.Statement.Schema.LookUpField(key)
			if field == nil {
				continue
			}
			for _, column := range columns {
				if field.DBName == column {
					relevant = true
					if _, expression := value.(clause.Expression); expression {
						return apperrors.NewBadRequestError("模型配置必须使用明确的模型引用")
					}
				}
			}
		}
		if !relevant {
			return nil
		}
	}
	raw, err := json.Marshal(tx.Statement.Dest)
	if err != nil {
		return err
	}
	if create {
		rows := []json.RawMessage{raw}
		if len(raw) > 0 && raw[0] == '[' {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return err
			}
		}
		for _, row := range rows {
			_, ids, err := resourceModelIDs(name, row)
			if err != nil {
				return err
			}
			if err := authorizeNewModelReferences(tx, nil, ids, allowGlobal); err != nil {
				return err
			}
		}
		return nil
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return err
	}
	selected, restricted := tx.Statement.SelectAndOmitColumns(false, true)
	filtered := make(map[string]json.RawMessage)
	for key, value := range patch {
		field := tx.Statement.Schema.LookUpField(key)
		if field == nil {
			continue
		}
		if chosen, ok := selected[field.DBName]; ok && !chosen || restricted && !selected[field.DBName] {
			continue
		}
		for _, column := range columns {
			if column != field.DBName {
				continue
			}
			if len(value) > 0 && value[0] == '"' {
				var encoded string
				if err := json.Unmarshal(value, &encoded); err != nil {
					return err
				}
				if json.Valid([]byte(encoded)) {
					value = json.RawMessage(encoded)
				}
			}
			filtered[column] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	query := tx.Session(&gorm.Session{NewDB: true, Context: tx.Statement.Context})
	if tx.Statement.Unscoped {
		query = query.Unscoped()
	}
	where, hasWhere := tx.Statement.Clauses["WHERE"]
	if hasWhere {
		query = query.Where(where.Expression)
	}
	id, _, err := resourceModelIDs(name, raw)
	if err != nil {
		return err
	}
	if id == "" {
		model, err := json.Marshal(tx.Statement.Model)
		if err != nil {
			return err
		}
		id, _, err = resourceModelIDs(name, model)
		if err != nil {
			return err
		}
	}
	if id != "" {
		query = query.Where("id = ?", id)
	} else if !hasWhere {
		return gorm.ErrMissingWhereClause
	}
	rows, err := resourceModelRows(query.Select(append([]string{"id"}, columns...)), name)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		rows = []json.RawMessage{json.RawMessage("{}")}
	}
	for _, row := range rows {
		_, previous, err := resourceModelIDs(name, row)
		if err != nil {
			return err
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(row, &next); err != nil {
			return err
		}
		for key, value := range filtered {
			next[key] = value
		}
		merged, err := json.Marshal(next)
		if err != nil {
			return err
		}
		_, ids, err := resourceModelIDs(name, merged)
		if err != nil {
			return err
		}
		if err := authorizeNewModelReferences(tx, previous, ids, allowGlobal); err != nil {
			return err
		}
	}
	return nil
}

func authorizeNewModelReferences(tx *gorm.DB, previous, next []string, allowGlobal bool) error {
	seen := make(map[string]bool)
	for _, id := range previous {
		seen[id] = true
	}
	var ids []string
	for _, id := range next {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	denied := func() error { return apperrors.NewForbiddenError("所选模型不在当前用户可用目录中") }
	db := tx.Session(&gorm.Session{NewDB: true, Context: tx.Statement.Context})
	var models []types.Model
	if err := db.Select("id", "tenant_id", "is_builtin", "status").Where("id IN ?", ids).Find(&models).Error; err != nil {
		return err
	}
	if len(models) != len(ids) {
		return denied()
	}
	caller := types.CallerFromContext(tx.Statement.Context)
	_, scopedKey := types.TenantAPIKeyScopeFromContext(tx.Statement.Context)
	var user types.User
	var memberships []types.TenantMember
	if !scopedKey {
		if err := db.Select("id", "is_active", "can_access_all_tenants").First(&user, "id = ?", caller.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return denied()
			}
			return err
		}
		if !user.IsActive {
			return denied()
		}
		if err := db.Where("user_id = ? AND status = ?", caller.UserID, types.TenantMemberStatusActive).Find(&memberships).Error; err != nil {
			return err
		}
	}
	for _, model := range models {
		allowed := model.IsBuiltin || scopedKey && model.TenantID == caller.TenantID || !scopedKey && user.CanAccessAllTenants && allowGlobal
		for _, member := range memberships {
			if member.TenantID == model.TenantID && member.Role.IsValid() {
				allowed = true
			}
		}
		if !allowed || model.Status != types.ModelStatusActive || model.ID != strings.TrimSpace(model.ID) {
			return denied()
		}
	}
	return nil
}
