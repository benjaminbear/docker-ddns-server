package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// IPBlockerMiddleware checks if the requesting IP is blocked
func (h *Handler) IPBlockerMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Extract the client IP
			clientIP := h.TrustedProxies.ClientIP(c.Request())

			// Check if IP is blocked
			isBlocked, blockedIP, err := h.IsIPBlocked(clientIP)
			if err != nil {
				log.Errorf("Error checking blocked IP %s: %v", clientIP, err)
				// Continue on error to avoid breaking the site
				return next(c)
			}

			if isBlocked {
				log.Warnf("Blocked IP %s attempted to access %s", clientIP, c.Path())

				// Update last attempt time
				if blockedIP != nil {
					blockedIP.LastAttemptAt = time.Now()
					h.DB.Save(blockedIP)
				}

				// Redirect to 127.0.0.1
				return c.Redirect(http.StatusFound, "http://127.0.0.1")
			}

			return next(c)
		}
	}
}

// SessionAuthMiddleware checks if user is authenticated via session
func (h *Handler) SessionAuthMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Skip auth if disabled
			if h.DisableAdminAuth {
				return next(c)
			}

			// Check if authenticated
			if !h.IsAuthenticated(c) {
				// Store the original URL for redirect after login
				originalURL := c.Request().URL.Path
				if c.Request().URL.RawQuery != "" {
					originalURL += "?" + c.Request().URL.RawQuery
				}

				// Redirect to login page
				return c.Redirect(http.StatusFound, "/@/login?redirect="+originalURL)
			}

			return next(c)
		}
	}
}

// HTTPSRedirectMiddleware redirects HTTP to HTTPS for admin routes
// Only applies to admin routes (/@/*) and only if HTTPS is available
func (h *Handler) HTTPSRedirectMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Only apply to admin routes
			if !strings.HasPrefix(c.Path(), "/@/") {
				return next(c)
			}

			// Skip login page to avoid redirect loop
			if c.Path() == "/@/login" {
				return next(c)
			}

			// Check if already HTTPS
			if h.IsHTTPS(c) {
				return next(c)
			}

			// Check if HTTPS is available by checking X-Forwarded-Proto header exists
			// This indicates we're behind a reverse proxy that supports HTTPS
			if h.TrustedProxies.TrustsRemote(c.Request().RemoteAddr) && c.Request().Header.Get("X-Forwarded-Proto") != "" {
				// Redirect to HTTPS
				httpsURL := h.GetHTTPSRedirectURL(c)
				return c.Redirect(http.StatusMovedPermanently, httpsURL)
			}

			// No HTTPS available, continue with HTTP
			return next(c)
		}
	}
}

// UpdateAuthMiddleware wraps BasicAuth for update endpoints
// CRITICAL: Only logs failed auth when credentials are ACTUALLY WRONG, not system errors
func (h *Handler) UpdateAuthMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Extract credentials
			username, password, ok := c.Request().BasicAuth()

			if !ok {
				h.recordRequestFailure(c, http.StatusUnauthorized, "Missing or malformed Basic authentication")
				// No credentials provided - this is NOT a failed auth attempt
				// It's a misconfigured client or direct browser access
				return c.String(http.StatusUnauthorized, "badauth\n")
			}

			// Attempt authentication
			authenticated, authError := h.AuthenticateUpdate(username, password, c)

			// If there was a system error (not wrong credentials), don't log as failed auth
			if authError != nil {
				h.recordRequestFailure(c, http.StatusUnauthorized, "Authentication service unavailable")
				log.Errorf("Authentication system error: %v", authError)
				return c.String(http.StatusUnauthorized, "badauth\n")
			}

			// Only log failed auth if authentication explicitly failed
			// This means: credentials were provided, checked, and found to be WRONG
			if !authenticated {
				clientIP := h.TrustedProxies.ClientIP(c.Request())

				log.Warnf("Failed DynDNS API authentication from IP %s, username: %s", clientIP, username)

				// Log the failed attempt (but DON'T trigger IP blocking for API endpoints)
				h.LogFailedAuth(clientIP, c.Request().UserAgent(), c.Path(), username, password, c.QueryParam("hostname"))

				return c.String(http.StatusUnauthorized, "badauth\n")
			}

			// Authentication successful
			return next(c)
		}
	}
}

// CleanupMiddleware periodically cleans up expired blocks and old records
func (h *Handler) CleanupMiddleware() echo.MiddlewareFunc {
	// Track last cleanup time
	lastCleanup := &time.Time{}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Run cleanup once per hour
			if lastCleanup.IsZero() || time.Since(*lastCleanup) > time.Hour {
				go func() {
					h.CleanupExpiredBlocks()
					h.CleanupOldFailedAuths()
				}()
				now := time.Now()
				lastCleanup = &now
			}

			return next(c)
		}
	}
}
