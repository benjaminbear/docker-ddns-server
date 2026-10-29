package certificates

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/benjaminbear/docker-ddns-server/dyndns/records"
	"github.com/go-acme/lego/v4/certificate"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{DB: db, Key: bytes.Repeat([]byte{7}, 32), Records: &records.Store{Zones: []string{"example.test"}}}
	if e = s.Init(); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestKeyPersistenceAndBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	key, e := LoadKey(path, "")
	if e != nil {
		t.Fatal(e)
	}
	again, e := LoadKey(path, "")
	if e != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	enc, e := seal(key, []byte("private"), "certificate:1")
	if e != nil {
		t.Fatal(e)
	}
	dec, e := unseal(key, enc, "certificate:1")
	if e != nil || string(dec) != "private" {
		t.Fatal(e)
	}
	if _, e = unseal(key, enc, "certificate:2"); e == nil {
		t.Fatal("object substitution accepted")
	}
	enc[len(enc)-1] ^= 1
	if _, e = unseal(key, enc, "certificate:1"); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = LoadKey(path, "short"); e == nil {
		t.Fatal("weak configured key")
	}
}
func fixture(t *testing.T) *certificate.Resource {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "example.test"}, DNSNames: []string{"example.test", "*.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := x509.MarshalPKCS8PrivateKey(key)
	return &certificate.Resource{Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})}
}
func TestSaveAndDownload(t *testing.T) {
	s := testService(t)
	c := &Certificate{Domains: "example.test,*.example.test", KeyType: "ec256"}
	s.DB.Create(c)
	r := fixture(t)
	if e := s.saveResource(c, r); e != nil {
		t.Fatal(e)
	}
	s.DB.First(c, c.ID)
	bundle, e := s.Download(c, "package.zip")
	if e != nil {
		t.Fatal(e)
	}
	archive, e := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if e != nil || len(archive.File) != 4 {
		t.Fatal("invalid ZIP", e)
	}
	for _, file := range archive.File {
		f, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(f)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		want, e := s.Download(c, file.Name)
		if e != nil || !bytes.Equal(data, want) {
			t.Fatal("wrong ZIP member", file.Name)
		}
	}
	if bytes.Contains(c.KeyPEMEnc, []byte("PRIVATE KEY")) {
		t.Fatal("plaintext stored")
	}
	key, e := s.Download(c, "privkey.pem")
	if e != nil || !bytes.Equal(key, r.PrivateKey) {
		t.Fatal("key download", e)
	}
	if _, e = s.Download(c, "../../database/ddns.db"); e == nil {
		t.Fatal("arbitrary download")
	}
	for _, part := range []string{"cert.pem", "chain.pem", "fullchain.pem"} {
		if _, e = s.Download(c, part); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.Validate("*.example.test,example.test", "admin@example.test", "rsa2048"); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"example.test.evil.net", "*.evil.net", "_svc.example.test", "*.*.example.test"} {
		if _, e = s.Validate(name, "admin@example.test", "ec256"); e == nil {
			t.Fatal("unauthorized name", name)
		}
	}
}
func TestOverlappingChallenges(t *testing.T) {
	t.Setenv("LEGO_DISABLE_CNAME_SUPPORT", "true")
	s := testService(t)
	values := map[string]bool{"existing": true}
	p := &Provider{Store: s.Records, Change: func(z, n, v string, add bool) error {
		if z != "example.test" || n != "_acme-challenge" {
			t.Fatal(z, n)
		}
		if add {
			values[v] = true
		} else {
			delete(values, v)
		}
		return nil
	}}
	if e := p.Present("example.test", "", "one"); e != nil {
		t.Fatal(e)
	}
	if e := p.Present("*.example.test", "", "two"); e != nil {
		t.Fatal(e)
	}
	if len(values) != 3 {
		t.Fatal(values)
	}
	p.CleanUp("example.test", "", "one")
	if len(values) != 2 {
		t.Fatal(values)
	}
	p.CleanupAll()
	if len(values) != 1 || !values["existing"] {
		t.Fatal("unrelated TXT lost")
	}
}
func TestRestartRecovery(t *testing.T) {
	s := testService(t)
	c := Certificate{Status: "pending"}
	s.DB.Create(&c)
	if e := s.Init(); e != nil {
		t.Fatal(e)
	}
	s.DB.First(&c, c.ID)
	if c.Status != "failed" || c.NextAttempt.IsZero() {
		t.Fatal(c)
	}
}

func TestAutomationAndRenewal(t *testing.T) {
	s := testService(t)
	if e := s.SaveAutomation(&Automation{Enabled: true, Scope: "zones", Email: "admin@example.test", KeyType: "ec256", Directory: Staging}); e != nil {
		t.Fatal(e)
	}
	r := fixture(t)
	issued := make(chan bool, 4)
	s.Issue = func(c *Certificate) (*certificate.Resource, error) { issued <- true; return r, nil }
	s.Tick(time.Now())
	select {
	case <-issued:
	case <-time.After(time.Second):
		t.Fatal("automatic issue not scheduled")
	}
	// Wait for asynchronous persistence before simulating the renewal threshold.
	for i := 0; i < 100; i++ {
		if s.busy.TryLock() {
			s.busy.Unlock()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var certs []Certificate
	s.DB.Find(&certs)
	if len(certs) != 1 || certs[0].Status != "valid" {
		t.Fatal(certs)
	}
	s.Tick(time.Now())
	select {
	case <-issued:
		t.Fatal("duplicate issuance")
	default:
	}
	s.DB.Model(&certs[0]).Updates(map[string]interface{}{"not_after": time.Now().Add(10 * 24 * time.Hour), "renew_at": time.Now().Add(-time.Hour), "next_attempt": time.Now().Add(-time.Hour)})
	s.Tick(time.Now())
	select {
	case <-issued:
	case <-time.After(time.Second):
		t.Fatal("renewal not scheduled")
	}
	for i := 0; i < 100; i++ {
		if s.busy.TryLock() {
			s.busy.Unlock()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHostDownloadNeverDistributesWildcardKey(t *testing.T) {
	s := testService(t)
	c := &Certificate{Domains: "example.test,*.example.test", Directory: Production}
	s.DB.Create(c)
	if e := s.saveResource(c, fixture(t)); e != nil {
		t.Fatal(e)
	}
	for _, host := range []string{"router.example.test", "example.test"} {
		if _, e := s.ForHost(host); e == nil {
			t.Fatal("zone wildcard key exposed on host link")
		}
	}
	// A dedicated certificate has exactly one SAN matching the host.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(99), DNSNames: []string{"router.example.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := x509.MarshalPKCS8PrivateKey(key)
	c = &Certificate{Domains: "router.example.test", Directory: Production}
	s.DB.Create(c)
	if e = s.saveResource(c, &certificate.Resource{Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), PrivateKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})}); e != nil {
		t.Fatal(e)
	}
	got, e := s.ForHost("router.example.test")
	if e != nil || got.ID != c.ID {
		t.Fatal("dedicated certificate unavailable", e)
	}
	if _, e = s.ForHost("other.example.test"); e == nil {
		t.Fatal("wrong host received certificate")
	}
}
