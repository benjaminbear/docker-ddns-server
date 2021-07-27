package handler

import (
	"net/http"
	"strconv"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/labstack/echo/v4"
)

// CreateLogEntry simply adds a log entry to the database.
func (h *Handler) CreateLogEntry(log *model.Log) (err error) {
	if err = h.DB.Create(log).Error; err != nil {
		return err
	}

	return nil
}

// ShowLogs fetches all log entries from all hosts and renders them to the website.
func (h *Handler) ShowLogs(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	logs := new([]model.Log)
	if err = h.DB.Preload("Host").Limit(30).Find(logs).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listlogs", echo.Map{
		"logs": logs,
	})
}

// ShowHostLogs fetches all log entries of a specific host by "id" and renders them to the website.
func (h *Handler) ShowHostLogs(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{UNAUTHORIZED})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	logs := new([]model.Log)
	if err = h.DB.Preload("Host").Where(&model.Log{HostID: uint(id)}).Limit(30).Find(logs).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listlogs", echo.Map{
		"logs": logs,
	})
}
