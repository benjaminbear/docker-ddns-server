package handler

import (
	"fmt"
	"os"
	"strings"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/go-playground/validator/v10"
	"github.com/jinzhu/gorm"
	"github.com/labstack/echo/v4"
	"github.com/tg123/go-htpasswd"
)

type Handler struct {
	DB        *gorm.DB
	AuthHost  *model.Host
	AuthAdmin bool
	Config    Envs
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
func (h *Handler) Authenticate(username, password string, c echo.Context) (bool, error) {
	h.AuthHost = nil
	h.AuthAdmin = false

	ok, err := h.authByEnv(username, password)
	if err != nil {
		fmt.Println("Error:", err)
		return false, nil
	}

	if ok {
		h.AuthAdmin = true
		return true, nil
	}

	host := &model.Host{}
	if err := h.DB.Where(&model.Host{UserName: username, Password: password}).First(host).Error; err != nil {
		fmt.Println("Error:", err)
		return false, nil
	}

	h.AuthHost = host

	return true, nil
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
func (h *Handler) ParseEnvs() error {
	h.Config = Envs{}
	h.Config.AdminLogin = os.Getenv("DDNS_ADMIN_LOGIN")
	if h.Config.AdminLogin == "" {
		return fmt.Errorf("environment variable DDNS_ADMIN_LOGIN has to be set")
	}

	h.Config.Domains = strings.Split(os.Getenv("DDNS_DOMAINS"), ",")
	if len(h.Config.Domains) < 1 {
		return fmt.Errorf("environment variable DDNS_DOMAINS has to be set")
	}

	return nil
}

// InitDB creates an empty database and creates all tables if there isn't already one, or opens the existing one.
func (h *Handler) InitDB() (err error) {
	if _, err := os.Stat("database"); os.IsNotExist(err) {
		err = os.MkdirAll("database", os.ModePerm)
		if err != nil {
			return err
		}
	}

	h.DB, err = gorm.Open("sqlite3", "database/ddns.db")
	if err != nil {
		return err
	}

	if !h.DB.HasTable(&model.Host{}) {
		h.DB.CreateTable(&model.Host{})
	}

	if !h.DB.HasTable(&model.Log{}) {
		h.DB.CreateTable(&model.Log{})
	}

	return nil
}
