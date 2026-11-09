package types

import (
	"errors"
	"time"
)

var ErrPlatformSSOUnavailable = errors.New("platform SSO temporarily unavailable")
var ErrPlatformSSOInvalid = errors.New("platform SSO session is invalid or expired")

type PlatformSSOIdentity struct {
	Provider    string `gorm:"primaryKey"`
	Subject     string `gorm:"primaryKey"`
	UserID      string `gorm:"index;not null"`
	Account     string
	DisplayName string
	CreatedAt   time.Time
}

type PlatformSSOSession struct {
	ID       string `gorm:"primaryKey"`
	Provider string
	Subject  string
	UserID   string `gorm:"index;not null"`
	// Secrets are always encrypted with SYSTEM_AES_KEY before persistence.
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ExpiresAt    time.Time
	CheckedAt    time.Time
	IsRevoked    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type PlatformSSOLoginRequest struct {
	AccessToken  string `json:"access_token" binding:"required"`
	RefreshToken string `json:"refresh_token"`
	// An assertion from the parent for detecting stale account-switch responses;
	// never the authority for the logged-in identity.
	ExpectedUserID string `json:"expected_user_id"`
}
