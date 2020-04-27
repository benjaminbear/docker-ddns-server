package handler

import (
	"fmt"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/go-playground/validator/v10"
	"github.com/jinzhu/gorm"
	"github.com/labstack/echo/v4"
	"github.com/tg123/go-htpasswd"
	"os"
	"strings"
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

func (cv *CustomValidator) Validate(i interface{}) error {
	return cv.Validator.Struct(i)
}

type Error struct {
	Message string `json:"message"`
}

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
