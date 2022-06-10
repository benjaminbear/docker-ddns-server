package db

import (
	"gorm.io/gorm"
)

// CName is a dns cname entry.
type CName struct {
	gorm.Model
	Hostname string `gorm:"not null" form:"hostname" validate:"required,hostname"`
	Target   Host   `validate:"required,hostname"`
	TargetID uint
	Ttl      int `form:"ttl" validate:"required,min=20,max=86400"`
}
