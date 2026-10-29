package model

import (
	"time"

	"gorm.io/gorm"
)

// Host is a dns host entry.
type Host struct {
	gorm.Model
	Hostname   string    `gorm:"uniqueIndex:idx_host_domain;not null" form:"hostname" validate:"required,min=1"` // Allow 1 character hostnames
	Domain     string    `gorm:"uniqueIndex:idx_host_domain;not null" form:"domain" validate:"required,fqdn"`
	Ip         string    `form:"ip" validate:"omitempty,ipv4|ipv6"`
	Ttl        int       `form:"ttl" validate:"required,min=20,max=86400"`
	LastUpdate time.Time `form:"lastupdate"`
	UserName   string    `gorm:"not null" form:"username" validate:"min=1"` // Allow 1 character usernames
	Password   string    `form:"password" validate:"min=6"`                 // Minimum 6 character passwords
}

// UpdateHost updates all fields of a host entry
// and sets a new LastUpdate date.
func (h *Host) UpdateHost(updateHost *Host) (updateRecord bool) {
	updateRecord = false
	if h.Ip != updateHost.Ip || h.Ttl != updateHost.Ttl {
		updateRecord = true
		h.LastUpdate = time.Now()
	}

	h.Ip = updateHost.Ip
	h.Ttl = updateHost.Ttl
	h.UserName = updateHost.UserName
	h.Password = updateHost.Password

	return
}
