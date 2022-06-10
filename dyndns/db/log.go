package db

import (
	"time"

	"gorm.io/gorm"
)

// Log defines a log entry.
type Log struct {
	gorm.Model
	Status    bool
	Message   string
	Host      Host
	HostID    uint
	SentIP    string
	CallerIP  string
	TimeStamp time.Time
	UserAgent string
}
