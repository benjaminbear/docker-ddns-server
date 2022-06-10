package webserver

import (
	"github.com/benjaminbear/docker-ddns-server/dyndns/config"
	"github.com/labstack/gommon/log"

	"strings"
	"time"

	"github.com/benjaminbear/docker-ddns-server/dyndns/db"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/tg123/go-htpasswd"
	"gorm.io/gorm"
)

type Handler struct {
	DB              *gorm.DB
	AuthAdmin       bool
	Config          *config.Config
	LastClearedLogs time.Time
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

func New(config *config.Config, db *gorm.DB) *Handler {
	h := &Handler{Config: config, DB: db}

	if config.AdminLogin == "" {
		h.AuthAdmin = true
	}

	return h
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

	host := &db.Host{}
	if err := h.DB.Where(&db.Host{UserName: username, Password: password, Hostname: reqArr[0], Domain: reqArr[1]}).First(host).Error; err != nil {
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

// Check if a log cleaning is needed
func (h *Handler) CheckClearInterval() {
	if h.Config.ClearLogInterval > 0 {
		if !equalDate(time.Now(), h.LastClearedLogs) {
			go h.ClearLogs()
		}
	}
}

// compare two dates
func equalDate(date1, date2 time.Time) bool {
	y1, m1, d1 := date1.Date()
	y2, m2, d2 := date2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}
