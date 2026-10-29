package handler

import (
	"encoding/gob"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/sessions"
)

// Session wraps gorilla session
type Session struct {
	*sessions.Session
}

// SessionStore wraps gorilla session store
type SessionStore struct {
	store *sessions.CookieStore
}

// InitSessionStore creates a new session store with a secret key
func (h *Handler) InitSessionStore() error {
	// Require a deployment-provided signing key.
	secret := []byte(os.Getenv("DDNS_SESSION_SECRET"))
	if len(secret) < 32 {
		return fmt.Errorf("DDNS_SESSION_SECRET must contain at least 32 bytes")
	}

	// Create cookie store
	store := sessions.NewCookieStore(secret)

	// Configure session options
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   24 * 60 * 60, // 24 hours default
		HttpOnly: true,
		Secure:   false, // Will be set to true per-request if HTTPS
		SameSite: http.SameSiteStrictMode,
	}

	h.SessionStore = &SessionStore{store: store}

	// Register types for session encoding
	registerSessionTypes.Do(func() {
		gob.Register(map[string]interface{}{})
		gob.Register([]interface{}{})
	})

	return nil
}

// Get retrieves a session
func (s *SessionStore) Get(r *http.Request, name string) (*Session, error) {
	sess, err := s.store.Get(r, name)
	if err != nil {
		return nil, err
	}
	return &Session{Session: sess}, nil
}

var registerSessionTypes sync.Once
