package main

import (
	"github.com/benjaminbear/docker-ddns-server/dyndns/handler"
	"github.com/foolin/goview"
	"github.com/foolin/goview/supports/echoview-v4"
	"github.com/go-playground/validator/v10"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"html/template"
	"net/http"
	"time"
)

func main() {
	// Set new instance
	e := echo.New()

	e.Logger.SetLevel(log.ERROR)

	e.Use(middleware.Logger())

	// Set Renderer
	e.Renderer = echoview.New(goview.Config{
		Root:      "views",
		Master:    "layouts/master",
		Extension: ".html",
		Funcs: template.FuncMap{
			"year": func() string {
				return time.Now().Format("2006")
			},
		},
		DisableCache: true,
	})

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

	authAdmin, err := h.ParseEnvs()
	if err != nil {
		e.Logger.Fatal(err)
	}

	// UI Routes
	groupPublic := e.Group("/")
	groupPublic.GET("*", func(c echo.Context) error {
		//redirect to admin
		return c.Redirect(301, "/admin/")
	})
	groupAdmin := e.Group("/admin")
	if authAdmin {
		groupAdmin.Use(middleware.BasicAuth(h.AuthenticateAdmin))
	}

	groupAdmin.GET("/", h.ListHosts)
	groupAdmin.GET("/hosts/add", h.AddHost)
	groupAdmin.GET("/hosts/edit/:id", h.EditHost)
	groupAdmin.GET("/hosts", h.ListHosts)
	groupAdmin.GET("/cnames/add", h.AddCName)
	groupAdmin.GET("/cnames", h.ListCNames)
	groupAdmin.GET("/logs", h.ShowLogs)
	groupAdmin.GET("/logs/host/:id", h.ShowHostLogs)

	// Rest Routes
	groupAdmin.POST("/hosts/add", h.CreateHost)
	groupAdmin.POST("/hosts/edit/:id", h.UpdateHost)
	groupAdmin.GET("/hosts/delete/:id", h.DeleteHost)
	//redirect to logout
	groupAdmin.GET("/logout", func(c echo.Context) error {
		// either custom url
		if len(h.LogoutUrl) > 0 {
			return c.Redirect(302, h.LogoutUrl)
		}
		// or standard url
		return c.Redirect(302, "../")
	})
	groupAdmin.POST("/cnames/add", h.CreateCName)
	groupAdmin.GET("/cnames/delete/:id", h.DeleteCName)

	// dyndns compatible api
	// (avoid breaking changes and create groups for each update endpoint)
	updateRoute := e.Group("/update")
	updateRoute.Use(middleware.BasicAuth(h.AuthenticateUpdate))
	updateRoute.GET("", h.UpdateIP)
	nicRoute := e.Group("/nic")
	nicRoute.Use(middleware.BasicAuth(h.AuthenticateUpdate))
	nicRoute.GET("/update", h.UpdateIP)
	v2Route := e.Group("/v2")
	v2Route.Use(middleware.BasicAuth(h.AuthenticateUpdate))
	v2Route.GET("/update", h.UpdateIP)
	v3Route := e.Group("/v3")
	v3Route.Use(middleware.BasicAuth(h.AuthenticateUpdate))
	v3Route.GET("/update", h.UpdateIP)

	// health-check
	e.GET("/ping", func(c echo.Context) error {
		u := &handler.Error{
			Message: "OK",
		}
		return c.JSON(http.StatusOK, u)
	})

	// Start server
	e.Logger.Fatal(e.Start(":8080"))
}
