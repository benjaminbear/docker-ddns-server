package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"gorm.io/gorm/clause"
)

const authFlowCookie = "__Host-ddns_auth_flow"

type AdminPasskey struct {
	ID         string `gorm:"primaryKey"`
	Name       string
	Credential []byte
	Provider   string
	Subject    string
	Email      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}
type AdminIdentity struct {
	ID     uint `gorm:"primaryKey"`
	Handle []byte
}
type GoogleIdentity struct {
	Email   string `gorm:"primaryKey"`
	Subject string `gorm:"uniqueIndex;not null"`
}
type adminUser struct {
	Handle      []byte
	Credentials []webauthn.Credential
}

func (u adminUser) WebAuthnID() []byte                         { return u.Handle }
func (u adminUser) WebAuthnName() string                       { return "DDNS administrator" }
func (u adminUser) WebAuthnDisplayName() string                { return "DDNS administrator" }
func (u adminUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

type authPrincipal struct{ Provider, Subject, Email, Passkey string }
type authFlow struct {
	Kind, State, Nonce, Verifier, SessionID, Name string
	Principal                                     authPrincipal
	WebAuthn                                      *webauthn.SessionData
	Expires                                       time.Time
}
type AdminAuth struct {
	Origin, Host  string
	WebAuthn      *webauthn.WebAuthn
	OAuth         *oauth2.Config
	Verifier      *oidc.IDTokenVerifier
	AllowedEmails map[string]bool
	Handle        []byte
	mu            sync.Mutex
	flows         map[string]authFlow
	credentialMu  sync.Mutex
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *Handler) InitAdminAuth() error {
	origin := strings.TrimRight(os.Getenv("DDNS_AUTH_ORIGIN"), "/")
	clientID, secret, emails := os.Getenv("DDNS_GOOGLE_CLIENT_ID"), os.Getenv("DDNS_GOOGLE_CLIENT_SECRET"), os.Getenv("DDNS_GOOGLE_ALLOWED_EMAILS")
	if origin == "" && clientID == "" && secret == "" && emails == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("DDNS_AUTH_ORIGIN must be an HTTPS origin without path, query or credentials")
	}
	if h.DisableAdminAuth {
		return fmt.Errorf("configure DDNS_ADMIN_LOGIN before enabling Google or passkeys")
	}
	a := &AdminAuth{Origin: origin, Host: u.Host, AllowedEmails: map[string]bool{}, flows: map[string]authFlow{}}
	a.WebAuthn, err = webauthn.New(&webauthn.Config{RPID: u.Hostname(), RPDisplayName: h.Title, RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}})
	if err != nil {
		return err
	}
	if clientID != "" || secret != "" || emails != "" {
		if clientID == "" || secret == "" || emails == "" {
			return fmt.Errorf("Google login requires client ID, client secret and explicit allowed emails")
		}
		for _, email := range strings.Split(emails, ",") {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" || strings.ContainsAny(email, "* \t\r\n") || !strings.Contains(email, "@") {
				return fmt.Errorf("invalid Google allowed email")
			}
			a.AllowedEmails[email] = true
		}
		a.OAuth = &oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: google.Endpoint, RedirectURL: origin + "/@/auth/google/callback", Scopes: []string{oidc.ScopeOpenID, "email"}}
		ctx := oidc.ClientContext(context.Background(), &http.Client{Timeout: 10 * time.Second})
		keys := oidc.NewRemoteKeySet(ctx, "https://www.googleapis.com/oauth2/v3/certs")
		a.Verifier = oidc.NewVerifier("https://accounts.google.com", keys, &oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{"RS256"}})
	}
	if err = h.DB.AutoMigrate(&AdminPasskey{}, &AdminIdentity{}, &GoogleIdentity{}); err != nil {
		return err
	}
	identity := AdminIdentity{ID: 1, Handle: []byte(randomToken())}
	if err = h.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
		return err
	}
	if err = h.DB.First(&identity, 1).Error; err != nil {
		return err
	}
	a.Handle = identity.Handle
	h.AdminAuth = a
	return nil
}

func (h *Handler) AdminAuthRoutes(e *echo.Echo) {
	g := e.Group("/@/auth")
	g.Use(middleware.BodyLimit("256K"))
	g.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: 1, Burst: 20, ExpiresIn: time.Minute}), IdentifierExtractor: func(c echo.Context) (string, error) { return h.TrustedProxies.ClientIP(c.Request()), nil }}))
	g.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Response().Header().Set("Cache-Control", "no-store")
			c.Response().Header().Set("Referrer-Policy", "no-referrer")
			a := h.AdminAuth
			if a == nil || h.DisableAdminAuth {
				return echo.NewHTTPError(404, "Additional sign-in methods are not configured")
			}
			if !h.IsHTTPS(c) || !strings.EqualFold(c.Request().Host, a.Host) {
				return echo.NewHTTPError(403, "Use the configured HTTPS sign-in domain")
			}
			if c.Request().Method == http.MethodPost && c.Request().Header.Get("Origin") != a.Origin {
				return echo.NewHTTPError(403, "Invalid request origin")
			}
			return next(c)
		}
	})
	g.GET("/google", h.GoogleStart)
	g.GET("/google/callback", h.GoogleCallback)
	g.POST("/passkey/login/begin", h.PasskeyLoginBegin)
	g.POST("/passkey/login/finish", h.PasskeyLoginFinish)
	g.GET("/passkeys", h.PasskeysPage)
	g.POST("/passkey/register/begin", h.PasskeyRegisterBegin)
	g.POST("/passkey/register/finish", h.PasskeyRegisterFinish)
	g.POST("/passkey/:id/revoke", h.PasskeyRevoke)
}

func (a *AdminAuth) putFlow(c echo.Context, f authFlow) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for id, old := range a.flows {
		if now.After(old.Expires) {
			delete(a.flows, id)
		}
	}
	if old, err := c.Cookie(authFlowCookie); err == nil {
		delete(a.flows, old.Value)
	}
	if len(a.flows) >= 1024 {
		return echo.NewHTTPError(429, "Too many sign-in attempts; try again shortly")
	}
	id := randomToken()
	f.Expires = now.Add(5 * time.Minute)
	a.flows[id] = f
	c.SetCookie(&http.Cookie{Name: authFlowCookie, Value: id, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	return nil
}
func (a *AdminAuth) takeFlow(c echo.Context, kind string) (authFlow, error) {
	cookie, err := c.Cookie(authFlowCookie)
	if err != nil {
		return authFlow{}, echo.NewHTTPError(400, "Sign-in expired; start again")
	}
	a.mu.Lock()
	f, ok := a.flows[cookie.Value]
	delete(a.flows, cookie.Value)
	a.mu.Unlock()
	c.SetCookie(&http.Cookie{Name: authFlowCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if !ok || f.Kind != kind || time.Now().After(f.Expires) {
		return authFlow{}, echo.NewHTTPError(400, "Sign-in expired; start again")
	}
	return f, nil
}

func (h *Handler) GoogleStart(c echo.Context) error {
	a := h.AdminAuth
	if a.OAuth == nil {
		return echo.NewHTTPError(404, "Google sign-in is not configured")
	}
	f := authFlow{Kind: "google", State: randomToken(), Nonce: randomToken(), Verifier: oauth2.GenerateVerifier()}
	if err := a.putFlow(c, f); err != nil {
		return err
	}
	return c.Redirect(302, a.OAuth.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier), oauth2.SetAuthURLParam("nonce", f.Nonce), oauth2.SetAuthURLParam("prompt", "select_account")))
}
func (h *Handler) GoogleCallback(c echo.Context) error {
	a := h.AdminAuth
	if a.OAuth == nil {
		return echo.NewHTTPError(404)
	}
	f, err := a.takeFlow(c, "google")
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(c.QueryParam("state")), []byte(f.State)) != 1 || c.QueryParam("code") == "" {
		return echo.NewHTTPError(400, "Google sign-in was cancelled or expired")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	token, err := a.OAuth.Exchange(ctx, c.QueryParam("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return echo.NewHTTPError(401, "Google sign-in failed; try again")
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return echo.NewHTTPError(401, "Missing Google identity")
	}
	id, err := a.Verifier.Verify(ctx, raw)
	if err != nil {
		return echo.NewHTTPError(401, "Invalid Google identity")
	}
	var claims struct {
		Email           string `json:"email"`
		Verified        bool   `json:"email_verified"`
		Nonce           string `json:"nonce"`
		AuthorizedParty string `json:"azp"`
	}
	if err = id.Claims(&claims); err != nil || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(f.Nonce)) != 1 || !claims.Verified || id.Subject == "" || ((claims.AuthorizedParty != "" || len(id.Audience) > 1) && claims.AuthorizedParty != a.OAuth.ClientID) {
		return echo.NewHTTPError(401, "Invalid Google identity")
	}
	email := strings.ToLower(claims.Email)
	if !a.AllowedEmails[email] {
		return echo.NewHTTPError(403, "This Google account is not authorized")
	}
	// Pin the stable Google subject; reassigning an email must not silently transfer access.
	identity := GoogleIdentity{Email: email, Subject: id.Subject}
	if err = h.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
		return echo.NewHTTPError(500, "Could not save identity")
	}
	if err = h.DB.Where("email = ? AND subject = ?", email, id.Subject).First(&identity).Error; err != nil {
		return echo.NewHTTPError(403, "Google identity does not match the authorized account")
	}
	if err = h.newAdminSession(c, authPrincipal{Provider: "google", Subject: id.Subject, Email: email}, false); err != nil {
		return err
	}
	// Complete the cross-site OAuth navigation on our own origin before sending
	// the Strict admin cookie on the next same-site navigation.
	return c.HTML(200, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Signed in</title></head><body><p>Signed in. <a href="/@/hosts">Continue to hosts</a></p><script src="/static/js/auth-complete.js"></script></body></html>`)
}

func (h *Handler) principalAllowed(p authPrincipal) bool {
	switch p.Provider {
	case "password":
		for _, line := range strings.Split(h.Config.AdminLogin, "\n") {
			if strings.SplitN(line, ":", 2)[0] == p.Subject && p.Subject != "" {
				return true
			}
		}
	case "google":
		if h.AdminAuth == nil || h.AdminAuth.OAuth == nil || !h.AdminAuth.AllowedEmails[p.Email] {
			return false
		}
		var count int64
		return h.DB.Model(&GoogleIdentity{}).Where("email = ? AND subject = ?", p.Email, p.Subject).Count(&count).Error == nil && count == 1
	}
	return false
}
func principalFromSession(s *Session) authPrincipal {
	get := func(k string) string { v, _ := s.Values[k].(string); return v }
	return authPrincipal{Provider: get("provider"), Subject: get("subject"), Email: get("email"), Passkey: get("passkey")}
}
func (h *Handler) newAdminSession(c echo.Context, p authPrincipal, remember bool) error {
	s := &Session{Session: sessions.NewSession(h.SessionStore.store, SessionName)}
	age := 86400
	if remember {
		age *= 30
	}
	s.Options = &sessions.Options{Path: "/", MaxAge: age, HttpOnly: true, Secure: h.IsHTTPS(c), SameSite: http.SameSiteStrictMode}
	s.Values[SessionAuthKey] = true
	s.Values[SessionUserKey] = p.Subject
	if p.Email != "" {
		s.Values[SessionUserKey] = p.Email
	}
	s.Values[SessionCreatedAt] = time.Now().Unix()
	s.Values[SessionExpiresAt] = time.Now().Add(time.Duration(age) * time.Second).Unix()
	s.Values["provider"] = p.Provider
	s.Values["subject"] = p.Subject
	s.Values["email"] = p.Email
	s.Values["passkey"] = p.Passkey
	s.Values["auth_id"] = randomToken()
	return s.Save(c.Request(), c.Response())
}
func (h *Handler) freshAdmin(c echo.Context) (*Session, error) {
	if h.DisableAdminAuth || !h.IsAuthenticated(c) {
		return nil, echo.NewHTTPError(401, "Sign in first")
	}
	s, err := h.GetSession(c)
	if err != nil {
		return nil, echo.NewHTTPError(401)
	}
	created, ok := s.Values[SessionCreatedAt].(int64)
	id, _ := s.Values["auth_id"].(string)
	if !ok || id == "" || created > time.Now().Unix() || time.Since(time.Unix(created, 0)) > 5*time.Minute {
		return nil, echo.NewHTTPError(403, "Sign out and sign in again before managing passkeys (within five minutes)")
	}
	return s, nil
}
func (h *Handler) passkeyUser() (adminUser, error) {
	var rows []AdminPasskey
	if err := h.DB.Where("revoked_at IS NULL").Find(&rows).Error; err != nil {
		return adminUser{}, err
	}
	u := adminUser{Handle: h.AdminAuth.Handle}
	for _, row := range rows {
		if !h.principalAllowed(authPrincipal{Provider: row.Provider, Subject: row.Subject, Email: row.Email}) {
			continue
		}
		var cred webauthn.Credential
		if err := json.Unmarshal(row.Credential, &cred); err != nil {
			return u, err
		}
		u.Credentials = append(u.Credentials, cred)
	}
	return u, nil
}
func (h *Handler) PasskeysPage(c echo.Context) error {
	if !h.IsAuthenticated(c) {
		return c.Redirect(303, "/@/login")
	}
	var rows []AdminPasskey
	if err := h.DB.Select("id", "name", "created_at", "last_used_at", "provider", "email").Where("revoked_at IS NULL").Order("created_at").Find(&rows).Error; err != nil {
		return err
	}
	data := h.page(c)
	data["passkeys"] = rows
	data["origin"] = h.AdminAuth.Origin
	return c.Render(200, "passkeys", data)
}
func (h *Handler) PasskeyRegisterBegin(c echo.Context) error {
	s, err := h.freshAdmin(c)
	if err != nil {
		return err
	}
	var input struct {
		Name string `json:"name"`
	}
	if err = c.Bind(&input); err != nil {
		return echo.NewHTTPError(400, "Invalid request")
	}
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) == 0 || len(input.Name) > 80 {
		return echo.NewHTTPError(400, "Give this passkey a name (1–80 characters)")
	}
	u, err := h.passkeyUser()
	if err != nil {
		return err
	}
	if len(u.Credentials) >= 20 {
		return echo.NewHTTPError(400, "Remove an unused passkey before adding another")
	}
	exclusions := []protocol.CredentialDescriptor{}
	for _, cred := range u.Credentials {
		exclusions = append(exclusions, cred.Descriptor())
	}
	options, session, err := h.AdminAuth.WebAuthn.BeginRegistration(u, webauthn.WithExclusions(exclusions))
	if err != nil {
		return echo.NewHTTPError(500, "Could not start passkey registration")
	}
	f := authFlow{Kind: "register", WebAuthn: session, SessionID: s.Values["auth_id"].(string), Principal: principalFromSession(s), Name: input.Name}
	if err = h.AdminAuth.putFlow(c, f); err != nil {
		return err
	}
	return c.JSON(200, options)
}
func (h *Handler) PasskeyRegisterFinish(c echo.Context) error {
	s, err := h.freshAdmin(c)
	if err != nil {
		return err
	}
	f, err := h.AdminAuth.takeFlow(c, "register")
	if err != nil {
		return err
	}
	if s.Values["auth_id"] != f.SessionID || !h.principalAllowed(f.Principal) {
		return echo.NewHTTPError(403, "Sign-in changed; start again")
	}
	h.AdminAuth.credentialMu.Lock()
	defer h.AdminAuth.credentialMu.Unlock()
	u, err := h.passkeyUser()
	if err != nil {
		return err
	}
	credential, err := h.AdminAuth.WebAuthn.FinishRegistration(u, *f.WebAuthn, c.Request())
	if err != nil {
		return echo.NewHTTPError(400, "Passkey registration could not be verified")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	row := AdminPasskey{ID: base64.RawURLEncoding.EncodeToString(credential.ID), Name: f.Name, Credential: raw, Provider: f.Principal.Provider, Subject: f.Principal.Subject, Email: f.Principal.Email}
	if err = h.DB.Create(&row).Error; err != nil {
		return echo.NewHTTPError(409, "Passkey is already registered or could not be saved")
	}
	return c.JSON(200, echo.Map{"ok": true})
}
func (h *Handler) PasskeyLoginBegin(c echo.Context) error {
	options, session, err := h.AdminAuth.WebAuthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return echo.NewHTTPError(500, "Could not start passkey sign-in")
	}
	if err = h.AdminAuth.putFlow(c, authFlow{Kind: "login", WebAuthn: session}); err != nil {
		return err
	}
	return c.JSON(200, options)
}
func (h *Handler) PasskeyLoginFinish(c echo.Context) error {
	f, err := h.AdminAuth.takeFlow(c, "login")
	if err != nil {
		return err
	}
	h.AdminAuth.credentialMu.Lock()
	defer h.AdminAuth.credentialMu.Unlock()
	var selected AdminPasskey
	lookup := func(rawID, userHandle []byte) (webauthn.User, error) {
		if !bytes.Equal(userHandle, h.AdminAuth.Handle) {
			return nil, errors.New("invalid user handle")
		}
		if err := h.DB.Where("id = ? AND revoked_at IS NULL", base64.RawURLEncoding.EncodeToString(rawID)).First(&selected).Error; err != nil {
			return nil, err
		}
		if !h.principalAllowed(authPrincipal{Provider: selected.Provider, Subject: selected.Subject, Email: selected.Email}) {
			return nil, errors.New("account no longer authorized")
		}
		return h.passkeyUser()
	}
	credential, err := h.AdminAuth.WebAuthn.FinishDiscoverableLogin(lookup, *f.WebAuthn, c.Request())
	if err != nil || credential.Authenticator.CloneWarning {
		return echo.NewHTTPError(401, "Passkey sign-in could not be verified")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	now := time.Now()
	result := h.DB.Model(&AdminPasskey{}).Where("id = ? AND revoked_at IS NULL", selected.ID).Updates(map[string]interface{}{"credential": raw, "last_used_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return echo.NewHTTPError(401, "Passkey is no longer available")
	}
	p := authPrincipal{Provider: selected.Provider, Subject: selected.Subject, Email: selected.Email, Passkey: selected.ID}
	if err = h.newAdminSession(c, p, false); err != nil {
		return err
	}
	return c.JSON(200, echo.Map{"redirect": "/@/hosts"})
}
func (h *Handler) PasskeyRevoke(c echo.Context) error {
	if _, err := h.freshAdmin(c); err != nil {
		return err
	}
	result := h.DB.Model(&AdminPasskey{}).Where("id = ? AND revoked_at IS NULL", c.Param("id")).Update("revoked_at", time.Now())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return echo.NewHTTPError(404, "Passkey not found")
	}
	return c.JSON(200, echo.Map{"ok": true})
}

func (h *Handler) adminAuthOrigin() string {
	if h.AdminAuth == nil {
		return ""
	}
	return h.AdminAuth.Origin
}
