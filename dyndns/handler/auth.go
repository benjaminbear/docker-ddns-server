package handler

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// Session key constants
const (
	SessionName      = "ddns_session"
	SessionUserKey   = "user"
	SessionAuthKey   = "authenticated"
	SessionCreatedAt = "created_at"
	SessionExpiresAt = "expires_at"
)

// ShowLoginPage renders the login page
func (h *Handler) ShowLoginPage(c echo.Context) error {
	// Check if already authenticated
	if h.IsAuthenticated(c) {
		return c.Redirect(http.StatusFound, "/@/hosts")
	}

	// Check if there's an error message from failed login
	errorMsg := c.QueryParam("error")
	google, passkeys, origin := false, false, ""
	if h.AdminAuth != nil {
		google = h.AdminAuth.OAuth != nil
		passkeys = true
		origin = h.AdminAuth.Origin
	}
	c.Response().Header().Set("Cache-Control", "no-store")

	return c.Render(http.StatusOK, "login", echo.Map{
		"title":         h.Title,
		"logoPath":      h.LogoPath,
		"poweredBy":     h.PoweredBy,
		"poweredByUrl":  h.PoweredByUrl,
		"error":         errorMsg,
		"googleEnabled": google, "passkeysEnabled": passkeys, "authOrigin": origin,
	})
}

// HandleLogin processes login form submission
func (h *Handler) HandleLogin(c echo.Context) error {
	username := c.FormValue("username")
	password := c.FormValue("password")
	rememberMe := c.FormValue("remember_me") == "on"

	// Get client IP for logging
	clientIP := h.TrustedProxies.ClientIP(c.Request())

	// Validate credentials
	authenticated, err := h.authByEnv(username, password)
	if err != nil {
		log.Error("Authentication error:", err)
		h.LogFailedAuth(clientIP, c.Request().UserAgent(), c.Path(), username, password)
		return c.Redirect(http.StatusFound, "/@/login?error=authentication_error")
	}

	if !authenticated {
		log.Warnf("Failed login attempt from IP %s, username: %s", clientIP, username)
		h.LogFailedAuth(clientIP, c.Request().UserAgent(), c.Path(), username, password)
		return c.Redirect(http.StatusFound, "/@/login?error=invalid_credentials")
	}

	if err := h.newAdminSession(c, authPrincipal{Provider: "password", Subject: username}, rememberMe); err != nil {
		return c.Redirect(http.StatusFound, "/@/login?error=session_error")
	}

	log.Infof("Successful login from IP %s, username: %s", clientIP, username)

	// Redirect to originally requested page or default to hosts
	redirect := c.QueryParam("redirect")
	if !strings.HasPrefix(redirect, "/@/") || strings.ContainsAny(redirect, "\\\r\n") || strings.Contains(redirect, "..") || redirect == "/@/login" {
		redirect = "/@/hosts"
	}
	return c.Redirect(http.StatusFound, redirect)
}

// HandleLogout destroys the session and logs out the user
func (h *Handler) HandleLogout(c echo.Context) error {
	sess, err := h.GetSession(c)
	if err == nil {
		// Get username before destroying session
		username := "unknown"
		if user, ok := sess.Values[SessionUserKey].(string); ok {
			username = user
		}

		// Destroy session
		sess.Options.MaxAge = -1
		sess.Values = make(map[interface{}]interface{})
		sess.Save(c.Request(), c.Response())

		if username != "" {
			log.Infof("User %s logged out", username)
		}
	}

	// Clear session cookie
	c.SetCookie(&http.Cookie{
		Name:     SessionName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.IsHTTPS(c),
		SameSite: http.SameSiteStrictMode,
	})

	// ALWAYS render logout page (not redirect)
	// Pass LogoutUrl so JavaScript can handle delayed redirect
	return c.Render(http.StatusOK, "logout", echo.Map{
		"title":        h.Title,
		"logoPath":     h.LogoPath,
		"logoutUrl":    h.LogoutUrl, // Pass the logout URL to template
		"poweredBy":    h.PoweredBy,
		"poweredByUrl": h.PoweredByUrl,
	})
}

// IsAuthenticated checks if the current session is authenticated
func (h *Handler) IsAuthenticated(c echo.Context) bool {
	if h.DisableAdminAuth {
		return true
	}

	sess, err := h.GetSession(c)
	if err != nil {
		return false
	}

	// Check if authenticated
	authenticated, ok := sess.Values[SessionAuthKey].(bool)
	if !ok || !authenticated {
		return false
	}

	// Check if session has expired
	if expiresAt, ok := sess.Values[SessionExpiresAt].(int64); ok {
		if time.Now().Unix() > expiresAt {
			log.Info("Session expired")
			return false
		}
	}

	p := principalFromSession(sess)
	if p.Provider != "" && !h.principalAllowed(p) {
		return false
	}
	if p.Passkey != "" {
		if h.AdminAuth == nil {
			return false
		}
		var count int64
		if h.DB.Model(&AdminPasskey{}).Where("id = ? AND revoked_at IS NULL", p.Passkey).Count(&count).Error != nil || count != 1 {
			return false
		}
	}

	return true
}

// GetSession retrieves or creates a session for the request
func (h *Handler) GetSession(c echo.Context) (*Session, error) {
	return h.SessionStore.Get(c.Request(), SessionName)
}

// GenerateCSRFToken generates a random CSRF token
func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// IsHTTPS checks if the request came via HTTPS
// Checks both direct HTTPS and reverse proxy headers
func (h *Handler) IsHTTPS(c echo.Context) bool {
	// Check if direct HTTPS
	if c.Request().TLS != nil {
		return true
	}

	if !h.TrustedProxies.TrustsRemote(c.Request().RemoteAddr) {
		return false
	}

	// Check reverse proxy headers
	proto := c.Request().Header.Get("X-Forwarded-Proto")
	if proto == "https" {
		return true
	}

	// Check other common headers
	if c.Request().Header.Get("X-Forwarded-Ssl") == "on" {
		return true
	}

	if c.Request().Header.Get("X-Url-Scheme") == "https" {
		return true
	}

	return false
}

// GetHTTPSRedirectURL constructs the HTTPS version of the current URL
func (h *Handler) GetHTTPSRedirectURL(c echo.Context) string {
	host := c.Request().Host
	uri := c.Request().RequestURI
	return "https://" + host + uri
}
