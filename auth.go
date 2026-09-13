package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "gpuless_session"
	sessionTTL    = 30 * 24 * time.Hour
	shortTTL      = 12 * time.Hour
)

var errWeakPassword = errors.New("password must be at least 10 characters")

func hashPassword(pw string) (string, error) {
	if utf8.RuneCountInString(pw) < 10 {
		return "", errWeakPassword
	}
	// bcrypt caps at 72 bytes and silently ignores the rest, so refuse rather
	// than pretend a 200-character passphrase was used in full.
	if len(pw) > 72 {
		return "", errors.New("password must be at most 72 bytes")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func checkPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// randomToken returns a URL-safe random string with n bytes of entropy.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("gpuless: no entropy available: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is what actually lands in the sessions table, so a stolen
// database does not hand over live sessions.
func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64, remember bool) error {
	tok := randomToken(32)
	ttl := shortTTL
	if remember {
		ttl = sessionTTL
	}
	if err := a.store.CreateSession(hashToken(tok), userID, ttl, clientIP(r), truncate(r.UserAgent(), 200)); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
	return nil
}

func (a *App) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.store.DeleteSession(hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (a *App) currentUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	u, err := a.store.SessionUser(hashToken(c.Value))
	if err != nil {
		return nil
	}
	return u
}

// requireUser gates everything except setup, login and static assets.
func (a *App) requireUser(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.currentUser(r)
		if u == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "sign in first")
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, u)
	}
}

// ------------------------------------------------------------- brute force

// throttle locks an address out after repeated failures. It is deliberately
// in memory: a restart clearing the counters is not a meaningful weakness for
// a single-tenant panel, and it keeps the write path off SQLite.
type throttle struct {
	mu      chanMutex
	fails   map[string]*failure
	max     int
	lockFor time.Duration
}

type failure struct {
	count  int
	locked time.Time
}

func newThrottle(max int, lockFor time.Duration) *throttle {
	return &throttle{mu: newChanMutex(), fails: map[string]*failure{}, max: max, lockFor: lockFor}
}

// Locked reports whether the address must wait, and for how long.
func (t *throttle) Locked(key string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.fails[key]
	if f == nil || f.locked.IsZero() {
		return false, 0
	}
	if d := time.Until(f.locked); d > 0 {
		return true, d
	}
	delete(t.fails, key)
	return false, 0
}

func (t *throttle) Fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.fails[key]
	if f == nil {
		f = &failure{}
		t.fails[key] = f
	}
	f.count++
	if f.count >= t.max {
		f.locked = time.Now().Add(t.lockFor)
		f.count = 0
	}
}

func (t *throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, key)
}

// chanMutex is a mutex that can be held across a select. Using a channel
// keeps the zero value unusable, which is what we want here.
type chanMutex chan struct{}

func newChanMutex() chanMutex {
	c := make(chanMutex, 1)
	c <- struct{}{}
	return c
}

func (c chanMutex) Lock()   { <-c }
func (c chanMutex) Unlock() { c <- struct{}{} }

// ----------------------------------------------------------------- helpers

func clientIP(r *http.Request) string {
	// Only trust a forwarding header when the panel is behind a proxy the
	// operator put there; the direct address is the honest default.
	if h := r.Header.Get("X-Forwarded-For"); h != "" && trustProxy {
		if i := strings.IndexByte(h, ','); i > 0 {
			return strings.TrimSpace(h[:i])
		}
		return strings.TrimSpace(h)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
