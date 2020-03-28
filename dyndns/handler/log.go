package handler

import (
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/labstack/echo/v4"
	"net/http"
	"strconv"
)

func (h *Handler) CreateLogEntry(log *model.Log) (err error) {
	if err = h.DB.Create(log).Error; err != nil {
		return err
	}

	return nil
}

func (h *Handler) ShowLogs(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	logs := new([]model.Log)
	if err = h.DB.Preload("Host").Limit(30).Find(logs).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listlogs", echo.Map{
		"logs":   logs,
		"config": h.Config,
	})
}

func (h *Handler) ShowHostLogs(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
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
		"logs":   logs,
		"config": h.Config,
	})
}
