package types

import "time"

// PlatformStorageBinding is immutable placement metadata, not an access grant.
// A workspace's owner may change without silently relocating existing files.
type PlatformStorageBinding struct {
	TenantID  uint64 `gorm:"primaryKey"`
	Provider  string
	Subject   string
	CreatedAt time.Time
}
