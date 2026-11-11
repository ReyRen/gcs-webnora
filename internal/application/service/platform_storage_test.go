package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPlatformStorageOwnerIsDurableAndNotTheViewer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}, &types.PlatformSSOIdentity{}, &types.PlatformStorageBinding{}))
	ctx := context.Background()
	for _, uid := range []string{"369", "568"} {
		require.NoError(t, db.Create(&types.PlatformSSOIdentity{Provider: "https://platform", Subject: uid, UserID: "user-" + uid}).Error)
	}
	require.NoError(t, db.Create(&types.Tenant{ID: 7, Name: "source"}).Error)
	owner := types.TenantMember{TenantID: 7, UserID: "user-369", Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&types.TenantMember{TenantID: 7, UserID: "user-568", Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive}).Error)
	resolve := NewPlatformStorageUserResolver(db, "https://platform/")
	uid, err := resolve(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "369", uid)
	// Removing/changing the business owner must not redirect reads of existing
	// objects to a new user's folder. Relocation is an explicit migration.
	require.NoError(t, db.Model(&owner).Update("role", types.TenantRoleViewer).Error)
	uid, err = resolve(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "369", uid)
	_, err = resolve(ctx, 8)
	require.Error(t, err)
	_, err = NewPlatformStorageUserResolver(db, "https://other-platform")(ctx, 7)
	require.Error(t, err)
}

func TestPlatformStorageRejectsAmbiguousAndUnsafeOwners(t *testing.T) {
	for _, subjects := range [][]string{{"369", "568"}, {"../568"}, {"000369"}} {
		t.Run(subjects[0], func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.TenantMember{}, &types.PlatformSSOIdentity{}, &types.PlatformStorageBinding{}))
			require.NoError(t, db.Create(&types.Tenant{ID: 7, Name: "workspace"}).Error)
			for _, subject := range subjects {
				require.NoError(t, db.Create(&types.PlatformSSOIdentity{Provider: "platform", Subject: subject, UserID: subject}).Error)
				require.NoError(t, db.Create(&types.TenantMember{TenantID: 7, UserID: subject, Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive}).Error)
			}
			_, err = NewPlatformStorageUserResolver(db, "platform")(context.Background(), 7)
			require.Error(t, err)
		})
	}
}
