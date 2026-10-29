package handler

import (
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
)

// ShowSecurityDashboard displays the security overview page
func (h *Handler) ShowSecurityDashboard(c echo.Context) error {

	// Get recent failed auths
	failedAuths, err := h.GetRecentFailedAuths(-1)
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	// Get blocked IPs
	blockedIPs, err := h.GetBlockedIPs()
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	// Count active blocks
	activeBlocks := 0
	for _, blocked := range blockedIPs {
		if blocked.IsBlocked() {
			activeBlocks++
		}
	}

	return c.Render(http.StatusOK, "security_dashboard", echo.Map{
		"failedAuths":  collapseFailedAuths(failedAuths),
		"failedCount":  len(failedAuths),
		"blockedIPs":   blockedIPs,
		"activeBlocks": activeBlocks,
		"authOrigin":   h.adminAuthOrigin(),
		"title":        h.Title,
		"logoPath":     h.LogoPath,
		"poweredBy":    h.PoweredBy,
		"poweredByUrl": h.PoweredByUrl,
	})
}

// ShowBlockedIPs displays all blocked IPs
func (h *Handler) ShowBlockedIPs(c echo.Context) error {

	blockedIPs, err := h.GetBlockedIPs()
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "blocked_ips", echo.Map{
		"blockedIPs":   blockedIPs,
		"title":        h.Title,
		"logoPath":     h.LogoPath,
		"poweredBy":    h.PoweredBy,
		"poweredByUrl": h.PoweredByUrl,
	})
}

// ShowFailedAuths displays recent failed authentication attempts
func (h *Handler) ShowFailedAuths(c echo.Context) error {
	// Auth check removed - middleware handles this

	failedAuths, err := h.GetRecentFailedAuths(-1)
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.Render(http.StatusOK, "failed_auths", echo.Map{
		"failedAuths":  collapseFailedAuths(failedAuths),
		"failedCount":  len(failedAuths),
		"title":        h.Title,
		"logoPath":     h.LogoPath,
		"poweredBy":    h.PoweredBy,
		"poweredByUrl": h.PoweredByUrl,
	})
}

// UnblockIPHandler handles the unblock IP request
func (h *Handler) UnblockIPHandler(c echo.Context) error {
	// Get IP from URL parameter and decode it
	encodedIP := c.Param("ip")
	ipAddress, err := url.QueryUnescape(encodedIP)
	if err != nil {
		return c.JSON(http.StatusBadRequest, &Error{"Invalid IP address format"})
	}

	if err := h.UnblockIP(ipAddress); err != nil {
		return c.JSON(http.StatusBadRequest, &Error{err.Error()})
	}

	return c.JSON(http.StatusOK, echo.Map{
		"message": "IP unblocked successfully",
		"ip":      ipAddress,
	})
}
