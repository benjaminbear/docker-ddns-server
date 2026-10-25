package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/w3K-one/docker-ddns-server/dyndns/certificates"
	"github.com/w3K-one/docker-ddns-server/dyndns/model"
	"github.com/w3K-one/docker-ddns-server/dyndns/nswrapper"
	"github.com/w3K-one/docker-ddns-server/dyndns/records"
)

func (h *Handler) InitFeatures() error {
	h.Records = &records.Store{DB: h.DB, Zones: h.Config.Domains}
	// Certificate setup failure does not prevent DNS or the existing admin panel starting.
	if os.Getenv("DDNS_CERT_KEY") == "" {
		if _, err := os.Stat("database/cert.key"); os.IsNotExist(err) && h.DB.Migrator().HasTable(&certificates.Account{}) {
			var count int64
			if err = h.DB.Model(&certificates.Account{}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("restore database/cert.key from backup before using stored certificates")
			}
		}
	}
	key, e := certificates.LoadKey("database/cert.key", os.Getenv("DDNS_CERT_KEY"))
	if e != nil {
		return e
	}
	s := &certificates.Service{DB: h.DB, Records: h.Records, Key: key, TestDirectory: os.Getenv("DDNS_ACME_DIRECTORY")}
	if e = s.Init(); e != nil {
		return e
	}
	h.Certificates = s
	go s.Run(context.Background())
	return nil
}
func (h *Handler) FeatureRoutes(g *echo.Group) {
	// New administrative operations always require configured admin authentication.
	g.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if h.DisableAdminAuth {
				return echo.NewHTTPError(403, "Configure admin authentication to use records and certificates")
			}
			return h.SessionAuthMiddleware()(next)(c)
		}
	})
	g.Use(middleware.BodyLimit("1M"))
	g.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{TokenLookup: "form:csrf", CookiePath: "/@", CookieHTTPOnly: true, CookieSameSite: http.SameSiteStrictMode}))
	g.GET("/records", h.ListRecords)
	g.POST("/records", h.SaveRecords)
	g.GET("/certs", h.ListCertificates)
	g.GET("/certs/status", h.CertificateStatus)
	g.POST("/certs", h.NewCertificate)
	g.POST("/certs/automation", h.SaveCertificateAutomation)
	g.POST("/certs/:id/renew", h.RenewCertificate)
	g.POST("/certs/:id/revoke", h.RevokeCertificate)
	g.GET("/certs/:id/download/:part", h.DownloadCertificate)
	g.GET("/hosts/:id/certificate.zip", h.HostCertificatePackage)
}
func (h *Handler) page(c echo.Context) echo.Map {
	return echo.Map{"title": h.Title, "logoPath": h.LogoPath, "poweredBy": h.PoweredBy, "poweredByUrl": h.PoweredByUrl, "csrf": c.Get("csrf"), "zones": h.Config.Domains}
}

type rrView struct {
	Name, Type string
	Zone       string
	TTL        int
	Values     string
}

func (h *Handler) ListRecords(c echo.Context) error {
	zone := c.QueryParam("zone")
	if zone == "" {
		zone = "all"
	}
	if zone != "all" && !h.Records.Zone(zone) {
		return echo.NewHTTPError(400, "Unknown zone")
	}
	var rows []model.Record
	query := h.DB.Where("domain IN ?", h.Config.Domains)
	if zone != "all" {
		query = query.Where("domain = ?", zone)
	}
	if e := query.Order("id ASC").Find(&rows).Error; e != nil {
		return e
	}
	sets := []rrView{}
	indexes := map[[3]string]int{}
	for _, r := range rows {
		key := [3]string{r.Domain, r.Name, r.Type}
		if index, exists := indexes[key]; exists {
			sets[index].Values += "\n" + r.Value
		} else {
			indexes[key] = len(sets)
			sets = append(sets, rrView{Name: r.Name, Type: r.Type, Zone: r.Domain, TTL: r.TTL, Values: r.Value})
		}
	}
	var aliases []model.CName
	aliasQuery := h.DB.Preload("Target").Joins("JOIN hosts ON hosts.id = c_names.target_id").Where("hosts.domain IN ? AND hosts.deleted_at IS NULL", h.Config.Domains)
	if zone != "all" {
		aliasQuery = aliasQuery.Where("hosts.domain = ?", zone)
	}
	if err := aliasQuery.Order("c_names.id ASC").Find(&aliases).Error; err != nil {
		return err
	}
	for _, alias := range aliases {
		sets = append(sets, rrView{Name: alias.Hostname, Type: "CNAME", Zone: alias.Target.Domain, TTL: alias.Ttl, Values: alias.Target.Hostname + "." + alias.Target.Domain + "."})
	}
	data := h.page(c)
	data["selectedType"] = "TXT"
	if c.QueryParam("type") == "CNAME" {
		data["selectedType"] = "CNAME"
	}
	data["zone"] = zone
	data["sets"] = sets
	data["types"] = nswrapper.Types
	return c.Render(200, "records", data)
}
func (h *Handler) SaveRecords(c echo.Context) error {
	zone := strings.ToLower(strings.TrimSpace(c.FormValue("zone")))
	name := strings.ToLower(strings.TrimSpace(c.FormValue("name")))
	kind := strings.ToUpper(c.FormValue("type"))
	ttl, e := strconv.Atoi(c.FormValue("ttl"))
	if e != nil {
		return echo.NewHTTPError(400, "Invalid TTL")
	}
	var values []string
	if c.FormValue("action") != "delete" {
		values = strings.Split(strings.ReplaceAll(c.FormValue("values"), "\r\n", "\n"), "\n")
	}
	if e = h.Records.Replace(zone, name, kind, ttl, values); e != nil {
		return echo.NewHTTPError(400, e.Error())
	}
	return c.Redirect(303, "/@/records?zone="+zone)
}
func (h *Handler) ListCertificates(c echo.Context) error {
	data := h.page(c)
	data["enabled"] = h.Certificates != nil
	data["prefill"] = c.QueryParam("domains")
	data["email"] = os.Getenv("DDNS_ACME_EMAIL")
	var certs []certificates.Certificate
	var events []certificates.Event
	if h.Certificates != nil {
		if e := h.DB.Order("id desc").Find(&certs).Error; e != nil {
			return e
		}
		if e := h.DB.Order("id desc").Limit(20).Find(&events).Error; e != nil {
			return e
		}
	}
	policy := certificates.Automation{Scope: "zones", KeyType: "rsa2048", Email: os.Getenv("DDNS_ACME_EMAIL")}
	if h.Certificates != nil {
		h.DB.First(&policy, 1)
	}
	data["automation"] = policy
	data["certs"] = certs
	data["events"] = events
	return c.Render(200, "certificates", data)
}
func (h *Handler) NewCertificate(c echo.Context) error {
	if h.Certificates == nil {
		return echo.NewHTTPError(503, "Certificate storage unavailable; check server configuration")
	}
	if c.FormValue("terms") != "yes" {
		return echo.NewHTTPError(400, "Accept the CA terms to issue a certificate")
	}
	email := strings.TrimSpace(c.FormValue("email"))
	keyType := c.FormValue("key_type")
	names, e := h.Certificates.Validate(c.FormValue("domains"), email, keyType)
	if e != nil {
		return echo.NewHTTPError(400, e.Error())
	}
	cert := &certificates.Certificate{Domains: names, Email: email, KeyType: keyType, Directory: h.Certificates.Directory(c.FormValue("staging") == "yes"), AutoRenew: c.FormValue("auto_renew") == "yes"}
	if e = h.Certificates.Start(cert); e != nil {
		return echo.NewHTTPError(409, e.Error())
	}
	return c.Redirect(303, "/@/certs")
}
func (h *Handler) certificate(c echo.Context) (*certificates.Certificate, error) {
	if h.Certificates == nil {
		return nil, echo.NewHTTPError(503, "Certificate storage unavailable")
	}
	id, e := strconv.ParseUint(c.Param("id"), 10, 32)
	if e != nil || id == 0 {
		return nil, echo.NewHTTPError(400, "Invalid certificate ID")
	}
	cert := &certificates.Certificate{}
	if e = h.DB.First(cert, id).Error; e != nil {
		return nil, echo.NewHTTPError(404, "Certificate not found")
	}
	return cert, nil
}
func (h *Handler) RenewCertificate(c echo.Context) error {
	cert, e := h.certificate(c)
	if e != nil {
		return e
	}
	if e = h.Certificates.Start(cert); e != nil {
		return echo.NewHTTPError(409, e.Error())
	}
	return c.Redirect(303, "/@/certs")
}
func (h *Handler) RevokeCertificate(c echo.Context) error {
	cert, e := h.certificate(c)
	if e != nil {
		return e
	}
	if e = h.Certificates.Revoke(cert); e != nil {
		return echo.NewHTTPError(400, "Revocation failed or another operation is running")
	}
	return c.Redirect(303, "/@/certs")
}
func (h *Handler) DownloadCertificate(c echo.Context) error {
	if !h.IsHTTPS(c) && os.Getenv("DDNS_ALLOW_INSECURE_DOWNLOAD") != "true" {
		return echo.NewHTTPError(403, "Certificate downloads require HTTPS; configure DDNS_TRUSTED_PROXIES for your reverse proxy")
	}
	cert, e := h.certificate(c)
	if e != nil {
		return e
	}
	part := c.Param("part")
	data, e := h.Certificates.Download(cert, part)
	if e != nil {
		return echo.NewHTTPError(400, "Certificate part unavailable")
	}
	h.Certificates.Audit(cert.ID, "download:"+part, "ok")
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-%s\"", strings.ReplaceAll(strings.Split(cert.Domains, ",")[0], "*", "wildcard"), part))
	contentType := "application/x-pem-file"
	if part == "package.zip" {
		contentType = "application/zip"
	}
	return c.Blob(200, contentType, data)
}

func (h *Handler) SaveCertificateAutomation(c echo.Context) error {
	if h.Certificates == nil {
		return echo.NewHTTPError(503, "Certificate storage unavailable")
	}
	enabled := c.FormValue("enabled") == "yes"
	if enabled && c.FormValue("terms") != "yes" {
		return echo.NewHTTPError(400, "Accept the CA subscriber agreement to enable automatic issuance")
	}
	policy := &certificates.Automation{Enabled: enabled, Scope: c.FormValue("scope"), Email: strings.TrimSpace(c.FormValue("email")), KeyType: c.FormValue("key_type"), Directory: h.Certificates.Directory(c.FormValue("staging") == "yes")}
	if err := h.Certificates.SaveAutomation(policy); err != nil {
		return echo.NewHTTPError(400, err.Error())
	}
	h.Certificates.Tick(time.Now())
	return c.Redirect(303, "/@/certs")
}

func (h *Handler) HostCertificatePackage(c echo.Context) error {
	if h.Certificates == nil {
		return echo.NewHTTPError(503, "Certificate storage unavailable")
	}
	id, e := strconv.ParseUint(c.Param("id"), 10, 32)
	if e != nil || id == 0 {
		return echo.NewHTTPError(400, "Invalid host ID")
	}
	var host model.Host
	if e = h.DB.First(&host, id).Error; e != nil {
		return echo.NewHTTPError(404, "Host not found")
	}
	cert, e := h.Certificates.ForHost(host.Hostname + "." + host.Domain)
	if e != nil {
		return echo.NewHTTPError(404, "No dedicated production certificate for this host; issue a host certificate from Certificates")
	}
	return c.Redirect(302, fmt.Sprintf("/@/certs/%d/download/package.zip", cert.ID))
}

// CertificateStatus exposes only public job metadata, never certificate material or keys.
func (h *Handler) CertificateStatus(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if h.Certificates == nil {
		return echo.NewHTTPError(503, "Certificate storage unavailable")
	}
	var rows []struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	if err := h.DB.Model(&certificates.Certificate{}).Select("id", "status").Order("id").Find(&rows).Error; err != nil {
		return err
	}
	return c.JSON(200, rows)
}
