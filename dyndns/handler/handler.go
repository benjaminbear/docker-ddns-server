package handler

import (
	"fmt"

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
	DB               *gorm.DB
	AuthAdmin        bool
	Config           Envs
	Title            string
	DisableAdminAuth bool
	LastClearedLogs  time.Time
	ClearInterval    uint64
	AllowWildcard    bool
	LogoutUrl        string
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
	reqParameter := c.QueryParam("hostname")
	reqArr := strings.SplitN(reqParameter, ".", 2)
	if len(reqArr) != 2 {
		log.Error("Error: Something wrong with the hostname parameter")
		return false, nil
	}

	host := &model.Host{}
	if err := h.DB.Where(&model.Host{UserName: username, Password: password, Hostname: reqArr[0], Domain: reqArr[1]}).First(host).Error; err != nil {
		log.Error("Error: ", err)
		return false, nil
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
		h.Title = "TheBBCloud DynDNS"
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
	if len(h.Config.Domains) < 1 {
		return adminAuth, fmt.Errorf("environment variable DDNS_DOMAINS has to be set")
	}

	return adminAuth, nil
}

// InitDB creates an empty database and creates all tables if there isn't already one, or opens the existing one.
func (h *Handler) InitDB() (err error) {
	if _, err := os.Stat("database"); os.IsNotExist(err) {
		err = os.MkdirAll("database", os.ModePerm)
		if err != nil {
			return err
		}
	}

	h.DB, err = gorm.Open(sqlite.Open("database/ddns.db"), &gorm.Config{})
	if err != nil {
		return err
	}

	err = h.DB.AutoMigrate(&model.Host{}, &model.CName{}, &model.Log{})

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
