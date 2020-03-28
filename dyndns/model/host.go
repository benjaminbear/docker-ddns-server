package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

type Host struct {
	gorm.Model
	Hostname   string    `gorm:"unique;not null" form:"hostname" validate:"required,hostname"`
	Ip         string    `form:"ip" validate:"omitempty,ipv4"`
	Ttl        int       `form:"ttl" validate:"required,min=20,max=86400"`
	LastUpdate time.Time `form:"lastupdate"`
	UserName   string    `gorm:"unique" form:"username" validate:"min=8"`
	Password   string    `form:"password" validate:"min=8"`
}

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
