package model

import "time"

// ProxyLog contains only the fields needed by the activity viewer, never credentials or raw URLs.
type ProxyLog struct {
	ID        uint      `gorm:"primaryKey"`
	Key       string    `gorm:"uniqueIndex"`
	TimeStamp time.Time `gorm:"index"`
	Hostname  string
	CallerIP  string
	SentIP    string
	UserAgent string
	Path      string
	Code      int
	Message   string
	Source    string
}

type LogCursor struct {
	Key    string `gorm:"primaryKey"`
	Offset int64
	Anchor string
}
