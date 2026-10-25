package certificates

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/miekg/dns"
	"net"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"github.com/w3K-one/docker-ddns-server/dyndns/nswrapper"
	"github.com/w3K-one/docker-ddns-server/dyndns/records"
	"gorm.io/gorm"
)

const Production = "https://acme-v02.api.letsencrypt.org/directory"
const Staging = "https://acme-staging-v02.api.letsencrypt.org/directory"

type Certificate struct {
	RenewAt                                               time.Time
	ID                                                    uint `gorm:"primaryKey"`
	Domains, Email, Directory, KeyType, Status, LastError string
	CertPEM, ChainPEM, KeyPEMEnc                          []byte `json:"-"`
	NotAfter                                              time.Time
	AutoRenew                                             bool
	NextAttempt                                           time.Time
	Failures                                              int
	CreatedAt, UpdatedAt                                  time.Time
}
type Account struct {
	ID           uint   `gorm:"primaryKey"`
	Email        string `gorm:"uniqueIndex:account_identity"`
	Directory    string `gorm:"uniqueIndex:account_identity"`
	KeyPEMEnc    []byte
	Registration []byte
}
type Event struct {
	ID             uint `gorm:"primaryKey"`
	CertificateID  uint
	Action, Result string
	CreatedAt      time.Time
}
type Service struct {
	DB            *gorm.DB
	Records       *records.Store
	Key           []byte
	busy          sync.Mutex
	TestDirectory string
	Issue         func(*Certificate) (*certificate.Resource, error)
}
type user struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (u *user) GetEmail() string                        { return u.email }
func (u *user) GetPrivateKey() crypto.PrivateKey        { return u.key }
func (u *user) GetRegistration() *registration.Resource { return u.registration }
func (s *Service) Init() error {
	if err := s.DB.AutoMigrate(&Certificate{}, &Account{}, &Event{}, &Automation{}, &Challenge{}); err != nil {
		return err
	}
	return s.DB.Model(&Certificate{}).Where("status IN ?", []string{"pending", "renewing"}).Updates(map[string]interface{}{"status": "failed", "last_error": "Issuance interrupted by server restart. Retry manually.", "next_attempt": time.Now().Add(time.Hour)}).Error
}
func (s *Service) Validate(domains, email, keyType string) (string, error) {
	if a, e := mail.ParseAddress(email); e != nil || a.Address != email {
		return "", fmt.Errorf("enter a valid contact email")
	}
	if keyType != "rsa2048" && keyType != "ec256" {
		return "", fmt.Errorf("unsupported key type")
	}
	names := strings.FieldsFunc(strings.ToLower(domains), func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' })
	if len(names) == 0 || len(names) > 20 {
		return "", fmt.Errorf("specify 1–20 names")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		name = strings.TrimSuffix(name, ".")
		base := strings.TrimPrefix(name, "*.")
		if !nswrapper.ValidName(base, false) || strings.ContainsAny(base, "_*") {
			return "", fmt.Errorf("invalid certificate name")
		}
		if _, _, e := s.Records.Resolve(base); e != nil {
			return "", e
		}
		if !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	return strings.Join(out, ","), nil
}
func (s *Service) Directory(staging bool) string {
	if s.TestDirectory != "" {
		return s.TestDirectory
	}
	if staging {
		return Staging
	}
	return Production
}
func (s *Service) Start(c *Certificate) error {
	if !s.busy.TryLock() {
		return fmt.Errorf("another certificate operation is running")
	}
	if c.ID == 0 {
		c.Status = "pending"
		if e := s.DB.Create(c).Error; e != nil {
			s.busy.Unlock()
			return e
		}
	} else {
		c.Status = "renewing"
		if e := s.DB.Model(c).Updates(map[string]interface{}{"status": c.Status, "last_error": ""}).Error; e != nil {
			s.busy.Unlock()
			return e
		}
	}
	copy := *c
	go func() { defer s.busy.Unlock(); s.issue(&copy) }()
	return nil
}
func (s *Service) issue(c *Certificate) {
	issue := s.Issue
	if issue == nil {
		issue = s.obtain
	}
	resource, err := issue(c)
	if err == nil {
		err = s.saveResource(c, resource)
	}
	if err != nil {
		c.Failures++
		delay := time.Hour * time.Duration(1<<min(c.Failures-1, 5))
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		// Provider errors are kept out of pages/logs: they can include DNS tokens or CA payloads.
		s.DB.Model(c).Updates(map[string]interface{}{"status": "failed", "last_error": certificateFailureMessage(err), "failures": c.Failures, "next_attempt": time.Now().Add(delay)})
		s.Audit(c.ID, "issue", "failed")
		return
	}
	s.Audit(c.ID, "issue", "ok")
}
func (s *Service) saveResource(c *Certificate, r *certificate.Resource) error {
	if r == nil {
		return fmt.Errorf("empty certificate response")
	}
	if _, err := tls.X509KeyPair(r.Certificate, r.PrivateKey); err != nil {
		return err
	}
	block, rest := pem.Decode(r.Certificate)
	if block == nil || block.Type != "CERTIFICATE" {
		return fmt.Errorf("invalid certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(c.Domains, ",") {
		if strings.HasPrefix(name, "*.") {
			name = "validation." + name[2:]
		}
		if err := parsed.VerifyHostname(name); err != nil {
			return err
		}
	}
	chain := rest
	if len(chain) == 0 {
		chain = r.IssuerCertificate
	}
	encrypted, err := seal(s.Key, r.PrivateKey, fmt.Sprintf("certificate:%d", c.ID))
	if err != nil {
		return err
	}
	renewAt := parsed.NotAfter.Add(-30 * 24 * time.Hour)
	adaptive := parsed.NotBefore.Add(parsed.NotAfter.Sub(parsed.NotBefore) * 2 / 3)
	if adaptive.After(renewAt) {
		renewAt = adaptive
	}
	return s.DB.Model(c).Updates(map[string]interface{}{"cert_pem": pem.EncodeToMemory(block), "chain_pem": chain, "key_pem_enc": encrypted, "not_after": parsed.NotAfter, "renew_at": renewAt, "status": "valid", "last_error": "", "failures": 0, "next_attempt": time.Now().Add(time.Hour)}).Error
}
func (s *Service) client(c *Certificate) (*lego.Client, error) {
	var account Account
	err := s.DB.Where("email = ? AND directory = ?", c.Email, c.Directory).First(&account).Error
	var key crypto.PrivateKey
	identity := "account:" + c.Email + ":" + c.Directory
	if errors.Is(err, gorm.ErrRecordNotFound) {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		raw, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return nil, e
		}
		encrypted, e := seal(s.Key, raw, identity)
		if e != nil {
			return nil, e
		}
		account = Account{Email: c.Email, Directory: c.Directory, KeyPEMEnc: encrypted}
		if e = s.DB.Create(&account).Error; e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	} else {
		raw, e := unseal(s.Key, account.KeyPEMEnc, identity)
		if e != nil {
			return nil, e
		}
		key, e = x509.ParsePKCS8PrivateKey(raw)
		if e != nil {
			return nil, e
		}
	}
	u := &user{email: c.Email, key: key}
	if len(account.Registration) > 0 {
		if err = json.Unmarshal(account.Registration, &u.registration); err != nil {
			return nil, err
		}
	}
	config := lego.NewConfig(u)
	config.CADirURL = c.Directory
	config.Certificate.Timeout = 2 * time.Minute
	config.Certificate.KeyType = certcrypto.RSA2048
	if c.KeyType == "ec256" {
		config.Certificate.KeyType = certcrypto.EC256
	}
	client, err := lego.NewClient(config)
	if err != nil {
		return nil, err
	}
	if u.registration == nil {
		reg, e := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if e != nil {
			return nil, e
		}
		u.registration = reg
		raw, e := json.Marshal(reg)
		if e != nil {
			return nil, e
		}
		if e = s.DB.Model(&account).Update("registration", raw).Error; e != nil {
			return nil, e
		}
	}
	return client, nil
}
func (s *Service) obtain(c *Certificate) (*certificate.Resource, error) {
	client, err := s.client(c)
	if err != nil {
		return nil, err
	}
	provider := &Provider{Store: s.Records, DB: s.DB}
	defer provider.CleanupAll()
	options := []dns01.ChallengeOption{}
	{
		ns := authoritativeNameserver()
		if _, _, e := net.SplitHostPort(ns); e != nil {
			ns = net.JoinHostPort(ns, "53")
		}
		options = append(options, dns01.WrapPreCheck(func(domain, fqdn, value string, check dns01.PreCheckFunc) (bool, error) {
			message := new(dns.Msg)
			message.SetQuestion(fqdn, dns.TypeTXT)
			response, _, e := (&dns.Client{Timeout: 5 * time.Second}).Exchange(message, ns)
			if e != nil {
				return false, e
			}
			if !response.Authoritative {
				return false, fmt.Errorf("configured DNS server is not authoritative")
			}
			for _, answer := range response.Answer {
				if txt, ok := answer.(*dns.TXT); ok && strings.Join(txt.Txt, "") == value {
					return true, nil
				}
			}
			return false, nil
		}))
	}
	if ns := os.Getenv("DDNS_ACME_RESOLVERS"); ns != "" {
		options = append(options, dns01.AddRecursiveNameservers(strings.Split(ns, ",")))
	}
	if err = client.Challenge.SetDNS01Provider(provider, options...); err != nil {
		return nil, err
	}
	return client.Certificate.Obtain(certificate.ObtainRequest{Domains: strings.Split(c.Domains, ","), Bundle: true})
}
func (s *Service) Revoke(c *Certificate) error {
	if !s.busy.TryLock() {
		return fmt.Errorf("another certificate operation is running")
	}
	defer s.busy.Unlock()
	if len(c.CertPEM) == 0 || c.Status == "revoked" {
		return fmt.Errorf("no active certificate")
	}
	client, e := s.client(c)
	if e != nil {
		return e
	}
	if e = client.Certificate.Revoke(c.CertPEM); e != nil {
		return fmt.Errorf("CA rejected revocation")
	}
	if e = s.DB.Model(c).Updates(map[string]interface{}{"status": "revoked", "auto_renew": false}).Error; e != nil {
		return e
	}
	s.Audit(c.ID, "revoke", "ok")
	return nil
}
func (s *Service) Download(c *Certificate, part string) ([]byte, error) {
	if len(c.CertPEM) == 0 {
		return nil, fmt.Errorf("certificate is not issued")
	}
	switch part {
	case "package.zip":
		var out bytes.Buffer
		archive := zip.NewWriter(&out)
		for _, part := range []string{"cert.pem", "chain.pem", "fullchain.pem", "privkey.pem"} {
			data, e := s.Download(c, part)
			if e != nil {
				return nil, e
			}
			header := &zip.FileHeader{Name: part, Method: zip.Deflate}
			header.SetMode(0600)
			file, e := archive.CreateHeader(header)
			if e != nil {
				return nil, e
			}
			if _, e = file.Write(data); e != nil {
				return nil, e
			}
		}
		if e := archive.Close(); e != nil {
			return nil, e
		}
		return out.Bytes(), nil

	case "cert.pem":
		return c.CertPEM, nil
	case "chain.pem":
		return c.ChainPEM, nil
	case "fullchain.pem":
		return append(append([]byte{}, c.CertPEM...), c.ChainPEM...), nil
	case "privkey.pem":
		return unseal(s.Key, c.KeyPEMEnc, fmt.Sprintf("certificate:%d", c.ID))
	default:
		return nil, fmt.Errorf("unknown certificate part")
	}
}
func (s *Service) Audit(id uint, action, result string) {
	s.DB.Create(&Event{CertificateID: id, Action: action, Result: result})
}
func (s *Service) RenewDue(now time.Time) {
	var certs []Certificate
	if s.DB.Where("auto_renew = ? AND status IN ? AND next_attempt <= ? AND renew_at <= ?", true, []string{"valid", "failed"}, now, now).Find(&certs).Error != nil {
		return
	}
	for i := range certs {
		if !s.ManagedCertificate(&certs[i]) {
			continue
		}
		if s.Start(&certs[i]) == nil {
			return
		}
	}
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.Tick(now)
		}
	}
}

// ForHost returns only a dedicated single-host certificate, never a zone or wildcard key.
func (s *Service) ForHost(host string) (*Certificate, error) {
	var certs []Certificate
	if err := s.DB.Where("not_after > ? AND status IN ? AND directory = ?", time.Now(), []string{"valid", "renewing", "failed"}, Production).Order("updated_at desc,id desc").Find(&certs).Error; err != nil {
		return nil, err
	}
	for i := range certs {
		c := &certs[i]
		block, _ := pem.Decode(c.CertPEM)
		if block == nil {
			continue
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err == nil && len(leaf.DNSNames) == 1 && leaf.DNSNames[0] == host && leaf.VerifyHostname(host) == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no dedicated certificate for host")
}
