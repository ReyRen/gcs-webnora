package types

// AvailableModel carries a user-authorized model and its configuration rights.
// The model's tenant is an internal ownership reference, not a catalog partition.
type AvailableModel struct {
	Model     *Model `json:"-"`
	CanManage bool   `json:"-"`
}

// ModelReferenceCounts protects persisted application bindings across tenants.
type ModelReferenceCounts struct {
	KnowledgeBases int64
	Agents         int64
	TenantSettings int64
}

// InUse reports whether any application configuration references the model.
func (c ModelReferenceCounts) InUse() bool {
	return c.KnowledgeBases > 0 || c.Agents > 0 || c.TenantSettings > 0
}
