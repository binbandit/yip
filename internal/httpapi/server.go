// Package httpapi serves the browser API (HTTP JSON + SSE + embedded web
// client) and the runner listener (pairing, mutual-TLS WebSocket, artifact
// transfer). Browser and node endpoints have separate authentication paths.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const (
	cookieName     = "yip_session"
	csrfHeader     = "X-Yip-Csrf"
	maxJSONBody    = 1 << 20
	requestTimeout = 30 * time.Second
)

// Options configures the browser server.
type Options struct {
	// SecureCookies marks cookies Secure (required for remote HTTPS access).
	SecureCookies bool
	// AllowedOrigins lists extra origins (scheme://host[:port]) accepted for
	// state-changing requests, e.g. a Tailscale Serve hostname.
	AllowedOrigins []string
	// Web serves the embedded client; nil serves a placeholder.
	Web    http.FileSystem
	Logger *slog.Logger
}

// Server is the browser-facing HTTP server.
type Server struct {
	hub  *hub.Hub
	opts Options
	log  *slog.Logger
	mux  *http.ServeMux
}

type ctxKey int

const (
	keyUser ctxKey = iota
	keySession
)

// New builds the browser handler.
func New(h *hub.Hub, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{hub: h, opts: opts, log: opts.Logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	s.mux.ServeHTTP(w, r)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	de := domain.AsError(err)
	if de.Code == "internal" {
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	if errors.Is(err, store.ErrNotFound) {
		de = domain.NotFound("Not found.")
	}
	writeJSON(w, de.Status, de.API(requestID(r)))
}

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" && len(id) < 80 {
		return id
	}
	return domain.Short(domain.NewID())
}

func decodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var v T
	body := http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil && !errors.Is(err, io.EOF) {
		return v, domain.Invalid("The request body is not valid: %s", err.Error())
	}
	return v, nil
}

func userFrom(r *http.Request) store.UserRow {
	u, _ := r.Context().Value(keyUser).(store.UserRow)
	return u
}

func sessionFrom(r *http.Request) store.Session {
	s, _ := r.Context().Value(keySession).(store.Session)
	return s
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// checkOrigin enforces strict Origin checks on state-changing requests.
func (s *Server) checkOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return domain.Forbidden("Missing Origin header.")
	}
	u, err := url.Parse(origin)
	if err != nil {
		return domain.Forbidden("Invalid Origin.")
	}
	if strings.EqualFold(u.Host, r.Host) {
		return nil
	}
	for _, o := range s.opts.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return nil
		}
	}
	return domain.Forbidden("Cross-origin requests are not allowed.")
}

// authed wraps a handler with session authentication, CSRF, and Origin checks.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			s.fail(w, r, domain.Unauthorized("Sign in to continue."))
			return
		}
		u, sess, err := s.hub.Authenticate(r.Context(), c.Value)
		if err != nil {
			s.clearCookie(w)
			s.fail(w, r, err)
			return
		}
		if isUnsafe(r.Method) {
			if err := s.checkOrigin(r); err != nil {
				s.fail(w, r, err)
				return
			}
			if subtleEq(r.Header.Get(csrfHeader), sess.CSRFToken) == false {
				s.fail(w, r, domain.Forbidden("Missing or invalid CSRF token. Reload the page."))
				return
			}
		}
		ctx := context.WithValue(r.Context(), keyUser, u)
		ctx = context.WithValue(ctx, keySession, sess)
		s.idempotent(w, r.WithContext(ctx), u.ID, h)
	}
}

func subtleEq(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *Server) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", Expires: expires, HttpOnly: true,
		Secure: s.opts.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.opts.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func queryInt(r *http.Request, name string, def int64) int64 {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func withTimeout(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		h(w, r.WithContext(ctx))
	}
}

var _ = protocol.SchemaVersion
