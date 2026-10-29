package model

// Record is one value in an RRset. Hard deletes permit reuse of unique names.
type Record struct {
	ID      uint   `json:"id" gorm:"primaryKey"`
	Domain  string `json:"domain" gorm:"not null;uniqueIndex:idx_rr"`
	Name    string `json:"name" gorm:"not null;uniqueIndex:idx_rr"`
	Type    string `json:"type" gorm:"not null;uniqueIndex:idx_rr"`
	Value   string `json:"value" gorm:"not null;uniqueIndex:idx_rr"`
	TTL     int    `json:"ttl"`
	HostID  *uint  `json:"host_id" gorm:"index"`
	Managed string `json:"managed"`
}
