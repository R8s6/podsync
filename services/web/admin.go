package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/mxpv/podsync/pkg/feed"
	"github.com/mxpv/podsync/pkg/model"
)

type AdminConfig struct {
	Enabled  bool             `toml:"enabled"`
	Token    string           `toml:"token" json:"-"`
	Origin   string           `toml:"origin"`
	Defaults feed.WebDefaults `toml:"defaults"`
}

func (c AdminConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.Token) < 32 || len(c.Token) > 256 || strings.TrimSpace(c.Token) != c.Token {
		return errors.New("web_admin.token must be a random secret of 32-256 characters")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("web_admin.origin must be an HTTPS origin, e.g. https://podsync.example.com")
	}
	return c.Defaults.Validate()
}

type adminSession struct {
	csrf    string
	expires time.Time
}

type adminWindow struct {
	start time.Time
	count int
}

type adminHandler struct {
	origin, host, cookiePath string
	tokenHash                [32]byte
	add                      func(context.Context, string) (string, error)
	mu                       sync.Mutex
	sessions                 map[string]adminSession
	windows                  map[string]adminWindow
}

// EnableAdmin registers only management routes. The file server, feeds, OPML,
// and media never pass through authentication middleware.
func (s *Server) EnableAdmin(cfg AdminConfig, add func(context.Context, string) (string, error)) error {
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	origin, _ := url.Parse(cfg.Origin)
	prefix := s.mountPath + "api/admin/"
	a := &adminHandler{
		origin: cfg.Origin, host: origin.Host, cookiePath: prefix,
		tokenHash: sha256.Sum256([]byte(cfg.Token)), add: add,
		sessions: make(map[string]adminSession), windows: make(map[string]adminWindow),
	}
	s.mux.Handle(prefix, http.StripPrefix(prefix, a))
	return nil
}

func (a *adminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodGet && r.URL.Path == "capabilities" {
		adminJSON(w, http.StatusOK, map[string]bool{"enabled": true})
		return
	}
	if r.Host != a.host {
		http.Error(w, "Use the configured HTTPS address to add shows.", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost && (r.Method != http.MethodGet || r.URL.Path != "session") {
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		// Compare against the configured public origin, never forwarded headers.
		// Check the origin even for login, where no session/CSRF token exists yet.
		if r.Header.Get("Origin") != a.origin {
			http.Error(w, "Invalid request origin.", http.StatusForbidden)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "JSON required.", http.StatusUnsupportedMediaType)
			return
		}
	}
	if r.URL.Path == "login" && r.Method == http.MethodPost {
		a.login(w, r)
		return
	}
	cookie, err := r.Cookie("__Secure-podsync_admin")
	if err != nil {
		http.Error(w, "Sign in to add a show.", http.StatusUnauthorized)
		return
	}
	a.mu.Lock()
	session, exists := a.sessions[cookie.Value]
	a.mu.Unlock()
	if !exists || !time.Now().Before(session.expires) {
		http.Error(w, "Sign in to add a show.", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		adminJSON(w, http.StatusOK, map[string]string{"csrf": session.csrf})
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.csrf)) != 1 {
		http.Error(w, "Invalid request token.", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "logout":
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
		a.setCookie(w, "", -1)
		w.WriteHeader(http.StatusNoContent)
	case "feeds":
		if !a.allow("add", 20) {
			http.Error(w, "Too many additions. Try again in a minute.", http.StatusTooManyRequests)
			return
		}
		var input struct {
			URL string `json:"url"`
		}
		if !adminDecode(w, r, &input) {
			return
		}
		id, err := a.add(r.Context(), input.URL)
		switch {
		case errors.Is(err, model.ErrInvalidFeed):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, model.ErrAlreadyExists):
			http.Error(w, "This show is already configured.", http.StatusConflict)
		case errors.Is(err, model.ErrFeedLimit):
			http.Error(w, "The limit of 100 web-added shows has been reached.", http.StatusConflict)
		case err != nil:
			log.WithError(err).Error("could not add web feed")
			http.Error(w, "Could not save the show. Check the server logs.", http.StatusInternalServerError)
		default:
			log.WithField("feed_id", id).Info("show added through web administration")
			adminJSON(w, http.StatusCreated, map[string]string{"id": id})
		}
	default:
		http.NotFound(w, r)
	}
}

func (a *adminHandler) login(w http.ResponseWriter, r *http.Request) {
	if !a.allow("login", 10) {
		http.Error(w, "Too many sign-in attempts. Try again in a minute.", http.StatusTooManyRequests)
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if !adminDecode(w, r, &input) {
		return
	}
	hash := sha256.Sum256([]byte(input.Token))
	if subtle.ConstantTimeCompare(hash[:], a.tokenHash[:]) != 1 {
		log.Warn("web administration sign-in failed")
		http.Error(w, "Incorrect admin token.", http.StatusUnauthorized)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, session := range a.sessions {
		if !time.Now().Before(session.expires) {
			delete(a.sessions, id)
		}
	}
	if old, err := r.Cookie("__Secure-podsync_admin"); err == nil {
		delete(a.sessions, old.Value)
	}
	if len(a.sessions) >= 32 {
		http.Error(w, "Too many active admin sessions.", http.StatusTooManyRequests)
		return
	}
	id := rand.Text()
	session := adminSession{csrf: rand.Text(), expires: time.Now().Add(8 * time.Hour)}
	a.sessions[id] = session
	a.setCookie(w, id, 0)
	adminJSON(w, http.StatusOK, map[string]string{"csrf": session.csrf})
}

func (a *adminHandler) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: "__Secure-podsync_admin", Value: value, Path: a.cookiePath,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge,
	})
}

// Global, bounded rate counters work identically with and without a proxy and
// cannot be bypassed by spoofing client-IP headers or exhausted by new IP keys.
func (a *adminHandler) allow(action string, limit int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	window := a.windows[action]
	if time.Since(window.start) >= time.Minute {
		window = adminWindow{start: time.Now()}
	}
	window.count++
	a.windows[action] = window
	return window.count <= limit
}

func adminDecode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "Invalid request body (maximum 4 KiB).", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "Expected one JSON object.", http.StatusBadRequest)
		return false
	}
	return true
}

func adminJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
