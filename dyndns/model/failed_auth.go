package model

import (
	"time"

	"gorm.io/gorm"
)

// FailedAuth tracks failed authentication attempts
// WARNING: This includes password logging which is a security risk.
// Passwords should be handled carefully and never displayed without authorization.
type FailedAuth struct {
	RepeatCount int    `gorm:"-"`
	Hostname    string // Requested update hostname; empty for old or non-update records.
	gorm.Model
	IPAddress string `gorm:"index;not null"`
	UserAgent string
	Timestamp time.Time `gorm:"index"`
	Path      string    // The path they tried to access
	Username  string    // Username they attempted (if provided)
	Password  string    // Password they attempted (SECURITY RISK - handle carefully)
}

// BlockedIP represents an IP that has been blocked
type BlockedIP struct {
	gorm.Model
	IPAddress     string    `gorm:"uniqueIndex;not null"`
	BlockedAt     time.Time `gorm:"index"`
	BlockedUntil  time.Time `gorm:"index"` // For temporary blocks
	FailureCount  int
	IsPermanent   bool // Flag for permanent blocks
	LastAttemptAt time.Time
	Reason        string
}

// IsBlocked checks if a block is still active
func (b *BlockedIP) IsBlocked() bool {
	if b.IsPermanent {
		return true
	}
	return time.Now().Before(b.BlockedUntil)
}
