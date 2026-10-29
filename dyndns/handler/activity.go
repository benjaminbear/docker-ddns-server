package handler

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type activityEntry struct {
	Hostname  string
	SentIP    string
	CallerIP  string
	UserAgent string
	Path      string
	Message   string
	Source    string
	Status    bool
	Code      int
	TimeStamp time.Time
	First     time.Time
	Count     int
	order     uint
}

func collapseActivity(entries []activityEntry) []activityEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].TimeStamp.Equal(entries[j].TimeStamp) {
			return entries[i].order < entries[j].order
		}
		return entries[i].TimeStamp.Before(entries[j].TimeStamp)
	})
	result := []activityEntry{}
	last := map[string]int{}
	for _, e := range entries {
		stream := e.Hostname
		if stream == "" {
			stream = e.Path + "\x00" + e.UserAgent
		}
		if i, ok := last[stream]; ok {
			prev := &result[i]
			if prev.SentIP == e.SentIP && prev.CallerIP == e.CallerIP && prev.Status == e.Status && prev.Code == e.Code && prev.Message == e.Message && prev.UserAgent == e.UserAgent && prev.Path == e.Path && prev.Source == e.Source {
				prev.TimeStamp = e.TimeStamp
				prev.Count++
				continue
			}
		}
		e.First = e.TimeStamp
		e.Count = 1
		last[stream] = len(result)
		result = append(result, e)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].TimeStamp.After(result[j].TimeStamp) })
	return result
}

func (h *Handler) activity() ([]activityEntry, error) {
	var logs []model.Log
	if err := h.DB.Preload("Host", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).Order("id").Find(&logs).Error; err != nil {
		return nil, err
	}
	entries := []activityEntry{}
	seen := map[string]bool{}
	key := func(name string, t time.Time, code int) string {
		return name + "|" + strconv.FormatInt(t.Unix(), 10) + "|" + strconv.Itoa(code)
	}
	for _, l := range logs {
		name := l.Host.Hostname + "." + l.Host.Domain
		if l.Host.ID == 0 {
			name = ""
		}
		code := 200
		if !l.Status {
			code = 400
		}
		t := l.TimeStamp
		if t.IsZero() {
			t = l.CreatedAt
		}
		entries = append(entries, activityEntry{Hostname: name, SentIP: l.SentIP, CallerIP: l.CallerIP, UserAgent: l.UserAgent, Message: l.Message, Source: "DDNS", Status: l.Status, Code: code, TimeStamp: t, order: l.ID})
		seen[key(name, t, code)] = true
	}
	var auths []model.FailedAuth
	if err := h.DB.Select("id,hostname,ip_address,user_agent,timestamp,path").Where("path IN ?", []string{"/update", "/nic/update", "/v2/update", "/v3/update"}).Order("id").Find(&auths).Error; err != nil {
		return nil, err
	}
	for _, a := range auths {
		entries = append(entries, activityEntry{Hostname: a.Hostname, CallerIP: a.IPAddress, UserAgent: a.UserAgent, Path: a.Path, Message: "Authentication failed", Source: "DDNS", Code: 401, TimeStamp: a.Timestamp, order: a.ID})
		seen[key(a.Hostname, a.Timestamp, 401)] = true
	}
	var proxy []model.ProxyLog
	if err := h.DB.Order("id").Find(&proxy).Error; err != nil {
		return nil, err
	}
	for _, p := range proxy {
		if p.Source == "nginx" && seen[key(p.Hostname, p.TimeStamp, p.Code)] {
			continue
		}
		entries = append(entries, activityEntry{Hostname: p.Hostname, SentIP: p.SentIP, CallerIP: p.CallerIP, UserAgent: p.UserAgent, Path: p.Path, Message: p.Message, Source: p.Source, Code: p.Code, TimeStamp: p.TimeStamp, order: p.ID})
	}
	return collapseActivity(entries), nil
}

func (h *Handler) renderActivity(c echo.Context, hostID string) error {
	entries, err := h.activity()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, &Error{"Unable to load activity"})
	}
	hostname := ""
	if hostID != "" {
		id, err := strconv.ParseUint(hostID, 10, 64)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid host")
		}
		var host model.Host
		if h.DB.First(&host, id).Error != nil {
			return echo.NewHTTPError(http.StatusNotFound, "Host not found")
		}
		hostname = host.Hostname + "." + host.Domain
		filtered := entries[:0]
		for _, e := range entries {
			if e.Hostname == hostname {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}
	h.proxyMu.Lock()
	warning := h.proxyError
	h.proxyMu.Unlock()
	if os.Getenv("DDNS_NGINX_LOG_DIR") == "" {
		warning = "Proxy logs are not connected. Requests rejected before reaching DDNS are not included."
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.Render(http.StatusOK, "listlogs", echo.Map{"logs": entries, "hostname": hostname, "proxyWarning": warning, "title": h.Title, "logoPath": h.LogoPath, "poweredBy": h.PoweredBy, "poweredByUrl": h.PoweredByUrl})
}

func (h *Handler) recordRequestFailure(c echo.Context, code int, message string) {
	now := time.Now()
	entry := model.ProxyLog{Key: fmt.Sprintf("request:%d", now.UnixNano()), TimeStamp: now, Hostname: strings.ToLower(strings.TrimSpace(c.QueryParam("hostname"))), CallerIP: h.TrustedProxies.ClientIP(c.Request()), UserAgent: c.Request().UserAgent(), Path: c.Path(), Code: code, Message: message, Source: "DDNS"}
	if len(entry.Hostname) > 253 {
		entry.Hostname = entry.Hostname[:253]
	}
	if err := h.DB.Create(&entry).Error; err != nil {
		c.Logger().Error("Unable to record update request failure")
	}
}

// Security keeps original credentials available, but combines only identical
// consecutive attempts. Changing an attempted password starts a new entry too.
func collapseFailedAuths(rows []model.FailedAuth) []model.FailedAuth {
	out := []model.FailedAuth{}
	last := map[string]int{}
	for _, r := range rows { // already newest first
		stream := r.Hostname + "|" + r.Username + "|" + r.Path
		if i, ok := last[stream]; ok {
			p := &out[i]
			if p.IPAddress == r.IPAddress && p.Password == r.Password && p.UserAgent == r.UserAgent {
				p.RepeatCount++
				continue
			}
		}
		r.RepeatCount = 1
		last[stream] = len(out)
		out = append(out, r)
	}
	return out
}
