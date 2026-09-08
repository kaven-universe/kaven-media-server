package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPassword = "correct horse battery staple"

func TestAuthenticatorChallengesAndAcceptsSHA256Digest(t *testing.T) {
	authenticator, _ := testAuthenticator(t)
	called := false
	handler := authenticator.Protect(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if !IsAdmin(request.Context()) {
			t.Error("authenticated request has no admin authorization")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))

	challenge := serveProtected(handler, http.MethodGet, "/private?value=1", "", "")
	if challenge.Code != http.StatusUnauthorized || challenge.Body.Len() != 0 {
		t.Fatalf("challenge = %d %q", challenge.Code, challenge.Body.String())
	}
	parameters, err := parseDigestAuthorization(challenge.Header().Get("WWW-Authenticate"))
	if err != nil {
		t.Fatal(err)
	}
	if parameters["realm"] != Realm || parameters["algorithm"] != "SHA-256" || parameters["qop"] != "auth" ||
		parameters["charset"] != "UTF-8" || parameters["nonce"] == "" || parameters["opaque"] == "" {
		t.Fatalf("challenge parameters = %#v", parameters)
	}
	authorization := digestAuthorization(authenticator, http.MethodGet, "/private?value=1", parameters["nonce"], "00000001", "client-nonce", "admin", testPassword, "SHA-256")
	response := serveProtected(handler, http.MethodGet, "/private?value=1", authorization, "")
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("response = %d, called = %t", response.Code, called)
	}

	replay := serveProtected(handler, http.MethodGet, "/private?value=1", authorization, "")
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", replay.Code)
	}
}

func TestAuthenticatorRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name      string
		username  string
		password  string
		algorithm string
		uri       string
		tamper    bool
	}{
		{name: "wrong username", username: "other", password: testPassword, algorithm: "SHA-256", uri: "/private"},
		{name: "wrong password", username: "admin", password: "another long password", algorithm: "SHA-256", uri: "/private"},
		{name: "MD5 downgrade", username: "admin", password: testPassword, algorithm: "MD5", uri: "/private"},
		{name: "wrong URI", username: "admin", password: testPassword, algorithm: "SHA-256", uri: "/different"},
		{name: "tampered nonce", username: "admin", password: testPassword, algorithm: "SHA-256", uri: "/private", tamper: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator, _ := testAuthenticator(t)
			nonce := authenticator.newNonce()
			if test.tamper {
				replacement := byte('A')
				if nonce[len(nonce)-1] == replacement {
					replacement = 'B'
				}
				nonce = nonce[:len(nonce)-1] + string(replacement)
			}
			header := digestAuthorization(authenticator, http.MethodGet, test.uri, nonce, "00000001", "cnonce", test.username, test.password, test.algorithm)
			handler := authenticator.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("invalid credentials reached protected handler")
			}))
			response := serveProtected(handler, http.MethodGet, "/private", header, "")
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
		})
	}
}

func TestAuthenticatorMarksValidExpiredNonceStale(t *testing.T) {
	authenticator, clock := testAuthenticator(t)
	nonce := authenticator.newNonce()
	header := digestAuthorization(authenticator, http.MethodGet, "/private", nonce, "00000001", "cnonce", "admin", testPassword, "SHA-256")
	*clock = clock.Add(DefaultNonceLifetime + time.Second)
	handler := authenticator.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expired nonce reached protected handler")
	}))
	response := serveProtected(handler, http.MethodGet, "/private", header, "")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "stale=true") {
		t.Fatalf("status = %d, challenge = %q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}

func TestAuthenticatorChecksOriginOnUnsafeMethods(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		status int
	}{
		{name: "same origin", origin: "https://example.test", status: http.StatusNoContent},
		{name: "non browser", status: http.StatusNoContent},
		{name: "cross site", origin: "https://attacker.test", status: http.StatusForbidden},
		{name: "null", origin: "null", status: http.StatusForbidden},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator, _ := testAuthenticator(t)
			nonce := authenticator.newNonce()
			header := digestAuthorization(authenticator, http.MethodPost, "/private", nonce, fmt.Sprintf("%08x", index+1), "cnonce", "admin", testPassword, "SHA-256")
			handler := authenticator.Protect(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}))
			response := serveProtected(handler, http.MethodPost, "/private", header, test.origin)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestAuthenticatorAcceptsDistinctConcurrentNonceCounts(t *testing.T) {
	authenticator, _ := testAuthenticator(t)
	nonce := authenticator.newNonce()
	handler := authenticator.Protect(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	const count = 50
	statuses := make([]int, count)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			nc := fmt.Sprintf("%08x", index+1)
			header := digestAuthorization(authenticator, http.MethodGet, "/private", nonce, nc, "shared-cnonce", "admin", testPassword, "SHA-256")
			statuses[index] = serveProtected(handler, http.MethodGet, "/private", header, "").Code
		}(index)
	}
	wait.Wait()
	for index, status := range statuses {
		if status != http.StatusNoContent {
			t.Errorf("request %d status = %d", index, status)
		}
	}
}

func TestParseDigestAuthorizationRejectsMalformedInput(t *testing.T) {
	for _, header := range []string{
		"Basic value",
		`Digest username="admin`,
		`Digest username="admin", username="other"`,
		`Digest username="admin",`,
		"Digest username=bad value",
		"Digest username=\"bad\nvalue\"",
	} {
		if _, err := parseDigestAuthorization(header); err == nil {
			t.Errorf("header %q parsed successfully", header)
		}
	}
}

func TestDigestResponseMatchesRFC7616SHA256Example(t *testing.T) {
	ha1 := sha256.Sum256([]byte("Mufasa:http-auth@example.org:Circle of Life"))
	response := digestResponse(
		ha1, http.MethodGet, "/dir/index.html", "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v",
		"00000001", "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ",
	)
	if got, want := hex.EncodeToString(response[:]), "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"; got != want {
		t.Fatalf("response = %q, want RFC 7616 value %q", got, want)
	}
}

func testAuthenticator(t *testing.T) (*Authenticator, *time.Time) {
	t.Helper()
	clock := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	authenticator, err := newAuthenticator(
		"admin", []byte(testPassword), bytes.NewReader(bytes.Repeat([]byte{0x42}, sha256.Size+16)),
		func() time.Time { return clock },
	)
	if err != nil {
		t.Fatal(err)
	}
	return authenticator, &clock
}

func digestAuthorization(authenticator *Authenticator, method, uri, nonce, nc, cnonce, username, password, algorithm string) string {
	ha1 := sha256.Sum256([]byte(username + ":" + Realm + ":" + password))
	ha2 := sha256.Sum256([]byte(method + ":" + uri))
	response := sha256.Sum256([]byte(
		hex.EncodeToString(ha1[:]) + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + hex.EncodeToString(ha2[:]),
	))
	return fmt.Sprintf(
		`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=%s, qop=auth, nc=%s, cnonce="%s", opaque="%s"`,
		username, Realm, nonce, uri, hex.EncodeToString(response[:]), algorithm, nc, cnonce, authenticator.opaque,
	)
}

func serveProtected(handler http.Handler, method, target, authorization, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Host = "example.test"
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
