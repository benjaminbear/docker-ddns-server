package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Keep existing bookmarks working after consolidating aliases into DNS records.
func (h *Handler) ListCNames(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/@/records")
}
func (h *Handler) AddCName(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/@/records?type=CNAME")
}

// CreateCName validates the cname data from the "add cname" website,
// adds the cname entry to the database,
// and adds the entry to the DNS server.
func (h *Handler) CreateCName(c echo.Context) (err error) {
	if h.Records != nil {
		h.Records.Mu.Lock()
		defer h.Records.Mu.Unlock()
	}
	cname := &model.CName{}
	if err = c.Bind(cname); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	targetID, parseErr := strconv.ParseUint(c.FormValue("target_id"), 10, 64)
	if parseErr != nil || targetID == 0 {
		return c.JSON(http.StatusBadRequest, &Error{"invalid target_id"})
	}
	host := &model.Host{}
	if err = h.DB.Where("id = ?", targetID).First(host).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	cname.Hostname = strings.ToLower(cname.Hostname)
	cname.Target = *host
	cname.TargetID = host.ID

	if err = c.Validate(cname); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.checkUniqueHostname(cname.Hostname, cname.Target.Domain); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.DB.Create(cname).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = nswrapper.UpdateRecord(cname.Hostname, fmt.Sprintf("%s.%s", cname.Target.Hostname, cname.Target.Domain), "CNAME", cname.Target.Domain, cname.Ttl, h.AllowWildcard); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.JSON(http.StatusOK, cname)
}

// DeleteCName fetches a cname entry from the database by "id"
// and deletes the database and DNS server entry to it.
func (h *Handler) DeleteCName(c echo.Context) (err error) {
	if h.Records != nil {
		h.Records.Mu.Lock()
		defer h.Records.Mu.Unlock()
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	cname := &model.CName{}
	if err = h.DB.Preload("Target").First(cname, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Delete(cname).Error; err != nil {
			return err
		}
		return h.removeDNSRecords([]string{cname.Hostname}, cname.Target.Domain)
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}
	return c.JSON(http.StatusOK, id)
}
