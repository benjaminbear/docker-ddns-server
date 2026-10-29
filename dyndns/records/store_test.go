package records

import (
	"errors"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

func TestRRsetsAndLegacyOwnership(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	db.AutoMigrate(&model.Record{}, &model.Host{}, &model.CName{})
	s := &Store{DB: db, Zones: []string{"example.test"}, Apply: func([]nswrapper.RRset) error { return nil }}
	if e = s.Replace("example.test", "@", "TXT", 600, []string{"one", "two"}); e != nil {
		t.Fatal(e)
	}
	s.Apply = func([]nswrapper.RRset) error { return errors.New("offline") }
	if e = s.Replace("example.test", "@", "TXT", 600, []string{"lost"}); e == nil {
		t.Fatal("DNS failure accepted")
	}
	var rows []model.Record
	db.Find(&rows)
	if len(rows) != 2 {
		t.Fatal("rollback lost records", rows)
	}
	s.Apply = func([]nswrapper.RRset) error { return nil }
	if e = s.Replace("other.test", "@", "TXT", 60, []string{"no"}); e == nil {
		t.Fatal("zone bypass")
	}
	if e = s.Replace("example.test", "alias", "CNAME", 60, []string{"www.example.net"}); e != nil {
		t.Fatal(e)
	}
	if e = s.Replace("example.test", "alias", "TXT", 60, []string{"no"}); e == nil {
		t.Fatal("CNAME conflict")
	}
	host := model.Host{Hostname: "router", Domain: "example.test"}
	db.Create(&host)
	db.Create(&model.CName{Hostname: "legacy", TargetID: host.ID})
	for _, tc := range []struct{ n, k, v string }{{"router", "A", "192.0.2.2"}, {"*.router", "AAAA", "2001:db8::2"}, {"legacy", "TXT", "no"}, {"*.legacy", "CNAME", "www.example.net"}, {"_acme-challenge", "TXT", "no"}} {
		if e = s.Replace("example.test", tc.n, tc.k, 60, []string{tc.v}); e == nil {
			t.Fatalf("ownership bypass %+v", tc)
		}
	}
	if e = s.Replace("example.test", "router", "TXT", 60, []string{"kept"}); e != nil {
		t.Fatal(e)
	}
}

func TestExistingAliasEditsAreTransactional(t *testing.T) {
	for _, mode := range []string{"save", "delete", "dns-failure", "wildcard-save"} {
		t.Run(mode, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "aliases.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			db.AutoMigrate(&model.Record{}, &model.Host{}, &model.CName{})
			host := model.Host{Hostname: "router", Domain: "example.test"}
			db.Create(&host)
			name := "alias"
			if mode == "wildcard-save" {
				name = "*.alias"
			}
			alias := model.CName{Hostname: name, TargetID: host.ID, Ttl: 60}
			db.Create(&alias)
			s := &Store{DB: db, Zones: []string{"example.test"}, Apply: func(sets []nswrapper.RRset) error {
				if len(sets) != 1 || sets[0].Name != name || sets[0].Type != "CNAME" {
					t.Fatal(sets)
				}
				if mode == "dns-failure" {
					return errors.New("DNS unavailable")
				}
				return nil
			}}
			values := []string{"new.example.net"}
			if mode == "delete" {
				values = nil
			}
			err = s.Replace("example.test", name, "CNAME", 600, values)
			var aliases, records, hosts int64
			db.Model(&model.CName{}).Count(&aliases)
			db.Model(&model.Record{}).Count(&records)
			db.Model(&model.Host{}).Count(&hosts)
			if hosts != 1 {
				t.Fatal("host changed")
			}
			if mode == "dns-failure" {
				if err == nil || aliases != 1 || records != 0 {
					t.Fatalf("rollback failed: %v aliases=%d records=%d", err, aliases, records)
				}
			} else {
				if err != nil || aliases != 0 {
					t.Fatalf("alias edit failed: %v aliases=%d", err, aliases)
				}
				expected := int64(1)
				if mode == "delete" {
					expected = 0
				}
				if records != expected {
					t.Fatal(records)
				}
			}
		})
	}
}
