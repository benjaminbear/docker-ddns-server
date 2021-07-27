package main

import (
	"net/http"

	"github.com/benjaminbear/docker-ddns-server/dyndns/handler"
	"github.com/foolin/goview/supports/echoview-v4"
	"github.com/go-playground/validator/v10"

	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	e := echo.New()

	e.Logger.SetLevel(log.ERROR)

	e.Use(middleware.Logger())

	// Set Renderer
	e.Renderer = echoview.Default()

	// Set Validator
	e.Validator = &handler.CustomValidator{Validator: validator.New()}

	// Set Statics
	e.Static("/static", "static")

	// Initialize handler
	h := &handler.Handler{}

	// Database connection
	if err := h.InitDB(); err != nil {
		e.Logger.Fatal(err)
	}
	defer h.DB.Close()

	if err := h.ParseEnvs(); err != nil {
		e.Logger.Fatal(err)
	}

	e.Use(middleware.BasicAuth(h.Authenticate))

	// UI Routes
	e.GET("/", func(c echo.Context) error {
		//render with master
		return c.Render(http.StatusOK, "index", nil)
	})

	e.GET("/hosts/add", h.AddHost)
	e.GET("/hosts/edit/:id", h.EditHost)
	e.GET("/hosts", h.ListHosts)
	e.GET("/logs", h.ShowLogs)
	e.GET("/logs/host/:id", h.ShowHostLogs)

	// Rest Routes
	e.POST("/hosts/add", h.CreateHost)
	e.POST("/hosts/edit/:id", h.UpdateHost)
	e.GET("/hosts/delete/:id", h.DeleteHost)

	// dyndns compatible api
	e.GET("/update", h.UpdateIP)
	e.GET("/nic/update", h.UpdateIP)
	e.GET("/v2/update", h.UpdateIP)
	e.GET("/v3/update", h.UpdateIP)

	// Start server
	e.Logger.Fatal(e.Start(":8080"))
}
