package handler

import (
	"bytes"
	"github.com/benjaminbear/docker-ddns-server/dyndns/certificates"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/benjaminbear/docker-ddns-server/dyndns/records"
	"github.com/foolin/goview"
	"github.com/labstack/echo/v4"
	"io"

	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFeatureRoutesRequireAuthentication(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		h := &Handler{DisableAdminAuth: disabled}
		t.Setenv("DDNS_SESSION_SECRET", strings.Repeat("x", 32))
		if e := h.InitSessionStore(); e != nil {
			t.Fatal(e)
		}
		e := echo.New()
		h.FeatureRoutes(e.Group("/@"))
		for _, path := range []string{"/@/records", "/@/certs", "/@/certs/status", "/@/certs/1/download/privkey.pem"} {
			r := httptest.NewRecorder()
			e.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
			if r.Code != 403 && r.Code != 302 && r.Code != 303 {
				t.Fatalf("%s unprotected: %d", path, r.Code)
			}
		}
	}
}
func TestDownloadsRequireHTTPS(t *testing.T) {
	h := &Handler{}
	c := echo.New().NewContext(httptest.NewRequest("GET", "http://example.test/@/certs/1/download/privkey.pem", nil), httptest.NewRecorder())
	err := h.DownloadCertificate(c)
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != 403 {
		t.Fatal(err)
	}
}
func TestFeatureCSRF(t *testing.T) {
	h := &Handler{DB: testDB(t)}
	t.Setenv("DDNS_SESSION_SECRET", strings.Repeat("x", 32))
	h.InitSessionStore()
	e := echo.New()
	h.FeatureRoutes(e.Group("/@"))
	req := httptest.NewRequest("POST", "/@/records", strings.NewReader("zone=example.test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(req, recorder)
	session, _ := h.GetSession(ctx)
	session.Values[SessionAuthKey] = true
	session.Values[SessionExpiresAt] = time.Now().Add(time.Hour).Unix()
	session.Save(req, recorder)
	for _, cookie := range recorder.Result().Cookies() {
		req.AddCookie(cookie)
	}
	recorder = httptest.NewRecorder()
	// Authenticated mutations still require a valid CSRF token.
	e.ServeHTTP(recorder, req)
	if recorder.Code != 400 && recorder.Code != 403 {
		t.Fatal(recorder.Code)
	}
}
func TestFeatureTemplates(t *testing.T) {
	view := goview.New(goview.Config{Root: "../views", Master: "layouts/master", Extension: ".html", Funcs: template.FuncMap{"year": func() string { return "2026" }}})
	for _, name := range []string{"records", "certificates"} {
		var out bytes.Buffer
		data := map[string]interface{}{"title": "Test", "csrf": "fixture", "zone": "example.test", "zones": []string{"example.test"}, "types": []string{"TXT", "MX"}, "sets": []rrView{{Name: "@", Type: "TXT", TTL: 60, Values: "<script>alert(1)</script>", Zone: "example.test"}}, "enabled": true, "certs": []certificates.Certificate{{ID: 1, Domains: "*.example.test", Status: "valid", CertPEM: []byte("fixture")}}}
		if e := view.RenderWriter(&out, name, data); e != nil {
			t.Fatal(name, e)
		}
		if strings.Contains(out.String(), "<script>alert(1)</script>") {
			t.Fatal("unescaped record")
		}
		if !strings.Contains(out.String(), "aria-label=\"Certificates\"") {
			t.Fatal("missing accessible navigation")
		}
	}
}

func TestCertificateStatusContainsOnlyJobMetadata(t *testing.T) {
	db := testDB(t)
	db.AutoMigrate(&certificates.Certificate{})
	db.Create(&certificates.Certificate{Domains: "private.example.test", Email: "owner@example.test", Status: "pending", KeyPEMEnc: []byte("secret-key"), CertPEM: []byte("certificate-data")})
	h := &Handler{DB: db, Certificates: &certificates.Service{DB: db}}
	rec := httptest.NewRecorder()
	if err := h.CertificateStatus(echo.New().NewContext(httptest.NewRequest("GET", "/@/certs/status", nil), rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || rec.Body.String() != "[{\"id\":1,\"status\":\"pending\"}]\n" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("status response can be cached")
	}
	db.Model(&certificates.Certificate{}).Where("id = ?", 1).Update("status", "failed")
	rec = httptest.NewRecorder()
	h.CertificateStatus(echo.New().NewContext(httptest.NewRequest("GET", "/@/certs/status", nil), rec))
	if !strings.Contains(rec.Body.String(), `"status":"failed"`) {
		t.Fatal(rec.Body.String())
	}
}

func TestFailedHostnameTooltipEscapesInput(t *testing.T) {
	view := goview.New(goview.Config{Root: "../views", Master: "layouts/master", Extension: ".html", Funcs: template.FuncMap{"year": func() string { return "2026" }, "hasPrefix": strings.HasPrefix, "mod": func(i, j int) int { return i % j }}})
	for _, name := range []string{"failed_auths", "security_dashboard"} {
		var out bytes.Buffer
		rows := []model.FailedAuth{{Hostname: `<img src=x onerror=alert(1)>`, IPAddress: "192.0.2.1"}, {IPAddress: "192.0.2.2"}}
		if err := view.RenderWriter(&out, name, map[string]interface{}{"failedAuths": rows, "blockedIPs": []model.BlockedIP{}}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), `<img src=x`) {
			t.Fatal("unescaped hostname")
		}
		if !strings.Contains(out.String(), "Hostname not recorded") || !strings.Contains(out.String(), `role="tooltip"`) {
			t.Fatal("missing hostname tooltip")
		}
	}
}

type recordPageCapture struct{ data echo.Map }

func (r *recordPageCapture) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	r.data = data.(echo.Map)
	return nil
}
func TestRecordsIncludeExistingAliasesAndFilterZones(t *testing.T) {
	db := testDB(t)
	host := testHost(t, db)
	db.Create(&model.CName{Hostname: "alias", TargetID: host.ID, Ttl: 120})
	db.Create(&model.Record{Domain: "example.test", Name: "@", Type: "TXT", TTL: 60, Value: "first"})
	db.Create(&model.Record{Domain: "other.test", Name: "@", Type: "TXT", TTL: 60, Value: "second"})
	h := &Handler{DB: db, Config: Envs{Domains: []string{"example.test", "other.test"}}, Records: &records.Store{DB: db, Zones: []string{"example.test", "other.test"}}}
	e := echo.New()
	capture := &recordPageCapture{}
	e.Renderer = capture
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 3}, {"?zone=example.test", 2}, {"?zone=other.test", 1}} {
		ctx := e.NewContext(httptest.NewRequest("GET", "/@/records"+tc.query, nil), httptest.NewRecorder())
		if err := h.ListRecords(ctx); err != nil {
			t.Fatal(err)
		}
		sets := capture.data["sets"].([]rrView)
		if len(sets) != tc.want {
			t.Fatalf("%s: %#v", tc.query, sets)
		}
		if tc.query == "" {
			found := false
			for _, set := range sets {
				if set.Type == "CNAME" {
					found = true
					if set.Name != "alias" || set.Values != "router.example.test." || set.TTL != 120 || set.Zone != "example.test" {
						t.Fatal(set)
					}
				}
			}
			if !found {
				t.Fatal("missing existing CNAME")
			}
		}
	}
	var aliases int64
	db.Model(&model.CName{}).Count(&aliases)
	if aliases != 1 {
		t.Fatal("listing changed alias storage")
	}
	rec := httptest.NewRecorder()
	h.ListCNames(e.NewContext(httptest.NewRequest("GET", "/@/cnames", nil), rec))
	if rec.Code != 303 || rec.Header().Get("Location") != "/@/records" {
		t.Fatal(rec)
	}
}
