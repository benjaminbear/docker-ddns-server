package certificates

import (
	"errors"

	"gorm.io/gorm"
	"strings"
	"time"
)

// Automation is a single administrator-controlled policy. Consent is captured when saving it.
type Automation struct {
	ID                               uint `gorm:"primaryKey"`
	Enabled                          bool
	Scope, Email, KeyType, Directory string
}

func (s *Service) SaveAutomation(a *Automation) error {
	if a.Scope != "zones" {
		return errors.New("invalid automatic certificate scope")
	}
	if _, err := s.Validate(s.Records.Zones[0], a.Email, a.KeyType); err != nil {
		return err
	}
	a.ID = 1
	return s.DB.Save(a).Error
}
func (s *Service) Tick(now time.Time) {
	if s.busy.TryLock() {
		s.CleanupChallenges()
		s.busy.Unlock()
	}
	var policy Automation
	err := s.DB.First(&policy, 1).Error
	if err == nil && policy.Enabled {
		names := []string{}
		for _, zone := range s.Records.Zones {
			names = append(names, zone)
		}
		for _, name := range names {
			domains, err := s.Validate(name+",*."+name, policy.Email, policy.KeyType)
			if err != nil {
				continue
			}
			var existing Certificate
			err = s.DB.Where("domains = ? AND directory = ?", domains, policy.Directory).Order("id desc").First(&existing).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c := &Certificate{Domains: domains, Email: policy.Email, KeyType: policy.KeyType, Directory: policy.Directory, AutoRenew: true}
				if s.Start(c) == nil {
					return
				}
			}
		}
	}
	s.RenewDue(now)
}
func (s *Service) ManagedCertificate(c *Certificate) bool {
	for _, name := range strings.Split(c.Domains, ",") {
		if _, _, err := s.Records.Resolve(strings.TrimPrefix(name, "*.")); err != nil {
			return false
		}
	}
	return true
}
