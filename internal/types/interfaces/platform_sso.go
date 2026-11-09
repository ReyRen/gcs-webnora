package interfaces

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
)

// Optional capability of the SQL token repository. Existing non-SSO fakes and
// integrations do not need to implement it.
type PlatformSSORepository interface {
	GetPlatformSSOIdentity(context.Context, string, string) (*types.PlatformSSOIdentity, error)
	BindPlatformSSOIdentity(context.Context, *types.PlatformSSOIdentity) error
	CreatePlatformSSOSession(context.Context, *types.PlatformSSOSession) error
	// The callback executes under a database row lock, serializing rotating refreshes
	// across app replicas. Updates, including revocation, are committed on success.
	WithPlatformSSOSession(context.Context, string, func(*types.PlatformSSOSession) error) error
}
