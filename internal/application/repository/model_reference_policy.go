package repository

import (
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type modelReferencePolicy struct {
	ID            uint64
	PolicyVersion int
	VerifiedAt    *time.Time
}

func (modelReferencePolicy) TableName() string { return "user_model_reference_policy" }

// EnsureModelReferencePolicy audits legacy references before permitting resource
// model grants. Existing cross-owner references require explicit migration
// review; they are never silently treated as authorized user selections.
// After activation all interactive reference changes pass the write validator.
func EnsureModelReferencePolicy(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var policy modelReferencePolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&policy, "id = ?", 1).Error; err != nil {
			return fmt.Errorf("user model policy migration is required: %w", err)
		}
		if policy.PolicyVersion == 1 {
			return nil
		}
		if policy.PolicyVersion != 0 {
			return fmt.Errorf("unsupported user model reference policy version")
		}
		if err := AuditLegacyModelReferences(tx); err != nil {
			return err
		}
		now := time.Now().UTC()
		policy.PolicyVersion = 1
		policy.VerifiedAt = &now
		return tx.Save(&policy).Error
	})
}

// AuditLegacyModelReferences reads all legacy resource references without
// changing models, credentials, memberships, files or policy activation.
func AuditLegacyModelReferences(tx *gorm.DB) error {
	var models []types.Model
	if err := tx.Select("id", "tenant_id", "is_builtin").Find(&models).Error; err != nil {
		return err
	}
	owners := make(map[string]uint64, len(models))
	for _, model := range models {
		if !model.IsBuiltin {
			owners[model.ID] = model.TenantID
		}
	}
	check := func(tenant uint64, ids []string) error {
		for _, id := range ids {
			if owner, exists := owners[id]; exists && owner != tenant {
				return fmt.Errorf("legacy cross-owner model reference requires migration review before enabling the user catalog")
			}
		}
		return nil
	}
	var kbs []types.KnowledgeBase
	if err := tx.Unscoped().Select(append([]string{"tenant_id"}, resourceModelColumns["KnowledgeBase"]...)).Find(&kbs).Error; err != nil {
		return err
	}
	for _, kb := range kbs {
		if err := check(kb.TenantID, kb.ModelIDs()); err != nil {
			return err
		}
	}
	var agents []types.CustomAgent
	if err := tx.Unscoped().Select("tenant_id", "config").Find(&agents).Error; err != nil {
		return err
	}
	for _, agent := range agents {
		if err := check(agent.TenantID, agent.ModelIDs()); err != nil {
			return err
		}
	}
	var tenants []types.Tenant
	if err := tx.Unscoped().Select(append([]string{"id"}, resourceModelColumns["Tenant"]...)).Find(&tenants).Error; err != nil {
		return err
	}
	for _, tenant := range tenants {
		if err := check(tenant.ID, tenant.ModelIDs()); err != nil {
			return err
		}
	}
	var documents []types.Knowledge
	if err := tx.Unscoped().Select("tenant_id", "embedding_model_id").Find(&documents).Error; err != nil {
		return err
	}
	for _, document := range documents {
		if err := check(document.TenantID, []string{document.EmbeddingModelID}); err != nil {
			return err
		}
	}
	return nil
}
