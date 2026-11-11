package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NewPlatformStorageUserResolver freezes placement from the workspace's actual
// owner membership and verified SSO identity. Request user/tenant claims cannot
// select the filesystem owner. Missing or ambiguous ownership fails closed.
func NewPlatformStorageUserResolver(db *gorm.DB, provider string) filesvc.StorageUserResolver {
	provider = strings.TrimRight(strings.TrimSpace(provider), "/")
	return func(ctx context.Context, tenantID uint64) (string, error) {
		if db == nil || provider == "" || tenantID == 0 {
			return "", fmt.Errorf("platform storage identity configuration missing")
		}
		var binding types.PlatformStorageBinding
		err := db.WithContext(ctx).Where("tenant_id = ?", tenantID).Take(&binding).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var subjects []string
			err = db.WithContext(ctx).Table("tenant_members AS m").
				Joins("JOIN platform_sso_identities AS i ON i.user_id = m.user_id").
				Joins("JOIN tenants AS t ON t.id = m.tenant_id AND t.deleted_at IS NULL").
				Where("m.tenant_id = ? AND m.role = ? AND m.status = ? AND m.deleted_at IS NULL AND i.provider = ?", tenantID, types.TenantRoleOwner, types.TenantMemberStatusActive, provider).
				Distinct().Pluck("i.subject", &subjects).Error
			if err != nil {
				return "", err
			}
			if len(subjects) != 1 {
				return "", fmt.Errorf("workspace %d has no unique platform storage owner", tenantID)
			}
			n, parseErr := strconv.ParseUint(subjects[0], 10, 64)
			if parseErr != nil || n == 0 || strconv.FormatUint(n, 10) != subjects[0] {
				return "", fmt.Errorf("invalid platform storage user ID")
			}
			binding = types.PlatformStorageBinding{TenantID: tenantID, Provider: provider, Subject: subjects[0], CreatedAt: time.Now()}
			if err = db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
				return "", err
			}
			// Read back the winning placement if two first writes raced.
			if err = db.WithContext(ctx).Where("tenant_id = ?", tenantID).Take(&binding).Error; err != nil {
				return "", err
			}
		}
		if binding.Provider != provider {
			return "", fmt.Errorf("workspace storage identity provider mismatch")
		}
		return binding.Subject, nil
	}
}
