package handler

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/fxamacker/cbor/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

type authHarness struct {
	t       *testing.T
	h       *Handler
	e       *echo.Echo
	cookies map[string]*http.Cookie
}

func newAuthHarness(t *testing.T, google bool) *authHarness {
	t.Helper()
	t.Setenv("DDNS_SESSION_SECRET", strings.Repeat("s", 40))
	t.Setenv("DDNS_AUTH_ORIGIN", "https://ddns.example.test")
	t.Setenv("DDNS_GOOGLE_CLIENT_ID", "")
	t.Setenv("DDNS_GOOGLE_CLIENT_SECRET", "")
	t.Setenv("DDNS_GOOGLE_ALLOWED_EMAILS", "")
	if google {
		t.Setenv("DDNS_GOOGLE_CLIENT_ID", "test-client")
		t.Setenv("DDNS_GOOGLE_CLIENT_SECRET", "fixture-secret")
		t.Setenv("DDNS_GOOGLE_ALLOWED_EMAILS", "admin@example.test")
	}
	h := &Handler{DB: testDB(t), Title: "Test DDNS", Config: Envs{AdminLogin: "admin:{SHA}8cbDCzZIvb6aVJMgVZ1gQxRoUP0="}}
	if err := h.InitSessionStore(); err != nil {
		t.Fatal(err)
	}
	if err := h.InitAdminAuth(); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	h.AdminAuthRoutes(e)
	e.GET("/protected", func(c echo.Context) error {
		if !h.IsAuthenticated(c) {
			return c.NoContent(401)
		}
		return c.NoContent(200)
	})
	return &authHarness{t, h, e, map[string]*http.Cookie{}}
}
func (a *authHarness) login(p authPrincipal, age time.Duration) {
	a.t.Helper()
	r := httptest.NewRequest("GET", "https://ddns.example.test/", nil)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	c := a.e.NewContext(r, w)
	if err := a.h.newAdminSession(c, p, false); err != nil {
		a.t.Fatal(err)
	}
	for _, cookie := range w.Result().Cookies() {
		a.cookies[cookie.Name] = cookie
	}
	if age > 0 {
		for _, cookie := range a.cookies {
			r.AddCookie(cookie)
		}
		w = httptest.NewRecorder()
		c = a.e.NewContext(r, w)
		s, _ := a.h.GetSession(c)
		s.Values[SessionCreatedAt] = time.Now().Add(-age).Unix()
		if err := s.Save(r, w); err != nil {
			a.t.Fatal(err)
		}
		for _, cookie := range w.Result().Cookies() {
			a.cookies[cookie.Name] = cookie
		}
	}
}
func (a *authHarness) request(method, path string, body interface{}, origin string) *httptest.ResponseRecorder {
	a.t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "https://ddns.example.test"+path, strings.NewReader(string(raw)))
	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "192.0.2.1:4000"
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, cookie := range a.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.e.ServeHTTP(w, r)
	for _, cookie := range w.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(a.cookies, cookie.Name)
		} else {
			a.cookies[cookie.Name] = cookie
		}
	}
	return w
}
func expectStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
}
func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
func clientData(kind, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]interface{}{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}
func optionsChallenge(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	expectStatus(t, w, 200)
	var v struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.PublicKey.Challenge
}
func registerResponse(t *testing.T, key *ecdsa.PrivateKey, id []byte, challenge, origin string, uv bool) map[string]interface{} {
	t.Helper()
	pub, err := cbor.Marshal(map[int]interface{}{1: 2, 3: -7, -1: 1, -2: key.X.FillBytes(make([]byte, 32)), -3: key.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	rp := sha256.Sum256([]byte("ddns.example.test"))
	data := append([]byte{}, rp[:]...)
	flags := byte(0x41)
	if uv {
		flags |= 4
	}
	data = append(data, flags, 0, 0, 0, 0)
	data = append(data, make([]byte, 16)...)
	data = binary.BigEndian.AppendUint16(data, uint16(len(id)))
	data = append(data, id...)
	data = append(data, pub...)
	att, err := cbor.Marshal(map[string]interface{}{"fmt": "none", "attStmt": map[string]interface{}{}, "authData": data})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]interface{}{"id": b64(id), "rawId": b64(id), "type": "public-key", "response": map[string]interface{}{"clientDataJSON": b64(clientData("webauthn.create", challenge, origin)), "attestationObject": b64(att)}, "clientExtensionResults": map[string]interface{}{}}
}
func assertionResponse(t *testing.T, key *ecdsa.PrivateKey, id, handle []byte, challenge, origin string, count uint32) map[string]interface{} {
	t.Helper()
	client := clientData("webauthn.get", challenge, origin)
	rp := sha256.Sum256([]byte("ddns.example.test"))
	auth := append([]byte{}, rp[:]...)
	auth = append(auth, 5)
	auth = binary.BigEndian.AppendUint32(auth, count)
	hash := sha256.Sum256(client)
	signed := append(append([]byte{}, auth...), hash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]interface{}{"id": b64(id), "rawId": b64(id), "type": "public-key", "response": map[string]interface{}{"clientDataJSON": b64(client), "authenticatorData": b64(auth), "signature": b64(signature), "userHandle": b64(handle)}, "clientExtensionResults": map[string]interface{}{}}
}

const testOrigin = "https://ddns.example.test"

func TestPasskeyCompleteLifecycle(t *testing.T) {
	a := newAuthHarness(t, false)
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 0)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := []byte("fixture-credential-id-32bytes-long")
	begin := a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "Test key"}, testOrigin)
	challenge := optionsChallenge(t, begin)
	response := registerResponse(t, key, id, challenge, testOrigin, true)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/finish", response, testOrigin), 200)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/finish", response, testOrigin), 400)
	delete(a.cookies, SessionName)
	begin = a.request("POST", "/@/auth/passkey/login/begin", nil, testOrigin)
	challenge = optionsChallenge(t, begin)
	response = assertionResponse(t, key, id, a.h.AdminAuth.Handle, challenge, testOrigin, 1)
	expectStatus(t, a.request("POST", "/@/auth/passkey/login/finish", response, testOrigin), 200)
	expectStatus(t, a.request("GET", "/protected", nil, ""), 200)
	expectStatus(t, a.request("POST", "/@/auth/passkey/login/finish", response, testOrigin), 400)
	expectStatus(t, a.request("POST", "/@/auth/passkey/"+b64(id)+"/revoke", nil, testOrigin), 200)
	expectStatus(t, a.request("GET", "/protected", nil, ""), 401)
	challenge = optionsChallenge(t, a.request("POST", "/@/auth/passkey/login/begin", nil, testOrigin))
	expectStatus(t, a.request("POST", "/@/auth/passkey/login/finish", assertionResponse(t, key, id, a.h.AdminAuth.Handle, challenge, testOrigin, 2), testOrigin), 401)
}
func TestPasskeyRejectsOriginUVAndStaleSessions(t *testing.T) {
	a := newAuthHarness(t, false)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, testOrigin), 401)
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 10*time.Minute)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, testOrigin), 403)
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 0)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, "https://evil.example"), 403)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, ""), 403)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := []byte("fixture-bad-registration")
	for _, tc := range []struct {
		origin string
		uv     bool
	}{{"https://evil.example", true}, {testOrigin, false}} {
		challenge := optionsChallenge(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, testOrigin))
		expectStatus(t, a.request("POST", "/@/auth/passkey/register/finish", registerResponse(t, key, id, challenge, tc.origin, tc.uv), testOrigin), 400)
	}
	challenge := optionsChallenge(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "key"}, testOrigin))
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 0)
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/finish", registerResponse(t, key, id, challenge, testOrigin, true), testOrigin), 403)
}
func TestGoogleVerifiedIdentityAndRevocation(t *testing.T) {
	a := newAuthHarness(t, true)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	a.h.AdminAuth.Verifier = oidc.NewVerifier("https://accounts.google.com", &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "test-client"})
	var nonce, subject, email, issuer, audience, pkce string
	verified := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.FormValue("code_verifier") != pkce {
			t.Error("missing PKCE verifier")
		}
		claims := jwt.MapClaims{"iss": issuer, "aud": audience, "sub": subject, "email": email, "email_verified": verified, "nonce": nonce, "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix()}
		token, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "fixture", "token_type": "Bearer", "id_token": token})
	}))
	defer server.Close()
	a.h.AdminAuth.OAuth.Endpoint.TokenURL = server.URL
	start := func() string {
		w := a.request("GET", "/@/auth/google", nil, "")
		expectStatus(t, w, 302)
		u, _ := url.Parse(w.Header().Get("Location"))
		q := u.Query()
		nonce = q.Get("nonce")
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			t.Fatal("PKCE missing")
		}
		f := a.h.AdminAuth.flows[a.cookies[authFlowCookie].Value]
		pkce = f.Verifier
		return q.Get("state")
	}
	issuer = "https://accounts.google.com"
	audience = "test-client"
	subject = "google-stable-id"
	email = "admin@example.test"
	state := start()
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 200)
	expectStatus(t, a.request("GET", "/protected", nil, ""), 200)
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 400)
	delete(a.h.AdminAuth.AllowedEmails, email)
	expectStatus(t, a.request("GET", "/protected", nil, ""), 401)
	a.h.AdminAuth.AllowedEmails[email] = true
	state = start()
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state=wrong", nil, ""), 400)
	state = start()
	nonce = "wrong"
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 401)
	state = start()
	verified = false
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 401)
	verified = true
	state = start()
	audience = "other-client"
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 401)
	audience = "test-client"
	state = start()
	issuer = "https://evil.example"
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 401)
	issuer = "https://accounts.google.com"
	state = start()
	email = "other@example.test"
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 403)
	email = "admin@example.test"
	state = start()
	subject = "replacement-account"
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 403)
}

func TestAuthConfigurationAndFlowBinding(t *testing.T) {
	a := newAuthHarness(t, true)
	for _, origin := range []string{"http://ddns.example.test", "https://ddns.example.test/path", "https://user@ddns.example.test", "https://ddns.example.test?x=y"} {
		t.Setenv("DDNS_AUTH_ORIGIN", origin)
		if err := a.h.InitAdminAuth(); err == nil {
			t.Fatalf("accepted unsafe origin %q", origin)
		}
	}
	t.Setenv("DDNS_AUTH_ORIGIN", testOrigin)
	t.Setenv("DDNS_GOOGLE_ALLOWED_EMAILS", "")
	if err := a.h.InitAdminAuth(); err == nil {
		t.Fatal("accepted incomplete Google configuration")
	}
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 0)
	if a.cookies[SessionName].SameSite != http.SameSiteStrictMode || !a.cookies[SessionName].Secure || !a.cookies[SessionName].HttpOnly {
		t.Fatal("admin cookie protections changed")
	}
	w := a.request("GET", "/@/auth/google", nil, "")
	expectStatus(t, w, 302)
	u, _ := url.Parse(w.Header().Get("Location"))
	state := u.Query().Get("state")
	delete(a.cookies, authFlowCookie)
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+state, nil, ""), 400)
	a.request("GET", "/@/auth/google", nil, "")
	id := a.cookies[authFlowCookie].Value
	flow := a.h.AdminAuth.flows[id]
	flow.Expires = time.Now().Add(-time.Second)
	a.h.AdminAuth.flows[id] = flow
	expectStatus(t, a.request("GET", "/@/auth/google/callback?code=fixture&state="+flow.State, nil, ""), 400)
}

func TestPasskeyRejectsForgedAssertions(t *testing.T) {
	a := newAuthHarness(t, false)
	a.login(authPrincipal{Provider: "password", Subject: "admin"}, 0)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	id := []byte("fixture-forgery-test")
	challenge := optionsChallenge(t, a.request("POST", "/@/auth/passkey/register/begin", map[string]string{"name": "test"}, testOrigin))
	expectStatus(t, a.request("POST", "/@/auth/passkey/register/finish", registerResponse(t, key, id, challenge, testOrigin, true), testOrigin), 200)
	delete(a.cookies, SessionName)
	for _, tc := range []struct {
		key    *ecdsa.PrivateKey
		handle []byte
		origin string
	}{{otherKey, a.h.AdminAuth.Handle, testOrigin}, {key, []byte("wrong-user"), testOrigin}, {key, a.h.AdminAuth.Handle, "https://evil.example"}} {
		challenge = optionsChallenge(t, a.request("POST", "/@/auth/passkey/login/begin", nil, testOrigin))
		expectStatus(t, a.request("POST", "/@/auth/passkey/login/finish", assertionResponse(t, tc.key, id, tc.handle, challenge, tc.origin, 1), testOrigin), 401)
		expectStatus(t, a.request("GET", "/protected", nil, ""), 401)
	}
}
