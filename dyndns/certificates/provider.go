package certificates

import (
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"github.com/benjaminbear/docker-ddns-server/dyndns/records"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"gorm.io/gorm"
	"strings"
	"sync"
	"time"
)

// Exact-value updates preserve pre-existing TXT data and overlapping apex/wildcard challenges.
type Challenge struct {
	ID                uint `gorm:"primaryKey"`
	Zone, Name, Value string
}

type Provider struct {
	DB      *gorm.DB
	Store   *records.Store
	mu      sync.Mutex
	pending [][3]string
	Change  func(string, string, string, bool) error
}

func (p *Provider) change(zone, name, value string, add bool) error {
	if p.Change != nil {
		return p.Change(zone, name, value, add)
	}
	return nswrapper.ChangeTXT(zone, name, value, add)
}
func (p *Provider) Present(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(strings.TrimPrefix(domain, "*."), keyAuth)
	zone, name, e := p.Store.Resolve(fqdn)
	if e != nil {
		return e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.DB != nil {
		if e = p.DB.Create(&Challenge{Zone: zone, Name: name, Value: value}).Error; e != nil {
			return e
		}
	}
	if e = p.change(zone, name, value, true); e != nil {
		return e
	}
	p.pending = append(p.pending, [3]string{zone, name, value})
	return nil
}
func (p *Provider) CleanUp(domain, token, keyAuth string) error {
	fqdn, value := dns01.GetRecord(strings.TrimPrefix(domain, "*."), keyAuth)
	zone, name, e := p.Store.Resolve(fqdn)
	if e != nil {
		return e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e = p.change(zone, name, value, false); e != nil {
		return e
	}
	if p.DB != nil {
		p.DB.Where("zone = ? AND name = ? AND value = ?", zone, name, value).Delete(&Challenge{})
	}
	for i, v := range p.pending {
		if v == [3]string{zone, name, value} {
			p.pending = append(p.pending[:i], p.pending[i+1:]...)
			break
		}
	}
	return nil
}
func (p *Provider) CleanupAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range p.pending {
		if p.change(v[0], v[1], v[2], false) == nil && p.DB != nil {
			p.DB.Where("zone = ? AND name = ? AND value = ?", v[0], v[1], v[2]).Delete(&Challenge{})
		}
	}
}
func (p *Provider) Timeout() (time.Duration, time.Duration) { return 3 * time.Minute, 2 * time.Second }

func (s *Service) CleanupChallenges() {
	var pending []Challenge
	if s.DB.Find(&pending).Error != nil {
		return
	}
	for _, v := range pending {
		if nswrapper.ChangeTXT(v.Zone, v.Name, v.Value, false) == nil {
			s.DB.Delete(&v)
		}
	}
}
