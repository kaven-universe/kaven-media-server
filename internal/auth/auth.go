package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName                       = "kaven_media_session"
	DefaultIdleTimeout               = 30 * time.Minute
	DefaultAbsoluteTimeout           = 12 * time.Hour
	DefaultLoginWindow               = 5 * time.Minute
	DefaultMaxLoginFailures          = 5
	DefaultMaxConcurrentLogins       = 4
	DefaultMaxSessions               = 128
	DefaultMaxLoginSources           = 1024
	MinRememberDuration              = 24 * time.Hour
	MaxRememberDuration              = 365 * 24 * time.Hour
	DefaultRememberDuration          = 30 * 24 * time.Hour
	defaultPasswordCost              = bcrypt.DefaultCost
	maxLoginBody               int64 = 8 * 1024
	sessionTokenBytes                = 32
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrLoginRateLimited   = errors.New("too many login attempts")
)

type Options struct {
	SecureCookie     bool
	SessionStore     PersistentSessionStore
	RememberDuration time.Duration
}

type PersistentSessionStore interface {
	Create(ctx context.Context, tokenHash, credentialKey []byte, createdAt, expiresAt time.Time) (created bool, err error)
	Get(ctx context.Context, tokenHash, credentialKey []byte, now time.Time) (expiresAt time.Time, exists bool, err error)
	Delete(ctx context.Context, tokenHash []byte) error
}

type Authenticator struct {
	username         string
	passwordHash     []byte
	credentialKey    [sha256.Size]byte
	random           io.Reader
	now              func() time.Time
	secureCookie     bool
	sessionStore     PersistentSessionStore
	rememberDuration time.Duration
	idleTimeout      time.Duration
	absoluteTimeout  time.Duration
	loginWindow      time.Duration
	maxLoginFailures int
	maxSessions      int
	maxLoginSources  int
	mutex            sync.Mutex
	sessions         map[[sha256.Size]byte]session
	loginFailures    map[string]loginFailure
	loginSlots       chan struct{}
}

type session struct {
	created    time.Time
	lastSeen   time.Time
	persistent bool
	expiresAt  time.Time
}

type loginFailure struct {
	count       int
	windowStart time.Time
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

func New(username string, password []byte) (*Authenticator, error) {
	return NewWithOptions(username, password, Options{})
}

func NewWithOptions(username string, password []byte, options Options) (*Authenticator, error) {
	return newAuthenticator(username, password, options, rand.Reader, time.Now, defaultPasswordCost)
}

func NewFromPasswordHash(username string, passwordHash []byte, options Options) (*Authenticator, error) {
	if username == "" || len(passwordHash) == 0 {
		return nil, errors.New("create authenticator: username and password hash are required")
	}
	if _, err := bcrypt.Cost(passwordHash); err != nil {
		return nil, fmt.Errorf("create authenticator: invalid password hash: %w", err)
	}
	if err := validateRememberDuration(options.RememberDuration); err != nil {
		return nil, err
	}
	return newAuthenticatorFromHash(username, passwordHash, storedCredentialKey(username, passwordHash), options, rand.Reader, time.Now), nil
}

func HashPassword(password []byte) ([]byte, error) {
	return hashPassword(password, defaultPasswordCost)
}

func (authenticator *Authenticator) VerifyPassword(password []byte) (bool, error) {
	select {
	case authenticator.loginSlots <- struct{}{}:
		defer func() { <-authenticator.loginSlots }()
	default:
		return false, ErrLoginRateLimited
	}
	passwordDigest := sha256.Sum256(password)
	valid := bcrypt.CompareHashAndPassword(authenticator.passwordHash, passwordDigest[:]) == nil
	clear(passwordDigest[:])
	return valid, nil
}

func (authenticator *Authenticator) RevokeAllSessions() {
	authenticator.mutex.Lock()
	clear(authenticator.sessions)
	authenticator.mutex.Unlock()
}

func (authenticator *Authenticator) Username() string {
	return authenticator.username
}

func (authenticator *Authenticator) RememberDuration() time.Duration {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	return authenticator.rememberDuration
}

func (authenticator *Authenticator) SetRememberDuration(duration time.Duration) error {
	if err := validateRememberDuration(duration); err != nil {
		return err
	}
	authenticator.mutex.Lock()
	authenticator.rememberDuration = normalizedRememberDuration(duration)
	authenticator.mutex.Unlock()
	return nil
}

func newAuthenticator(username string, password []byte, options Options, random io.Reader, now func() time.Time, passwordCost int) (*Authenticator, error) {
	if username == "" || len(password) == 0 {
		return nil, errors.New("create authenticator: username and password are required")
	}
	if random == nil || now == nil {
		return nil, errors.New("create authenticator: random source and clock are required")
	}
	if err := validateRememberDuration(options.RememberDuration); err != nil {
		return nil, err
	}
	passwordHash, err := hashPassword(password, passwordCost)
	if err != nil {
		return nil, fmt.Errorf("create authenticator password verifier: %w", err)
	}
	return newAuthenticatorFromHash(username, passwordHash, configuredCredentialKey(username, password), options, random, now), nil
}

func hashPassword(password []byte, passwordCost int) ([]byte, error) {
	passwordDigest := sha256.Sum256(password)
	passwordHash, err := bcrypt.GenerateFromPassword(passwordDigest[:], passwordCost)
	clear(passwordDigest[:])
	return passwordHash, err
}

func newAuthenticatorFromHash(username string, passwordHash []byte, credentialKey [sha256.Size]byte, options Options, random io.Reader, now func() time.Time) *Authenticator {
	return &Authenticator{
		username: username, passwordHash: append([]byte(nil), passwordHash...), credentialKey: credentialKey, random: random, now: now,
		secureCookie: options.SecureCookie, sessionStore: options.SessionStore,
		rememberDuration: normalizedRememberDuration(options.RememberDuration),
		idleTimeout:      DefaultIdleTimeout, absoluteTimeout: DefaultAbsoluteTimeout,
		loginWindow: DefaultLoginWindow, maxLoginFailures: DefaultMaxLoginFailures,
		maxSessions: DefaultMaxSessions, maxLoginSources: DefaultMaxLoginSources,
		sessions: make(map[[sha256.Size]byte]session), loginFailures: make(map[string]loginFailure),
		loginSlots: make(chan struct{}, DefaultMaxConcurrentLogins),
	}
}

func (authenticator *Authenticator) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !authenticator.authenticateRequest(request) {
			authenticator.clearSessionCookie(writer, request)
			writeJSONError(writer, http.StatusUnauthorized, "authentication required")
			return
		}
		if isUnsafeMethod(request.Method) && !IsSameOrigin(request) {
			writeJSONError(writer, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next.ServeHTTP(writer, request.WithContext(withAdmin(request.Context())))
	})
}

func (authenticator *Authenticator) LoginHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if !IsSameOrigin(request) {
			writeJSONError(writer, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeJSONError(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxLoginBody)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var credentials loginRequest
		if err := decoder.Decode(&credentials); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid login request")
			return
		}
		if err := ensureJSONEnd(decoder); err != nil {
			writeJSONError(writer, http.StatusBadRequest, "invalid login request")
			return
		}
		rememberFor := time.Duration(0)
		if credentials.Remember {
			rememberFor = authenticator.RememberDuration()
		}
		password := []byte(credentials.Password)
		credentials.Password = ""
		token, expiresAt, retryAfter, err := authenticator.loginWithPersistence(
			request.Context(), credentials.Username, password, remoteSource(request.RemoteAddr), rememberFor,
		)
		clear(password)
		if errors.Is(err, ErrLoginRateLimited) {
			retrySeconds := max(int64(1), int64((retryAfter+time.Second-1)/time.Second))
			writer.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
			writeJSONError(writer, http.StatusTooManyRequests, ErrLoginRateLimited.Error())
			return
		}
		if errors.Is(err, ErrInvalidCredentials) {
			writeJSONError(writer, http.StatusUnauthorized, ErrInvalidCredentials.Error())
			return
		}
		if err != nil {
			slog.Error("create administrator session", "error", err)
			writeJSONError(writer, http.StatusInternalServerError, "login failed")
			return
		}
		authenticator.setSessionCookie(writer, request, token, expiresAt)
		writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": true})
	})
}

func (authenticator *Authenticator) SessionHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		authenticated := authenticator.authenticateRequest(request)
		if !authenticated {
			authenticator.clearSessionCookie(writer, request)
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": authenticated})
	})
}

func (authenticator *Authenticator) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if !IsSameOrigin(request) {
			writeJSONError(writer, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		authenticator.revokeRequest(request)
		authenticator.clearSessionCookie(writer, request)
		writer.WriteHeader(http.StatusNoContent)
	})
}

func (authenticator *Authenticator) login(username string, password []byte, source string) (string, time.Duration, error) {
	token, _, retryAfter, err := authenticator.loginWithPersistence(context.Background(), username, password, source, 0)
	return token, retryAfter, err
}

func (authenticator *Authenticator) loginWithPersistence(ctx context.Context, username string, password []byte, source string, rememberFor time.Duration) (string, time.Time, time.Duration, error) {
	now := authenticator.now().UTC()
	if retryAfter, blocked := authenticator.loginBlocked(source, now); blocked {
		return "", time.Time{}, retryAfter, ErrLoginRateLimited
	}
	select {
	case authenticator.loginSlots <- struct{}{}:
		defer func() { <-authenticator.loginSlots }()
	default:
		return "", time.Time{}, time.Second, ErrLoginRateLimited
	}
	passwordDigest := sha256.Sum256(password)
	passwordValid := bcrypt.CompareHashAndPassword(authenticator.passwordHash, passwordDigest[:]) == nil
	clear(passwordDigest[:])
	usernameValid := constantStringEqual(username, authenticator.username)
	if !passwordValid || !usernameValid {
		authenticator.recordLoginFailure(source, now)
		return "", time.Time{}, 0, ErrInvalidCredentials
	}
	authenticator.clearLoginFailures(source)
	token, expiresAt, err := authenticator.createSession(ctx, now, rememberFor)
	if err != nil {
		return "", time.Time{}, 0, err
	}
	return token, expiresAt, 0, nil
}

func (authenticator *Authenticator) loginBlocked(source string, now time.Time) (time.Duration, bool) {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	authenticator.cleanFailuresLocked(now)
	failure, exists := authenticator.loginFailures[source]
	if !exists && len(authenticator.loginFailures) >= authenticator.maxLoginSources {
		return authenticator.loginWindow, true
	}
	if !exists || failure.count < authenticator.maxLoginFailures {
		return 0, false
	}
	retryAfter := failure.windowStart.Add(authenticator.loginWindow).Sub(now)
	return max(time.Second, retryAfter), true
}

func (authenticator *Authenticator) recordLoginFailure(source string, now time.Time) {
	authenticator.mutex.Lock()
	defer authenticator.mutex.Unlock()
	authenticator.cleanFailuresLocked(now)
	failure, exists := authenticator.loginFailures[source]
	if !exists {
		if len(authenticator.loginFailures) >= authenticator.maxLoginSources {
			return
		}
		failure.windowStart = now
	}
	failure.count++
	authenticator.loginFailures[source] = failure
}

func (authenticator *Authenticator) clearLoginFailures(source string) {
	authenticator.mutex.Lock()
	delete(authenticator.loginFailures, source)
	authenticator.mutex.Unlock()
}

func (authenticator *Authenticator) cleanFailuresLocked(now time.Time) {
	for source, failure := range authenticator.loginFailures {
		if !now.Before(failure.windowStart.Add(authenticator.loginWindow)) {
			delete(authenticator.loginFailures, source)
		}
	}
}

func (authenticator *Authenticator) createSession(ctx context.Context, now time.Time, rememberFor time.Duration) (string, time.Time, error) {
	for range 3 {
		value := make([]byte, sessionTokenBytes)
		if _, err := io.ReadFull(authenticator.random, value); err != nil {
			return "", time.Time{}, fmt.Errorf("create admin session token: %w", err)
		}
		token := base64.RawURLEncoding.EncodeToString(value)
		key := sha256.Sum256(value)
		clear(value)
		authenticator.mutex.Lock()
		authenticator.cleanSessionsLocked(now)
		_, duplicate := authenticator.sessions[key]
		authenticator.mutex.Unlock()
		if duplicate {
			continue
		}

		expiresAt := time.Time{}
		if rememberFor > 0 {
			expiresAt = now.Add(rememberFor)
			if authenticator.sessionStore == nil {
				return "", time.Time{}, errors.New("persistent administrator sessions are unavailable")
			}
			created, err := authenticator.sessionStore.Create(ctx, key[:], authenticator.credentialKey[:], now, expiresAt)
			if err != nil {
				return "", time.Time{}, fmt.Errorf("persist administrator session: %w", err)
			}
			if !created {
				continue
			}
		}

		authenticator.mutex.Lock()
		authenticator.cleanSessionsLocked(now)
		if _, duplicate := authenticator.sessions[key]; duplicate {
			authenticator.mutex.Unlock()
			if rememberFor > 0 {
				if err := authenticator.sessionStore.Delete(ctx, key[:]); err != nil {
					return "", time.Time{}, fmt.Errorf("remove colliding administrator session: %w", err)
				}
			}
			continue
		}
		if len(authenticator.sessions) >= authenticator.maxSessions {
			authenticator.evictOldestSessionLocked()
		}
		authenticator.sessions[key] = session{created: now, lastSeen: now, persistent: rememberFor > 0, expiresAt: expiresAt}
		authenticator.mutex.Unlock()
		return token, expiresAt, nil
	}
	return "", time.Time{}, errors.New("create admin session token: repeated random collision or persistent store failure")
}

func (authenticator *Authenticator) authenticateRequest(request *http.Request) bool {
	cookies := request.CookiesNamed(CookieName)
	if len(cookies) != 1 {
		return false
	}
	key, ok := sessionKey(cookies[0].Value)
	if !ok {
		return false
	}
	now := authenticator.now().UTC()
	authenticator.mutex.Lock()
	authenticator.cleanSessionsLocked(now)
	current, exists := authenticator.sessions[key]
	if exists {
		current.lastSeen = now
		authenticator.sessions[key] = current
		authenticator.mutex.Unlock()
		return true
	}
	authenticator.mutex.Unlock()
	if authenticator.sessionStore == nil {
		return false
	}
	expiresAt, exists, err := authenticator.sessionStore.Get(request.Context(), key[:], authenticator.credentialKey[:], now)
	if err != nil {
		slog.Error("read persistent administrator session", "error", err)
		return false
	}
	if !exists {
		return false
	}
	authenticator.mutex.Lock()
	if len(authenticator.sessions) >= authenticator.maxSessions {
		authenticator.evictOldestSessionLocked()
	}
	authenticator.sessions[key] = session{created: now, lastSeen: now, persistent: true, expiresAt: expiresAt}
	authenticator.mutex.Unlock()
	return true
}

func (authenticator *Authenticator) revokeRequest(request *http.Request) {
	cookies := request.CookiesNamed(CookieName)
	if len(cookies) != 1 {
		return
	}
	key, ok := sessionKey(cookies[0].Value)
	if !ok {
		return
	}
	authenticator.mutex.Lock()
	delete(authenticator.sessions, key)
	authenticator.mutex.Unlock()
	if authenticator.sessionStore != nil {
		if err := authenticator.sessionStore.Delete(request.Context(), key[:]); err != nil {
			slog.Error("delete persistent administrator session", "error", err)
		}
	}
}

func (authenticator *Authenticator) cleanSessionsLocked(now time.Time) {
	for key, current := range authenticator.sessions {
		if current.persistent && !now.Before(current.expiresAt) ||
			!current.persistent && (!now.Before(current.created.Add(authenticator.absoluteTimeout)) ||
				!now.Before(current.lastSeen.Add(authenticator.idleTimeout))) {
			delete(authenticator.sessions, key)
		}
	}
}

func (authenticator *Authenticator) evictOldestSessionLocked() {
	var oldestKey [sha256.Size]byte
	var oldest time.Time
	for key, current := range authenticator.sessions {
		if oldest.IsZero() || current.lastSeen.Before(oldest) {
			oldestKey, oldest = key, current.lastSeen
		}
	}
	delete(authenticator.sessions, oldestKey)
}

func sessionKey(token string) ([sha256.Size]byte, bool) {
	value, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(value) != sessionTokenBytes {
		return [sha256.Size]byte{}, false
	}
	key := sha256.Sum256(value)
	clear(value)
	return key, true
}

func (authenticator *Authenticator) setSessionCookie(writer http.ResponseWriter, request *http.Request, token string, expiresAt time.Time) {
	cookie := &http.Cookie{
		Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: authenticator.secureCookie || request.TLS != nil, SameSite: http.SameSiteStrictMode,
	}
	if !expiresAt.IsZero() {
		cookie.Expires = expiresAt
		cookie.MaxAge = max(1, int(expiresAt.Sub(authenticator.now().UTC()).Seconds()))
	}
	http.SetCookie(writer, cookie)
}

func validateRememberDuration(duration time.Duration) error {
	if duration != 0 && (duration < MinRememberDuration || duration > MaxRememberDuration) {
		return errors.New("remember duration must be between 1 and 365 days")
	}
	return nil
}

func normalizedRememberDuration(duration time.Duration) time.Duration {
	if duration == 0 {
		return DefaultRememberDuration
	}
	return duration
}

func configuredCredentialKey(username string, password []byte) [sha256.Size]byte {
	passwordDigest := sha256.Sum256(password)
	hash := sha256.New()
	hash.Write([]byte(username))
	hash.Write([]byte{0})
	hash.Write(passwordDigest[:])
	clear(passwordDigest[:])
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func storedCredentialKey(username string, passwordHash []byte) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write([]byte(username))
	hash.Write([]byte{0})
	hash.Write(passwordHash)
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func (authenticator *Authenticator) clearSessionCookie(writer http.ResponseWriter, request *http.Request) {
	http.SetCookie(writer, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: authenticator.secureCookie || request.TLS != nil, SameSite: http.SameSiteStrictMode,
		MaxAge: -1, Expires: time.Unix(1, 0).UTC(),
	})
}

func remoteSource(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil && host != "" {
		return host
	}
	if remoteAddress == "" {
		return "unknown"
	}
	return remoteAddress
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeJSONError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, status, map[string]string{"error": message})
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

func IsSameOrigin(request *http.Request) bool {
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
