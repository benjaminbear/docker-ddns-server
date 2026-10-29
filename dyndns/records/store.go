package records

import (
	"fmt"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"gorm.io/gorm"
	"strings"
	"sync"
)

// Store serializes DNS record edits; dynamic addresses remain owned by Hosts.
type Store struct {
	DB    *gorm.DB
	Zones []string
	Apply func([]nswrapper.RRset) error
	Mu    sync.Mutex
}

func (s *Store) Zone(zone string) bool {
	for _, z := range s.Zones {
		if z == zone {
			return true
		}
	}
	return false
}
func (s *Store) Resolve(fqdn string) (string, string, error) {
	fqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	best := ""
	for _, zone := range s.Zones {
		if (fqdn == zone || strings.HasSuffix(fqdn, "."+zone)) && len(zone) > len(best) {
			best = zone
		}
	}
	if best == "" {
		return "", "", fmt.Errorf("name is outside managed zones")
	}
	name := strings.TrimSuffix(fqdn, "."+best)
	if fqdn == best {
		name = "@"
	}
	return best, name, nil
}
func (s *Store) Replace(zone, name, kind string, ttl int, values []string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if !s.Zone(zone) {
		return fmt.Errorf("unknown zone")
	}
	if strings.HasPrefix(name, "_acme-challenge") {
		return fmt.Errorf("ACME challenge names are reserved")
	}
	set := nswrapper.RRset{Zone: zone, Name: name, Type: kind, TTL: ttl, Values: values}
	if _, err := nswrapper.RRScript([]nswrapper.RRset{set}); err != nil {
		return err
	}
	unique := map[string]bool{}
	rows := []model.Record{}
	for _, value := range values {
		canonical, err := nswrapper.FormatRData(kind, value)
		if err != nil {
			return err
		}
		if kind != "TXT" && kind != "CAA" {
			value = canonical
		}
		if unique[value] {
			continue
		}
		unique[value] = true
		rows = append(rows, model.Record{Domain: zone, Name: name, Type: kind, Value: value, TTL: ttl, Managed: "static"})
	}
	set.Values = nil
	for _, r := range rows {
		set.Values = append(set.Values, r.Value)
	}
	return s.DB.Transaction(func(tx *gorm.DB) error {
		base := strings.TrimPrefix(name, "*.")
		var hosts int64
		if err := tx.Model(&model.Host{}).Where("domain = ? AND hostname = ?", zone, base).Count(&hosts).Error; err != nil {
			return err
		}
		if hosts > 0 && (kind == "A" || kind == "AAAA" || kind == "CNAME") {
			return fmt.Errorf("address belongs to a DDNS host; use Hosts")
		}
		var aliases int64
		if err := tx.Model(&model.CName{}).Joins("JOIN hosts ON hosts.id = c_names.target_id").Where("hosts.domain = ? AND c_names.hostname IN ?", zone, []string{name, base}).Count(&aliases).Error; err != nil {
			return err
		}
		if aliases > 0 {
			var exactAliases int64
			if err := tx.Model(&model.CName{}).Joins("JOIN hosts ON hosts.id = c_names.target_id").Where("hosts.domain = ? AND c_names.hostname = ?", zone, name).Count(&exactAliases).Error; err != nil {
				return err
			}
			if kind != "CNAME" || exactAliases != aliases {
				return fmt.Errorf("CNAME cannot coexist with other records")
			}
			// Transfer an existing alias into the common record store only when
			// explicitly saved or deleted. A DNS failure rolls this back too.
			ids := tx.Model(&model.CName{}).Select("c_names.id").Joins("JOIN hosts ON hosts.id = c_names.target_id").Where("hosts.domain = ? AND c_names.hostname = ?", zone, name)
			if err := tx.Unscoped().Where("id IN (?)", ids).Delete(&model.CName{}).Error; err != nil {
				return err
			}
		}
		var conflicts int64
		q := tx.Model(&model.Record{}).Where("domain = ? AND name = ?", zone, name)
		if kind == "CNAME" {
			q = q.Where("type <> ?", kind)
		} else {
			q = q.Where("type = ?", "CNAME")
		}
		if err := q.Count(&conflicts).Error; err != nil {
			return err
		}
		if len(values) > 0 && conflicts > 0 {
			return fmt.Errorf("CNAME cannot coexist with other records")
		}
		if kind == "MX" {
			for _, value := range values {
				fields := strings.Fields(value)
				targetZone, targetName, e := s.Resolve(strings.TrimSuffix(fields[1], "."))
				if e != nil {
					continue
				}
				var aliases int64
				if e = tx.Model(&model.Record{}).Where("domain = ? AND name = ? AND type = ?", targetZone, targetName, "CNAME").Count(&aliases).Error; e != nil {
					return e
				}
				if aliases > 0 {
					return fmt.Errorf("MX target cannot be a CNAME")
				}
				if e = tx.Model(&model.CName{}).Joins("JOIN hosts ON hosts.id = c_names.target_id").Where("hosts.domain = ? AND c_names.hostname = ?", targetZone, targetName).Count(&aliases).Error; e != nil {
					return e
				}
				if aliases > 0 {
					return fmt.Errorf("MX target cannot be a CNAME")
				}
			}
		}
		if err := tx.Where("domain = ? AND name = ? AND type = ?", zone, name, kind).Delete(&model.Record{}).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		apply := s.Apply
		if apply == nil {
			apply = nswrapper.ApplyBatch
		}
		return apply([]nswrapper.RRset{set})
	})
}
