package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
)

type workspaceHandlers struct {
	hub     *hub.Hub
	browser *Server
	runner  *RunnerServer
}

// Workspaces routes an installation's independent hubs. The caller starts the
// root hub; the manager starts children and closes all hubs on shutdown.
type Workspaces struct {
	mu       sync.RWMutex
	ctx      context.Context
	root     workspaceHandlers
	children map[string]workspaceHandlers
	opts     Options
	closed   bool
}

func OpenWorkspaces(ctx context.Context, root *hub.Hub, opts Options) (*Workspaces, error) {
	m := &Workspaces{ctx: ctx, opts: opts, children: make(map[string]workspaceHandlers)}
	m.root = m.handlers(root)
	dir := filepath.Join(root.Config().DataDir, "workspaces")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		// Incomplete creations stay hidden and are never routed or discovered.
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child, err := hub.Open(ctx, m.childConfig(entry.Name()))
		if err == nil && child.Org().ID == "" {
			err = fmt.Errorf("workspace %s is uninitialized", entry.Name())
			child.Close()
		}
		if err != nil {
			for _, c := range m.children {
				c.hub.Close()
			}
			return nil, fmt.Errorf("open workspace %s: %w", entry.Name(), err)
		}
		child.Start(ctx)
		m.children[entry.Name()] = m.handlers(child)
	}
	return m, nil
}

func (m *Workspaces) handlers(h *hub.Hub) workspaceHandlers {
	s := New(h, m.opts)
	s.mux.HandleFunc("GET /v1/workspaces", withTimeout(s.authed(m.list)))
	s.mux.HandleFunc("POST /v1/workspaces", withTimeout(s.authed(m.create)))
	return workspaceHandlers{hub: h, browser: s, runner: NewRunnerServer(h, func(msg string, args ...any) {
		s.log.Info(msg, args...)
	})}
}

func (m *Workspaces) childConfig(id string) hub.Config {
	cfg := m.root.hub.Config()
	cfg.DataDir = filepath.Join(cfg.DataDir, "workspaces", id)
	cfg.RunnerURL = strings.TrimRight(cfg.RunnerURL, "/") + "/w/" + id
	cfg.SessionHub = m.root.hub
	return cfg
}

func (m *Workspaces) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.serve(w, r, false)
}

// RunnerHandler uses the same workspace selection, but never browser auth.
func (m *Workspaces) RunnerHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.serve(w, r, true) })
}

func (m *Workspaces) serve(w http.ResponseWriter, r *http.Request, runner bool) {
	// ServeMux canonical redirects run after StripPrefix. Reject ambiguous
	// child paths here so a redirect can never drop the workspace boundary.
	if strings.HasPrefix(r.URL.Path, "/w/") && path.Clean(r.URL.Path) != strings.TrimSuffix(r.URL.Path, "/") {
		writeError(w, domain.Invalid("Use a workspace URL without repeated slashes or dot segments."))
		return
	}
	m.mu.RLock()
	selected := m.root
	prefix := ""
	if r.URL.Path == "/w" || strings.HasPrefix(r.URL.Path, "/w/") {
		id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/w/"), "/")
		var ok bool
		selected, ok = m.children[id]
		if !ok {
			m.mu.RUnlock()
			writeError(w, domain.NotFound("Workspace not found."))
			return
		}
		prefix = "/w/" + id
	}
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		http.Error(w, "Installation is shutting down.", http.StatusServiceUnavailable)
		return
	}
	if prefix != "" && r.URL.Path == prefix {
		target := prefix + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	var handler http.Handler = selected.browser
	if runner {
		handler = selected.runner
	} else if strings.TrimPrefix(r.URL.Path, prefix) == "/v1/workspaces" {
		// Installation-wide mutations share one idempotency ledger, including
		// retries issued after the client switches workspaces.
		handler = m.root.browser
	}
	if prefix != "" {
		handler = http.StripPrefix(prefix, handler)
	}
	handler.ServeHTTP(w, r)
}

func (m *Workspaces) list(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	root := m.root.hub.Org()
	out := []protocol.Workspace{{ID: root.ID, Name: root.Name, Path: ""}}
	for id, child := range m.children {
		out = append(out, protocol.Workspace{ID: id, Name: child.hub.Org().Name, Path: "/w/" + id})
	}
	sort.Slice(out[1:], func(i, j int) bool {
		return strings.ToLower(out[i+1].Name) < strings.ToLower(out[j+1].Name)
	})
	writeJSON(w, http.StatusOK, out)
}

func (m *Workspaces) create(w http.ResponseWriter, r *http.Request) {
	s := m.root.browser
	req, ok := decodeJSON[protocol.CreateWorkspaceRequest](s, w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		s.fail(w, r, domain.Invalid("Use a workspace name of 1–80 characters without control characters."))
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		s.fail(w, r, domain.Conflict("Installation is shutting down."))
		return
	}
	if strings.EqualFold(name, m.root.hub.Org().Name) {
		s.fail(w, r, domain.Conflict("A workspace with that name already exists."))
		return
	}
	for _, child := range m.children {
		if strings.EqualFold(name, child.hub.Org().Name) {
			s.fail(w, r, domain.Conflict("A workspace with that name already exists."))
			return
		}
	}
	id := domain.NewID()
	cfg := m.childConfig(id)
	finalDir := cfg.DataDir
	cfg.DataDir = filepath.Join(filepath.Dir(finalDir), "."+id)
	child, err := hub.Open(r.Context(), cfg)
	if err == nil {
		err = child.SeedWorkspace(r.Context(), name, userFrom(r).User)
		closeErr := child.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = os.Rename(cfg.DataDir, finalDir)
	}
	if err == nil {
		child, err = hub.Open(m.ctx, m.childConfig(id))
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	child.Start(m.ctx)
	m.children[id] = m.handlers(child)
	writeJSON(w, http.StatusCreated, protocol.Workspace{ID: id, Name: name, Path: "/w/" + id})
}

func (m *Workspaces) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var err error
	for _, child := range m.children {
		err = errors.Join(err, child.hub.Close())
	}
	return errors.Join(err, m.root.hub.Close())
}
