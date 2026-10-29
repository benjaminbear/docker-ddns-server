package handler

import (
	"log"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
)

// CreateLogEntry simply adds a log entry to the database.
func (h *Handler) CreateLogEntry(log *model.Log) (err error) {
	if err = h.DB.Create(log).Error; err != nil {
		return err
	}

	return nil
}

func (h *Handler) ShowLogs(c echo.Context) error     { return h.renderActivity(c, "") }
func (h *Handler) ShowHostLogs(c echo.Context) error { return h.renderActivity(c, c.Param("id")) }

func (h *Handler) ClearLogs() {
	var clearInterval = strconv.FormatUint(h.ClearInterval, 10) + " day"
	h.DB.Exec("DELETE FROM LOGS WHERE created_at < datetime('now', '-" + clearInterval + "');REINDEX LOGS;")
	h.LastClearedLogs = time.Now()
	log.Print("logs cleared")
}
