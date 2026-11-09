package repository

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *authTokenRepository) GetPlatformSSOIdentity(ctx context.Context, provider, subject string) (*types.PlatformSSOIdentity, error) {
	var identity types.PlatformSSOIdentity
	err := r.db.WithContext(ctx).Where("provider = ? AND subject = ?", provider, subject).First(&identity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &identity, err
}

func (r *authTokenRepository) BindPlatformSSOIdentity(ctx context.Context, identity *types.PlatformSSOIdentity) error {
	return r.db.WithContext(ctx).Create(identity).Error
}

func (r *authTokenRepository) CreatePlatformSSOSession(ctx context.Context, session *types.PlatformSSOSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *authTokenRepository) WithPlatformSSOSession(ctx context.Context, id string, fn func(*types.PlatformSSOSession) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session types.PlatformSSOSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return types.ErrPlatformSSOInvalid
			}
			return err
		}
		if err := fn(&session); err != nil {
			return err
		}
		return tx.Save(&session).Error
	})
}
