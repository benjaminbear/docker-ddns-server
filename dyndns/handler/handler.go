package handler

import (
	"errors"
	"fmt"
	"github.com/benjaminbear/docker-ddns-server/dyndns/certificates"
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"github.com/benjaminbear/docker-ddns-server/dyndns/records"
	"gorm.io/gorm/logger"
	"sync"

	"github.com/labstack/gommon/log"

	"os"
	"strconv"
	"strings"
	"time"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/tg123/go-htpasswd"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Handler struct {
	proxyMu          sync.Mutex
	proxyError       string
	AdminAuth        *AdminAuth
	DB               *gorm.DB
	Records          *records.Store
	Certificates     *certificates.Service
	TrustedProxies   nswrapper.TrustedProxies
	deleteRecords    func([]string, string, bool) error
	AuthAdmin        bool
	Config           Envs
	Title            string
	DisableAdminAuth bool
	LastClearedLogs  time.Time
	ClearInterval    uint64
	AllowWildcard    bool
	LogoutUrl        string
	LogoPath         string
	SessionStore     *SessionStore
	PoweredBy        string
	PoweredByUrl     string
}

type Envs struct {
	AdminLogin string
	Domains    []string
}

type CustomValidator struct {
	Validator *validator.Validate
}

// Validate implements the Validator.
func (cv *CustomValidator) Validate(i interface{}) error {
	return cv.Validator.Struct(i)
}

type Error struct {
	Message string `json:"message"`
}

// Authenticate is the method the website admin user and the host update user have to authenticate against.
// To gather admin rights the username password combination must match with the credentials given by the env var.
func (h *Handler) AuthenticateUpdate(username, password string, c echo.Context) (bool, error) {
	h.CheckClearInterval()
	if username == "" || password == "" {
		return false, nil
	}
	reqParameter := strings.ToLower(c.QueryParam("hostname"))
	reqArr := strings.SplitN(reqParameter, ".", 2)
	if len(reqArr) != 2 || reqArr[0] == "" || reqArr[1] == "" {
		log.Error("Error: Something wrong with the hostname parameter")
		return false, nil
	}

	lowerUsername := strings.ToLower(username)
	host := &model.Host{}
	if err := h.DB.Where("user_name = ? AND password = ? AND hostname = ? AND domain = ?", lowerUsername, password, reqArr[0], reqArr[1]).First(host).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if host.ID == 0 {
		log.Error("hostname or user user credentials unknown")
		return false, nil
	}
	c.Set("updateHost", host)

	return true, nil
}
func (h *Handler) AuthenticateAdmin(username, password string, c echo.Context) (bool, error) {
	h.AuthAdmin = false
	ok, err := h.authByEnv(username, password)
	if err != nil {
		log.Error("Error:", err)
		return false, nil
	}

	if ok {
		h.AuthAdmin = true
		return true, nil
	}

	return false, nil
}
func (h *Handler) authByEnv(username, password string) (bool, error) {
	hashReader := strings.NewReader(h.Config.AdminLogin)

	pw, err := htpasswd.NewFromReader(hashReader, htpasswd.DefaultSystems, nil)
	if err != nil {
		return false, err
	}

	if ok := pw.Match(username, password); ok {
		return true, nil
	}

	return false, nil
}

// ParseEnvs parses all needed environment variables:
// DDNS_ADMIN_LOGIN: The basic auth login string in htpasswd style.
// DDNS_DOMAINS: All domains that will be handled by the dyndns server.
func (h *Handler) ParseEnvs() (adminAuth bool, err error) {
	log.Info("Read environment variables")
	h.Config = Envs{}
	adminAuth = true
	h.Config.AdminLogin = os.Getenv("DDNS_ADMIN_LOGIN")
	if h.Config.AdminLogin == "" {
		log.Info("No Auth! DDNS_ADMIN_LOGIN should be set")
		adminAuth = false
		h.AuthAdmin = true
		h.DisableAdminAuth = true
	}
	var ok bool
	h.Title, ok = os.LookupEnv("DDNS_TITLE")
	if !ok {
		h.Title = "Dynamic DNS"
	}

	// ADDED: Check for logo files in the static icons directory upon startup.
	logoExtensions := []string{"png", "webp", "svg"}
	for _, ext := range logoExtensions {
		path := fmt.Sprintf("static/icons/logo.%s", ext)
		if _, err := os.Stat(path); err == nil {
			h.LogoPath = "/" + path // Store the valid path if found
			log.Info("Found logo at: ", h.LogoPath)
			break
		}
	}

	allowWildcard, ok := os.LookupEnv("DDNS_ALLOW_WILDCARD")
	if ok {
		h.AllowWildcard, err = strconv.ParseBool(allowWildcard)
		if err == nil {
			log.Info("Wildcard allowed")
		}
	}
	logoutUrl, ok := os.LookupEnv("DDNS_LOGOUT_URL")
	if ok {
		if len(logoutUrl) > 0 {
			log.Info("Logout url set: ", logoutUrl)
			h.LogoutUrl = logoutUrl
		}
	}

	h.PoweredBy, ok = os.LookupEnv("DDNS_POWERED_BY")
	if !ok || h.PoweredBy == "" {
		h.PoweredBy = "Docker DDNS Server"
	} else {
		log.Info("Powered by set: ", h.PoweredBy)
	}

	h.PoweredByUrl, ok = os.LookupEnv("DDNS_POWERED_BY_URL")
	if !ok || h.PoweredByUrl == "" {
		h.PoweredByUrl = "https://github.com/benjaminbear/docker-ddns-server"
	} else {
		log.Info("Powered by URL set: ", h.PoweredByUrl)
	}

	clearEnv := os.Getenv("DDNS_CLEAR_LOG_INTERVAL")
	clearInterval, err := strconv.ParseUint(clearEnv, 10, 32)
	if err != nil {
		log.Info("No log clear interval found")
	} else {
		log.Info("log clear interval found: ", clearInterval, "days")
		h.ClearInterval = clearInterval
		if clearInterval > 0 {
			h.LastClearedLogs = time.Now()
		}
	}

	h.Config.Domains = strings.Split(os.Getenv("DDNS_DOMAINS"), ",")
	if strings.TrimSpace(os.Getenv("DDNS_DOMAINS")) == "" {
		return adminAuth, fmt.Errorf("environment variable DDNS_DOMAINS has to be set")
	}

	for i, domain := range h.Config.Domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if !nswrapper.ValidName(domain, false) {
			return adminAuth, fmt.Errorf("invalid managed domain")
		}
		h.Config.Domains[i] = domain
	}

	h.TrustedProxies, err = nswrapper.ParseTrustedProxies(os.Getenv("DDNS_TRUSTED_PROXIES"))
	if err != nil {
		return adminAuth, err
	}

	// Initialize session store
	if err := h.InitSessionStore(); err != nil {
		return adminAuth, fmt.Errorf("failed to initialize session store: %v", err)
	}

	return adminAuth, nil
}

// InitDB creates an empty database and creates all tables if there isn't already one, or opens the existing one.
func (h *Handler) InitDB() (err error) {
	if _, err := os.Stat("database"); os.IsNotExist(err) {
		err = os.MkdirAll("database", 0700)
		if err != nil {
			return err
		}
	}

	h.DB, err = gorm.Open(sqlite.Open("database/ddns.db?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}

	os.Chmod("database/ddns.db", 0600)
	err = migrateDatabase(h.DB)

	return err
}

// Check if a log cleaning is needed
func (h *Handler) CheckClearInterval() {
	if !h.LastClearedLogs.IsZero() {
		if !DateEqual(time.Now(), h.LastClearedLogs) {
			go h.ClearLogs()
		}
	}
}

// compare two dates
func DateEqual(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
