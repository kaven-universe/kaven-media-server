package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	Realm                  = "Kaven Media Server"
	DefaultNonceLifetime   = 5 * time.Minute
	DefaultReplayCacheSize = 65_536
	maxAuthorizationSize   = 8 * 1024
	allowedFutureSkew      = 30 * time.Second
)

type Authenticator struct {
	username      string
	ha1           [sha256.Size]byte
	nonceKey      [sha256.Size]byte
	opaque        string
	nonceLifetime time.Duration
	maxReplay     int
	now           func() time.Time
	counter       atomic.Uint64

	replayMutex sync.Mutex
	replays     map[string]time.Time
}

type nonceState uint8

const (
	nonceInvalid nonceState = iota
	nonceCurrent
	nonceStale
)

func New(username string, password []byte) (*Authenticator, error) {
	return newAuthenticator(username, password, rand.Reader, time.Now)
}

func newAuthenticator(username string, password []byte, random io.Reader, now func() time.Time) (*Authenticator, error) {
	if username == "" || len(password) == 0 {
		return nil, errors.New("create authenticator: username and password are required")
	}
	if random == nil || now == nil {
		return nil, errors.New("create authenticator: random source and clock are required")
	}
	authenticator := &Authenticator{
		username: username, nonceLifetime: DefaultNonceLifetime,
		maxReplay: DefaultReplayCacheSize, now: now, replays: make(map[string]time.Time),
	}
	if _, err := io.ReadFull(random, authenticator.nonceKey[:]); err != nil {
		return nil, fmt.Errorf("create authenticator nonce key: %w", err)
	}
	opaque := make([]byte, 16)
	if _, err := io.ReadFull(random, opaque); err != nil {
		return nil, fmt.Errorf("create authenticator opaque value: %w", err)
	}
	authenticator.opaque = hex.EncodeToString(opaque)
	hash := sha256.New()
	_, _ = io.WriteString(hash, username+":"+Realm+":")
	_, _ = hash.Write(password)
	copy(authenticator.ha1[:], hash.Sum(nil))
	return authenticator, nil
}

func (authenticator *Authenticator) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		valid, stale := authenticator.authenticate(request)
		if !valid {
			authenticator.challenge(writer, stale)
			return
		}
		if isUnsafeMethod(request.Method) && !sameOrigin(request) {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request.WithContext(withAdmin(request.Context())))
	})
}

func (authenticator *Authenticator) authenticate(request *http.Request) (bool, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > maxAuthorizationSize {
		return false, false
	}
	parameters, err := parseDigestAuthorization(values[0])
	if err != nil {
		return false, false
	}
	required := []string{"username", "realm", "nonce", "uri", "response", "algorithm", "qop", "nc", "cnonce", "opaque"}
	for _, name := range required {
		if parameters[name] == "" {
			return false, false
		}
	}
	if !constantStringEqual(parameters["username"], authenticator.username) ||
		!constantStringEqual(parameters["realm"], Realm) ||
		!strings.EqualFold(parameters["algorithm"], "SHA-256") || parameters["qop"] != "auth" ||
		!constantStringEqual(parameters["opaque"], authenticator.opaque) {
		return false, false
	}
	requestTarget := request.RequestURI
	if requestTarget == "" {
		requestTarget = request.URL.RequestURI()
	}
	if !constantStringEqual(parameters["uri"], requestTarget) {
		return false, false
	}
	if len(parameters["nc"]) != 8 {
		return false, false
	}
	if _, err := strconv.ParseUint(parameters["nc"], 16, 32); err != nil {
		return false, false
	}
	state, expires := authenticator.validateNonce(parameters["nonce"])
	if state == nonceInvalid {
		return false, false
	}

	expected := digestResponse(authenticator.ha1, request.Method, parameters["uri"], parameters["nonce"], parameters["nc"], parameters["cnonce"])
	provided, err := hex.DecodeString(parameters["response"])
	if err != nil || len(provided) != sha256.Size || subtle.ConstantTimeCompare(provided, expected[:]) != 1 {
		return false, false
	}
	if state == nonceStale {
		return false, true
	}
	replayKey := strings.Join([]string{
		parameters["username"], parameters["nonce"], parameters["cnonce"], parameters["nc"],
	}, "\x00")
	if !authenticator.acceptReplayKey(replayKey, expires) {
		return false, false
	}
	return true, false
}

func digestResponse(ha1 [sha256.Size]byte, method, uri, nonce, nonceCount, clientNonce string) [sha256.Size]byte {
	ha2 := sha256.Sum256([]byte(method + ":" + uri))
	return sha256.Sum256([]byte(
		hex.EncodeToString(ha1[:]) + ":" + nonce + ":" + nonceCount + ":" + clientNonce + ":auth:" + hex.EncodeToString(ha2[:]),
	))
}

func (authenticator *Authenticator) challenge(writer http.ResponseWriter, stale bool) {
	nonce := authenticator.newNonce()
	value := fmt.Sprintf(
		`Digest realm="%s", qop="auth", algorithm=SHA-256, nonce="%s", opaque="%s", charset=UTF-8`,
		Realm, nonce, authenticator.opaque,
	)
	if stale {
		value += ", stale=true"
	}
	writer.Header().Set("WWW-Authenticate", value)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Add("Vary", "Authorization")
	writer.WriteHeader(http.StatusUnauthorized)
}

func (authenticator *Authenticator) newNonce() string {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(authenticator.now().UTC().Unix()))
	binary.BigEndian.PutUint64(payload[8:], authenticator.counter.Add(1))
	mac := hmac.New(sha256.New, authenticator.nonceKey[:])
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func (authenticator *Authenticator) validateNonce(encoded string) (nonceState, time.Time) {
	value, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(value) != 16+sha256.Size {
		return nonceInvalid, time.Time{}
	}
	payload, signature := value[:16], value[16:]
	mac := hmac.New(sha256.New, authenticator.nonceKey[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nonceInvalid, time.Time{}
	}
	issued := time.Unix(int64(binary.BigEndian.Uint64(payload[:8])), 0).UTC()
	now := authenticator.now().UTC()
	if issued.After(now.Add(allowedFutureSkew)) {
		return nonceInvalid, time.Time{}
	}
	expires := issued.Add(authenticator.nonceLifetime)
	if now.After(expires) {
		return nonceStale, expires
	}
	return nonceCurrent, expires
}

func (authenticator *Authenticator) acceptReplayKey(key string, expires time.Time) bool {
	now := authenticator.now().UTC()
	authenticator.replayMutex.Lock()
	defer authenticator.replayMutex.Unlock()
	for existing, expiry := range authenticator.replays {
		if now.After(expiry) {
			delete(authenticator.replays, existing)
		}
	}
	if _, exists := authenticator.replays[key]; exists || len(authenticator.replays) >= authenticator.maxReplay {
		return false
	}
	authenticator.replays[key] = expires
	return true
}

func constantStringEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sameOrigin(request *http.Request) bool {
	values := request.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	origin, err := url.Parse(values[0])
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return strings.EqualFold(origin.Host, request.Host)
}

type contextKey struct{}

func withAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, true)
}

func IsAdmin(ctx context.Context) bool {
	value, _ := ctx.Value(contextKey{}).(bool)
	return value
}
