package api

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

const (
	sessionPath   = "/api/session"
	sessionCookie = "inkan_session"
	sessionTTL    = 30 * 24 * time.Hour

	// Every sign in counts, right or wrong, for all clients together. A
	// budget per address could be spread over many addresses.
	loginAttempts = 5
	loginWindow   = time.Minute
)

// auth guards the API with one password. The session is a signed cookie, so
// nothing is stored. Without a password everything is let through.
type auth struct {
	password [sha256.Size]byte
	key      []byte // signs the session cookie, nil without a password

	mu       sync.Mutex
	attempts int
	since    time.Time
}

type Session struct {
	Protected bool `json:"protected" doc:"Whether a password is set"`
	SignedIn  bool `json:"signed_in" doc:"Whether this browser may use the API. Always true without a password"`
}

type SessionInput struct {
	Cookie string `cookie:"inkan_session"`
}

type SessionOutput struct {
	Body Session
}

type SignInInput struct {
	// Set by the proxy that ends TLS, inkan itself only speaks plain HTTP.
	Proto string `header:"X-Forwarded-Proto" hidden:"true"`
	Body  struct {
		Password string `json:"password" minLength:"1" maxLength:"1024"`
	}
}

type CookieOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

func newAuth(password string) (*auth, error) {
	a := &auth{}
	if password == "" {
		return a, nil
	}
	a.password = sha256.Sum256([]byte(password))
	// Slow on purpose. A cookie that leaks must not make the password cheap
	// to guess. A new password gives a new key, which ends every session.
	key, err := pbkdf2.Key(sha256.New, password, []byte("inkan session cookie"), 600_000, sha256.Size)
	if err != nil {
		return nil, fmt.Errorf("deriving the session key: %w", err)
	}
	a.key = key
	return a, nil
}

func registerSession(api huma.API, a *auth) {
	huma.Register(api, huma.Operation{
		OperationID: "get-session",
		Method:      http.MethodGet,
		Path:        sessionPath,
		Summary:     "Show whether a sign in is needed",
		Tags:        []string{"Session"},
	}, a.session)
	huma.Register(api, huma.Operation{
		OperationID:   "sign-in",
		Method:        http.MethodPost,
		Path:          sessionPath,
		Summary:       "Sign in with the password",
		Description:   "Sets the session cookie. Five tries a minute are allowed, for all clients together.",
		Tags:          []string{"Session"},
		DefaultStatus: http.StatusNoContent,
	}, a.signIn)
	huma.Register(api, huma.Operation{
		OperationID:   "sign-out",
		Method:        http.MethodDelete,
		Path:          sessionPath,
		Summary:       "Sign out",
		Tags:          []string{"Session"},
		DefaultStatus: http.StatusNoContent,
	}, a.signOut)
}

// require turns away every API call without a session. The session routes
// stay open, signing in has to work without one.
func (a *auth) require(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if a.key != nil && ctx.Operation().Path != sessionPath {
			cookie, err := huma.ReadCookie(ctx, sessionCookie)
			if err != nil || !a.signedIn(cookie.Value) {
				_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "sign in first")
				return
			}
		}
		next(ctx)
	}
}

func (a *auth) session(_ context.Context, in *SessionInput) (*SessionOutput, error) {
	return &SessionOutput{Body: Session{
		Protected: a.key != nil,
		SignedIn:  a.key == nil || a.signedIn(in.Cookie),
	}}, nil
}

func (a *auth) signIn(_ context.Context, in *SignInInput) (*CookieOutput, error) {
	if !a.attempt() {
		return nil, huma.Error429TooManyRequests("too many tries, wait a minute")
	}
	given := sha256.Sum256([]byte(in.Body.Password))
	if a.key == nil || subtle.ConstantTimeCompare(given[:], a.password[:]) != 1 {
		return nil, huma.Error401Unauthorized("wrong password")
	}
	value := a.sign(time.Now().Add(sessionTTL).Unix())
	return &CookieOutput{SetCookie: cookie(value, int(sessionTTL.Seconds()), in.Proto == "https")}, nil
}

func (a *auth) signOut(context.Context, *struct{}) (*CookieOutput, error) {
	return &CookieOutput{SetCookie: cookie("", -1, false)}, nil
}

func cookie(value string, maxAge int, secure bool) http.Cookie {
	//nolint:gosec // Secure follows the request, plain HTTP on a private network has to work
	return http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// sign builds the cookie value, the expiry in unix seconds and its MAC.
func (a *auth) sign(expires int64) string {
	stamp := strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(stamp))
	return stamp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *auth) signedIn(value string) bool {
	stamp, _, _ := strings.Cut(value, ".")
	expires, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || time.Now().Unix() >= expires {
		return false
	}
	return hmac.Equal([]byte(value), []byte(a.sign(expires)))
}

// attempt takes one try from the budget and reports whether there was one.
func (a *auth) attempt() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now := time.Now(); now.Sub(a.since) >= loginWindow {
		a.since, a.attempts = now, 0
	}
	a.attempts++
	return a.attempts <= loginAttempts
}

// loopback reports whether addr only takes connections from this machine.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}
