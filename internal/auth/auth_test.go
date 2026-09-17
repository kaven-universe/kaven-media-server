package auth

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const testPassword = "correct horse battery staple"

func TestLoginCreatesSessionAndLogoutRevokesIt(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	login := serveLogin(authenticator, "admin", testPassword, "")
	if login.Code != http.StatusOK || login.Body.String() != "{\"authenticated\":true}\n" {
		t.Fatalf("login = %d %q", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login)
	if cookie.Name != CookieName || cookie.Value == "" || cookie.Path != "/" || !cookie.HttpOnly || cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", cookie)
	}

	protected := authenticator.Protect(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !IsAdmin(request.Context()) {
			t.Error("protected request has no administrator identity")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	if response := serveWithCookie(protected, http.MethodGet, "/private", cookie, ""); response.Code != http.StatusNoContent {
		t.Fatalf("protected status = %d", response.Code)
	}
	if response := serveWithCookie(authenticator.SessionHandler(), http.MethodGet, "/api/v1/admin/session", cookie, ""); response.Code != http.StatusOK {
		t.Fatalf("session status = %d", response.Code)
	}

	logout := serveWithCookie(authenticator.LogoutHandler(), http.MethodPost, "/api/v1/admin/logout", cookie, "")
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", logout.Code)
	}
	cleared := responseCookie(t, logout)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("cleared cookie = %#v", cleared)
	}
	response := serveWithCookie(protected, http.MethodGet, "/private", cookie, "")
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("revoked session = %d, challenge %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestLoginRejectsInvalidAndMalformedCredentials(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	for _, credentials := range [][2]string{{"admin", "wrong password value"}, {"other", testPassword}} {
		response := serveLogin(authenticator, credentials[0], credentials[1], "")
		if response.Code != http.StatusUnauthorized || response.Body.String() != "{\"error\":\"invalid username or password\"}\n" {
			t.Fatalf("invalid login = %d %q", response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"username":"admin","password":"value","extra":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{}`))
	response = httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content type status = %d", response.Code)
	}
}

func TestLoginRejectsCrossOriginAndOversizedBodies(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	if response := serveLogin(authenticator, "admin", testPassword, "https://attacker.test"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login status = %d", response.Code)
	}

	body := `{"username":"admin","password":"` + strings.Repeat("x", int(maxLoginBody)) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized login status = %d", response.Code)
	}
}

func TestDuplicateSessionCookiesAreRejected(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	cookie := responseCookie(t, serveLogin(authenticator, "admin", testPassword, ""))
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.AddCookie(cookie)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	authenticator.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("duplicate cookies reached protected handler")
	})).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate cookie status = %d", response.Code)
	}
}

func TestLoginRateLimitExpires(t *testing.T) {
	authenticator, clock := testAuthenticator(t, Options{})
	for attempt := 0; attempt < DefaultMaxLoginFailures; attempt++ {
		if response := serveLogin(authenticator, "admin", "wrong password value", ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attempt+1, response.Code)
		}
	}
	limited := serveLogin(authenticator, "admin", testPassword, "")
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited login = %d, Retry-After %q", limited.Code, limited.Header().Get("Retry-After"))
	}
	*clock = clock.Add(DefaultLoginWindow)
	if response := serveLogin(authenticator, "admin", testPassword, ""); response.Code != http.StatusOK {
		t.Fatalf("login after window = %d %q", response.Code, response.Body.String())
	}
}

func TestConcurrentLoginWorkIsBounded(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	for range DefaultMaxConcurrentLogins {
		authenticator.loginSlots <- struct{}{}
	}
	if _, retryAfter, err := authenticator.login("admin", []byte(testPassword), "source"); !errors.Is(err, ErrLoginRateLimited) || retryAfter != time.Second {
		t.Fatalf("saturated login = retry %v, error %v", retryAfter, err)
	}
}

func TestSessionIdleAndAbsoluteExpiration(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		authenticator, clock := testAuthenticator(t, Options{})
		cookie := responseCookie(t, serveLogin(authenticator, "admin", testPassword, ""))
		*clock = clock.Add(DefaultIdleTimeout)
		if response := serveWithCookie(authenticator.SessionHandler(), http.MethodGet, "/api/v1/admin/session", cookie, ""); response.Code != http.StatusOK || response.Body.String() != "{\"authenticated\":false}\n" {
			t.Fatalf("idle session status = %d %q", response.Code, response.Body.String())
		}
	})

	t.Run("absolute", func(t *testing.T) {
		authenticator, clock := testAuthenticator(t, Options{})
		authenticator.idleTimeout = DefaultAbsoluteTimeout * 2
		cookie := responseCookie(t, serveLogin(authenticator, "admin", testPassword, ""))
		*clock = clock.Add(DefaultAbsoluteTimeout)
		if response := serveWithCookie(authenticator.SessionHandler(), http.MethodGet, "/api/v1/admin/session", cookie, ""); response.Code != http.StatusOK || response.Body.String() != "{\"authenticated\":false}\n" {
			t.Fatalf("absolute session status = %d %q", response.Code, response.Body.String())
		}
	})
}

func TestUnsafeRequestsRequireSameOrigin(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{})
	cookie := responseCookie(t, serveLogin(authenticator, "admin", testPassword, ""))
	handler := authenticator.Protect(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	if response := serveWithCookie(handler, http.MethodPost, "/private", cookie, "https://attacker.test"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", response.Code)
	}
	if response := serveWithCookie(handler, http.MethodPost, "/private", cookie, "https://example.test"); response.Code != http.StatusNoContent {
		t.Fatalf("same-origin status = %d", response.Code)
	}
	if response := serveWithCookie(authenticator.LogoutHandler(), http.MethodPost, "/api/v1/admin/logout", cookie, "https://attacker.test"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin logout status = %d", response.Code)
	}
}

func TestSecureSessionCookieOption(t *testing.T) {
	authenticator, _ := testAuthenticator(t, Options{SecureCookie: true})
	if cookie := responseCookie(t, serveLogin(authenticator, "admin", testPassword, "")); !cookie.Secure {
		t.Fatalf("cookie is not Secure: %#v", cookie)
	}

	authenticator, _ = testAuthenticator(t, Options{})
	body, _ := json.Marshal(loginRequest{Username: "admin", Password: testPassword})
	request := httptest.NewRequest(http.MethodPost, "https://example.test/api/v1/admin/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	if cookie := responseCookie(t, response); !cookie.Secure {
		t.Fatalf("direct TLS cookie is not Secure: %#v", cookie)
	}
}

func TestLongPasswordIsSupported(t *testing.T) {
	password := strings.Repeat("long-password-", 80)
	clock := time.Date(2026, time.September, 9, 1, 2, 3, 0, time.UTC)
	authenticator, err := newAuthenticator("admin", []byte(password), Options{}, bytes.NewReader(bytes.Repeat([]byte{0x42}, 1024)), func() time.Time { return clock }, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if response := serveLogin(authenticator, "admin", password, ""); response.Code != http.StatusOK {
		t.Fatalf("long-password login = %d %q", response.Code, response.Body.String())
	}
}

func TestNewRejectsInvalidDependencies(t *testing.T) {
	if _, err := newAuthenticator("admin", []byte(testPassword), Options{}, nil, time.Now, bcrypt.MinCost); err == nil {
		t.Fatal("nil random source succeeded")
	}
	if _, err := newAuthenticator("admin", []byte(testPassword), Options{}, bytes.NewReader(nil), nil, bcrypt.MinCost); err == nil {
		t.Fatal("nil clock succeeded")
	}
	authenticator, _ := testAuthenticator(t, Options{})
	authenticator.random = errorReader{}
	if _, _, err := authenticator.login("admin", []byte(testPassword), "source"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("random failure = %v", err)
	}
}

func testAuthenticator(t *testing.T, options Options) (*Authenticator, *time.Time) {
	t.Helper()
	clock := time.Date(2026, time.September, 9, 1, 2, 3, 0, time.UTC)
	authenticator, err := newAuthenticator(
		"admin", []byte(testPassword), options,
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 4096)), func() time.Time { return clock }, bcrypt.MinCost,
	)
	if err != nil {
		t.Fatal(err)
	}
	return authenticator, &clock
}

func serveLogin(authenticator *Authenticator, username, password, origin string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(loginRequest{Username: username, Password: password})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.1:12345"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	authenticator.LoginHandler().ServeHTTP(response, request)
	return response
}

func serveWithCookie(handler http.Handler, method, target string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Host = "example.test"
	request.AddCookie(cookie)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseCookie(t *testing.T, response *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("response cookies = %#v", cookies)
	}
	return cookies[0]
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random unavailable")
}
