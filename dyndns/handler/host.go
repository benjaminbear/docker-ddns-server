package handler

import (
	"fmt"
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/jinzhu/gorm"
	"github.com/labstack/echo/v4"
	"net"
	"net/http"
	"strconv"
	"time"
)

func (h *Handler) GetHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	host := &model.Host{}
	if err = h.DB.First(host, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	// Display site
	return c.JSON(http.StatusOK, id)
}

func (h *Handler) ListHosts(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	hosts := new([]model.Host)
	if err = h.DB.Find(hosts).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "listhosts", echo.Map{
		"hosts":  hosts,
		"config": h.Config,
	})
}

func (h *Handler) AddHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	return c.Render(http.StatusOK, "edithost", echo.Map{
		"addEdit": "add",
		"config":  h.Config,
	})
}

func (h *Handler) EditHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	host := &model.Host{}
	if err = h.DB.First(host, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "edithost", echo.Map{
		"host":    host,
		"addEdit": "edit",
		"config":  h.Config,
	})
}

func (h *Handler) CreateHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	host := &model.Host{}
	if err = c.Bind(host); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = c.Validate(host); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.DB.Create(host).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	// If a ip is set create dns entry
	if host.Ip != "" {
		ipType := getIPType(host.Ip)
		if ipType == "" {
			return c.JSON(http.StatusBadRequest, &Error{fmt.Sprintf("ip %s is not a valid ip", host.Ip)})
		}

		if err = h.updateRecord(host.Hostname, host.Ip, ipType, host.Ttl); err != nil {
			return c.JSON(http.StatusBadRequest, &Error{err.Error()})
		}
	}

	return c.JSON(http.StatusOK, host)
}

func (h *Handler) UpdateHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	hostUpdate := &model.Host{}
	if err = c.Bind(hostUpdate); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	host := &model.Host{}
	if err = h.DB.First(host, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	forceRecordUpdate := host.UpdateHost(hostUpdate)
	if err = c.Validate(host); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.DB.Save(host).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	// If ip or ttl changed update dns entry
	if forceRecordUpdate {
		ipType := getIPType(host.Ip)
		if ipType == "" {
			return c.JSON(http.StatusBadRequest, &Error{fmt.Sprintf("ip %s is not a valid ip", host.Ip)})
		}

		if err = h.updateRecord(host.Hostname, host.Ip, ipType, host.Ttl); err != nil {
			return c.JSON(http.StatusBadRequest, &Error{err.Error()})
		}
	}

	return c.JSON(http.StatusOK, host)
}

func (h *Handler) DeleteHost(c echo.Context) (err error) {
	if !h.AuthAdmin {
		return c.JSON(http.StatusUnauthorized, &Error{"You are not allow to view that content"})
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	host := &model.Host{}
	if err = h.DB.First(host, id).Error; err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err = tx.Unscoped().Delete(host).Error; err != nil {
			return c.JSON(http.StatusBadRequest, &Error{err.Error()})
		}

		if err = tx.Where(&model.Log{HostID: uint(id)}).Delete(&model.Log{}).Error; err != nil {
			return c.JSON(http.StatusBadRequest, &Error{err.Error()})
		}

		return nil
	})
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	if err = h.deleteRecord(host.Hostname); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.JSON(http.StatusOK, id)
}

func (h *Handler) UpdateIP(c echo.Context) (err error) {
	if h.AuthHost == nil {
		return c.String(http.StatusBadRequest, "badauth\n")
	}

	log := &model.Log{Status: false, Host: *h.AuthHost, TimeStamp: time.Now(), UserAgent: shrinkUserAgent(c.Request().UserAgent())}
	log.SentIP = c.QueryParam(("myip"))

	// Get caller IP
	log.CallerIP, err = getCallerIP(c.Request())
	if log.CallerIP == "" {
		log.CallerIP, _, err = net.SplitHostPort(c.Request().RemoteAddr)
		if err != nil {
			if err = h.CreateLogEntry(log); err != nil {
				fmt.Println(err)
			}

			return c.String(http.StatusBadRequest, "badrequest\n")
		}
	}

	// Validate hostname
	hostname := c.QueryParam("hostname")
	if hostname == "" || hostname != h.AuthHost.Hostname+"."+h.Config.Domain {
		if err = h.CreateLogEntry(log); err != nil {
			fmt.Println(err)
		}

		return c.String(http.StatusBadRequest, "notfqdn\n")
	}

	// Get IP type
	ipType := getIPType(log.SentIP)
	if ipType == "" {
		log.SentIP = log.CallerIP
		ipType = getIPType(log.SentIP)
		if ipType == "" {
			if err = h.CreateLogEntry(log); err != nil {
				fmt.Println(err)
			}

			return c.String(http.StatusBadRequest, "badrequest\n")
		}
	}

	// add/update DNS record
	if err = h.updateRecord(log.Host.Hostname, log.SentIP, ipType, log.Host.Ttl); err != nil {
		if err = h.CreateLogEntry(log); err != nil {
			fmt.Println(err)
		}

		return c.String(http.StatusBadRequest, "dnserr\n")
	}

	log.Host.Ip = log.SentIP
	log.Host.LastUpdate = log.TimeStamp
	log.Status = true
	if err = h.CreateLogEntry(log); err != nil {
		fmt.Println(err)
	}

	return c.String(http.StatusOK, "good\n")
}
