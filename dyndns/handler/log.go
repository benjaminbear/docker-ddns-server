package handler

import (
	"log"
	"net/http"
	"strconv"
	"time"

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
	if err = h.DB.Preload("Host").Limit(30).Order("created_at desc").Find(logs).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listlogs", echo.Map{
		"logs":  logs,
		"title": h.Title,
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
	if err = h.DB.Preload("Host").Where(&model.Log{HostID: uint(id)}).Order("created_at desc").Limit(30).Find(logs).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listlogs", echo.Map{
		"logs":  logs,
		"title": h.Title,
	})
}

func (h *Handler) ClearLogs() {
	var clearInterval = strconv.FormatUint(h.ClearInterval, 10) + " day"
	h.DB.Exec("DELETE FROM LOGS WHERE created_at < datetime('now', '-" + clearInterval + "');REINDEX LOGS;")
	h.LastClearedLogs = time.Now()
	log.Print("logs cleared")
}
