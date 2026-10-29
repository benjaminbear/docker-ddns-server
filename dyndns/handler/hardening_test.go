package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	if err := migrateDatabase(db); err != nil {
		t.Fatal(err)
	}
	return db
}
func testHost(t *testing.T, db *gorm.DB) model.Host {
	t.Helper()
	host := model.Host{Hostname: "router", Domain: "example.test", UserName: "user", Password: "test-password", Ttl: 60}
	if err := db.Create(&host).Error; err != nil {
		t.Fatal(err)
	}
	return host
}
func TestUpdateAuthentication(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	testHost(t, h.DB)
	for _, tc := range []struct {
		user, password, hostname string
		want                     bool
	}{
		{"user", "test-password", "router.example.test", true},
		{"USER", "test-password", "ROUTER.EXAMPLE.TEST", true},
		{"", "test-password", "router.example.test", false},
		{"user", "", "router.example.test", false},
		{"", "", "router.example.test", false},
		{"user", "wrong", "router.example.test", false},
		{"user", "test-password", ".example.test", false},
		{"user", "test-password", "router.", false},
		{"user", "test-password", "other.example.test", false},
		{"' OR 1=1 --", "test-password", "router.example.test", false},
	} {
		c := echo.New().NewContext(httptest.NewRequest("GET", "/nic/update?hostname="+url.QueryEscape(tc.hostname), nil), httptest.NewRecorder())
		got, err := h.AuthenticateUpdate(tc.user, tc.password, c)
		if err != nil || got != tc.want {
			t.Errorf("user=%q hostname=%q: got %v err=%v", tc.user, tc.hostname, got, err)
		}
	}
}
func TestCNameTargetRejectsSQL(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	testHost(t, h.DB)
	for _, id := range []string{"1 OR 1=1", "id = 1", "0", "-1", "", "18446744073709551616"} {
		form := url.Values{"target_id": {id}, "hostname": {"alias"}, "ttl": {"60"}}
		req := httptest.NewRequest("POST", "/@/cnames/add", strings.NewReader(form.Encode()))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
		rec := httptest.NewRecorder()
		e := echo.New()
		e.Validator = &CustomValidator{Validator: validator.New()}
		if err := h.CreateCName(e.NewContext(req, rec)); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("id=%q status=%d", id, rec.Code)
		}
	}
	var count int64
	h.DB.Model(&model.CName{}).Count(&count)
	if count != 0 {
		t.Fatal("invalid targets inserted CNAMEs")
	}
}
func TestDeleteRollbackAndDNSCascade(t *testing.T) {
	for _, kind := range []string{"host", "cname"} {
		for _, failure := range []string{"dns", "database", "none"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				db := testDB(t)
				host := testHost(t, db)
				alias := model.CName{Hostname: "alias", TargetID: host.ID, Ttl: 60}
				if err := db.Create(&alias).Error; err != nil {
					t.Fatal(err)
				}
				entry := model.Log{HostID: host.ID}
				db.Create(&entry)
				called := false
				h := &Handler{DB: db, AllowWildcard: true, deleteRecords: func(names []string, zone string, wild bool) error {
					called = true
					want := []string{"alias"}
					if kind == "host" {
						want = []string{"router", "alias"}
					}
					if !reflect.DeepEqual(names, want) || zone != "example.test" || !wild {
						t.Fatalf("bad DNS delete: %v %s %v", names, zone, wild)
					}
					if failure == "dns" {
						return errors.New("DNS unavailable")
					}
					return nil
				}}
				if failure == "database" {
					sql := "CREATE TRIGGER fail_delete BEFORE DELETE ON c_names BEGIN SELECT RAISE(ABORT, 'test failure'); END"
					if err := db.Exec(sql).Error; err != nil {
						t.Fatal(err)
					}
				}
				rec := httptest.NewRecorder()
				c := echo.New().NewContext(httptest.NewRequest("GET", "/delete/1", nil), rec)
				c.SetParamNames("id")
				c.SetParamValues("1")
				var err error
				if kind == "host" {
					err = h.DeleteHost(c)
				} else {
					err = h.DeleteCName(c)
				}
				if err != nil {
					t.Fatal(err)
				}
				wantCode := 200
				if failure != "none" {
					wantCode = 400
				}
				if rec.Code != wantCode {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
				}
				if called != (failure != "database") {
					t.Fatal("unexpected DNS call")
				}
				var hosts, aliases, logs int64
				db.Model(&model.Host{}).Count(&hosts)
				db.Model(&model.CName{}).Count(&aliases)
				db.Model(&model.Log{}).Count(&logs)
				if failure != "none" && (hosts != 1 || aliases != 1 || logs != 1) {
					t.Fatalf("rollback failed: %d %d %d", hosts, aliases, logs)
				}
				if failure == "none" && (aliases != 0 || (kind == "host" && (hosts != 0 || logs != 0))) {
					t.Fatal("cascade incomplete")
				}
			})
		}
	}
}
func TestUniqueMigration(t *testing.T) {
	db := testDB(t)
	if err := db.Migrator().DropIndex(&model.Host{}, "idx_host_domain"); err != nil {
		t.Fatal(err)
	}
	first := testHost(t, db)
	second := testHost(t, db)
	alias := model.CName{Hostname: "alias", TargetID: second.ID, Ttl: 60}
	db.Create(&alias)
	entry := model.Log{HostID: second.ID}
	db.Create(&entry)
	for i := 0; i < 2; i++ {
		if err := migrateDatabase(db); err != nil {
			t.Fatal(err)
		}
	}
	db.First(&alias, alias.ID)
	db.First(&entry, entry.ID)
	if alias.TargetID != first.ID || entry.HostID != first.ID {
		t.Fatal("dependents not remapped")
	}
	duplicate := model.Host{Hostname: first.Hostname, Domain: first.Domain}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("unique constraint missing")
	}
	var count int64
	db.Unscoped().Model(&model.Host{}).Count(&count)
	if count != 1 {
		t.Fatalf("host count=%d", count)
	}
}
func TestSessionSecretAndProxyHTTPS(t *testing.T) {
	for _, secret := range []string{"", "short", strings.Repeat("x", 32)} {
		t.Setenv("DDNS_SESSION_SECRET", secret)
		h := &Handler{}
		if err := h.InitSessionStore(); (err == nil) != (len(secret) >= 32) {
			t.Fatalf("secret length=%d err=%v", len(secret), err)
		}
	}
	h := &Handler{}
	req := httptest.NewRequest("GET", "http://example.test/@/hosts", nil)
	req.RemoteAddr = "10.0.0.2:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	c := echo.New().NewContext(req, httptest.NewRecorder())
	if h.IsHTTPS(c) {
		t.Fatal("untrusted HTTPS header accepted")
	}
	h.TrustedProxies, _ = nswrapper.ParseTrustedProxies("10.0.0.2")
	if !h.IsHTTPS(c) {
		t.Fatal("trusted HTTPS header rejected")
	}
}
func TestAPIAuthFailuresDoNotBlock(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	testHost(t, h.DB)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("GET", "/nic/update?hostname=router.example.test", nil)
		req.SetBasicAuth("user", "wrong")
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetPath("/nic/update")
		err := h.UpdateAuthMiddleware()(func(c echo.Context) error { t.Fatal("authenticated invalid password"); return nil })(c)
		if err != nil || rec.Code != 401 || rec.Body.String() != "badauth\n" {
			t.Fatalf("response %d %s %v", rec.Code, rec.Body, err)
		}
	}
	blocked, _, err := h.IsIPBlocked("192.0.2.1")
	if err != nil || blocked {
		t.Fatal("API failure blocked client")
	}
	var count int64
	h.DB.Model(&model.FailedAuth{}).Count(&count)
	if count != 4 {
		t.Fatal("failed auth intelligence lost")
	}
}

func TestMigrationRollsBackOnDependentFailure(t *testing.T) {
	db := testDB(t)
	if err := db.Migrator().DropIndex(&model.Host{}, "idx_host_domain"); err != nil {
		t.Fatal(err)
	}
	testHost(t, db)
	duplicate := testHost(t, db)
	db.Create(&model.CName{Hostname: "alias", TargetID: duplicate.ID, Ttl: 60})
	if err := db.Exec("CREATE TRIGGER fail_remap BEFORE UPDATE ON c_names BEGIN SELECT RAISE(ABORT, 'test failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(db); err == nil {
		t.Fatal("expected migration failure")
	}
	var count int64
	db.Model(&model.Host{}).Count(&count)
	if count != 2 {
		t.Fatal("failed migration removed a host")
	}
	var alias model.CName
	db.First(&alias)
	if alias.TargetID != duplicate.ID {
		t.Fatal("failed migration changed target")
	}
}

func TestProxyCannotEvadeBlock(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	if err := h.BlockIP("192.0.2.1", 3, "test block"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/nic/update", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("X-Real-IP", "203.0.113.99")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	err := h.IPBlockerMiddleware()(func(c echo.Context) error { t.Fatal("spoof evaded block"); return nil })(c)
	if err != nil || rec.Code != 302 || rec.Header().Get("Location") != "http://127.0.0.1" {
		t.Fatalf("wrong block response: %v %d", err, rec.Code)
	}
}

func TestFailedUpdateCapturesRequestedHostname(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	e := echo.New()
	e.GET("/nic/update", func(c echo.Context) error { return c.NoContent(200) }, h.UpdateAuthMiddleware())
	req := httptest.NewRequest("GET", "/nic/update?hostname=CAMERA.EXAMPLE.Net&password=not-for-hostname-log", nil)
	req.SetBasicAuth("Quad", "fixture-wrong-password")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	var row model.FailedAuth
	if err := h.DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Hostname != "camera.example.net" || row.Path != "/nic/update" {
		t.Fatal(row.Hostname, row.Path)
	}
	if err := h.LogFailedAuth("192.0.2.1", "fixture", "/nic/update", "fixture", "fixture", strings.Repeat("x", 300)); err != nil {
		t.Fatal(err)
	}
	var newest model.FailedAuth
	h.DB.Order("id desc").First(&newest)
	if len(newest.Hostname) != 253 {
		t.Fatal("hostname must be bounded")
	}
}
