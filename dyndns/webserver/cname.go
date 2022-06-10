package webserver

import (
	"net/http"
	"strconv"

	"github.com/benjaminbear/docker-ddns-server/dyndns/db"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// ListCNames fetches all cnames from database and lists them on the website.
func (h *Handler) ListCNames(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	cnames := new([]db.CName)
	if err = h.DB.Preload("Target").Find(cnames).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listcnames", echo.Map{
		"cnames": cnames,
		"title":  h.Config.Title,
	})
}

// AddCName just renders the "add cname" website.
// Therefore all host entries from the database are being fetched.
func (h *Handler) AddCName(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	hosts := new([]db.Host)
	if err = h.DB.Find(hosts).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "addcname", echo.Map{
		"config": h.Config,
		"hosts":  hosts,
		"title":  h.Config.Title,
	})
}

// CreateCName validates the cname data from the "add cname" website,
// adds the cname entry to the database,
// and adds the entry to the DNS server.
func (h *Handler) CreateCName(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	cname := &db.CName{}
	if err = c.Bind(cname); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	host := &db.Host{}
	if err = h.DB.First(host, c.FormValue("target_id")).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	cname.Target = *host

	if err = c.Validate(cname); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.checkUniqueHostname(cname.Hostname, cname.Target.Domain); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.DB.Create(cname).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.JSON(http.StatusOK, cname)
}

// DeleteCName fetches a cname entry from the database by "id"
// and deletes the database and DNS server entry to it.
func (h *Handler) DeleteCName(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	cname := &db.CName{}
	if err = h.DB.Preload("Target").First(cname, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err = tx.Unscoped().Delete(cname).Error; err != nil {
			return c.JSON(http.StatusBadRequest, &Error{err.Error()})
		}

		return nil
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.JSON(http.StatusOK, id)
}
