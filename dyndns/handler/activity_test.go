package handler

import (
	"compress/gzip"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollapseActivityPreservesTransitions(t *testing.T) {
	now := time.Now()
	rows := []activityEntry{}
	for i, ip := range []string{"192.0.2.1", "192.0.2.1", "192.0.2.2", "192.0.2.1", "192.0.2.1"} {
		rows = append(rows, activityEntry{Hostname: "router.example.test", CallerIP: ip, Code: 200, Status: true, TimeStamp: now.Add(time.Duration(i) * time.Second)})
	}
	rows = append(rows, activityEntry{Hostname: "other.example.test", Code: 400, TimeStamp: now.Add(time.Second)})
	got := collapseActivity(rows)
	if len(got) != 4 || got[0].Count != 2 || got[1].CallerIP != "192.0.2.2" || got[2].Count != 2 {
		t.Fatalf("lost transitions: %+v", got)
	}
	rows[4].Code = 400
	rows[4].Status = false
	if len(collapseActivity(rows)) != 5 {
		t.Fatal("result change was collapsed")
	}
}

func proxyFixture(at string) string {
	return "192.0.2.10 - - [" + at + "] \"GET /nic/update?hostname=Router.Example.test&password=never-store&myip=192.0.2.1 HTTP/1.0\" 400 248 \"-\" \"NVR\"\n"
}
func TestProxyTailRestartRotationPartialAndTruncate(t *testing.T) {
	h := &Handler{DB: testDB(t), ClearInterval: 69}
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	line := proxyFixture(time.Now().Format("02/Jan/2006:15:04:05 -0700"))
	if err := os.WriteFile(path, []byte(line+"partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.importProxyFile(path); err != nil {
		t.Fatal(err)
	}
	h = &Handler{DB: h.DB, ClearInterval: 69}
	if err := h.importProxyFile(path); err != nil {
		t.Fatal(err)
	}
	var count int64
	h.DB.Model(&model.ProxyLog{}).Count(&count)
	if count != 1 {
		t.Fatalf("count %d", count)
	}
	var row model.ProxyLog
	h.DB.First(&row)
	if row.Hostname != "router.example.test" || row.Code != 400 || row.SentIP != "192.0.2.1" {
		t.Fatalf("bad parse %+v", row)
	}
	rotated := filepath.Join(dir, "access.log.1")
	os.Rename(path, rotated)
	if err := h.importProxyFile(rotated); err != nil {
		t.Fatal(err)
	}
	next := proxyFixture(time.Now().Add(time.Second).Format("02/Jan/2006:15:04:05 -0700"))
	os.WriteFile(path, []byte(next), 0600)
	if err := h.importProxyFile(path); err != nil {
		t.Fatal(err)
	}
	h.DB.Model(&model.ProxyLog{}).Count(&count)
	if count != 2 {
		t.Fatalf("rotation count %d", count)
	}
	// Copy-truncate and regrow to the same size: anchor must catch this.
	os.WriteFile(path, []byte(line), 0600)
	if err := h.importProxyFile(path); err != nil {
		t.Fatal(err)
	}
	h.DB.Model(&model.ProxyLog{}).Count(&count)
	if count != 2 {
		t.Fatal("duplicate imported")
	}
	var cursors []model.LogCursor
	h.DB.Find(&cursors)
	if len(cursors) != 2 {
		t.Fatal("rename did not preserve cursor")
	}
}
func TestActivityIncludesAuthAndSuppressesProxyDuplicate(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	host := testHost(t, h.DB)
	now := time.Now()
	h.DB.Create(&model.Log{HostID: host.ID, TimeStamp: now, Status: true, Message: "No errors occurred"})
	h.DB.Create(&model.FailedAuth{Hostname: "router.example.test", Timestamp: now.Add(time.Second), Path: "/nic/update", IPAddress: "192.0.2.2", Password: "secret"})
	h.DB.Create(&model.ProxyLog{Key: "duplicate", Hostname: "router.example.test", TimeStamp: now.Add(time.Second), Code: 401, Source: "nginx"})
	h.DB.Create(&model.ProxyLog{Key: "proxy-only", Hostname: "router.example.test", TimeStamp: now.Add(2 * time.Second), Code: 400, Source: "nginx"})
	rows, err := h.activity()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Source != "nginx" || rows[1].Code != 401 || !rows[2].Status {
		t.Fatalf("unexpected entries %+v", rows)
	}
}

func TestProxyGzipAndZeroRetention(t *testing.T) {
	h := &Handler{DB: testDB(t)} // zero means retain all
	dir := t.TempDir()
	plain := filepath.Join(dir, "access.log")
	line := proxyFixture("01/Jan/2020:00:00:00 +0000")
	os.WriteFile(plain, []byte(line), 0600)
	if err := h.importProxyFile(plain); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(dir, "access.log.1.gz"))
	z := gzip.NewWriter(f)
	z.Write([]byte(line))
	z.Close()
	f.Close()
	for i := 0; i < 2; i++ {
		if err := h.importProxyFile(filepath.Join(dir, "access.log.1.gz")); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	h.DB.Model(&model.ProxyLog{}).Count(&count)
	if count != 1 {
		t.Fatalf("gzip duplicate or zero retention bug: %d", count)
	}
}
func TestSecurityCollapsePreservesCredentialChanges(t *testing.T) {
	rows := []model.FailedAuth{{Hostname: "router.example.test", Password: "one"}, {Hostname: "router.example.test", Password: "two"}, {Hostname: "router.example.test", Password: "one"}, {Hostname: "router.example.test", Password: "one"}}
	got := collapseFailedAuths(rows)
	if len(got) != 3 || got[2].RepeatCount != 2 {
		t.Fatal("credential changes lost")
	}
}
