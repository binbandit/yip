// Package integration runs the hub in-process against a real temporary SQLite
// database and drives it through the browser API. Tests that need a machine
// connect one in-process and play the runner's side of the protocol.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
)

const (
	ownerHandle   = "brayden"
	ownerPassword = "correct-horse-battery"
)

type envOptions struct {
	forge      func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error)
	githubRepo func(ctx context.Context, h *hub.Hub, owner, name string) (github.RepoInfo, error)
	limits     func(*domain.Limits)
}

type env struct {
	t         *testing.T
	dir       string
	hub       *hub.Hub
	browser   *httptest.Server
	c         *client
	engineers map[string]string
	ctx       context.Context
	cancel    context.CancelFunc
	opts      envOptions
}

func quietLogger() *slog.Logger {
	level := slog.LevelError
	if os.Getenv("YIP_TEST_LOG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func newEnv(t *testing.T, opts envOptions) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{t: t, dir: t.TempDir(), ctx: ctx, cancel: cancel, opts: opts}
	lim := domain.DefaultLimits()
	lim.HeartbeatInterval = 500 * time.Millisecond
	if opts.limits != nil {
		opts.limits(&lim)
	}
	e.startHub(lim)
	var err error
	if e.engineers, err = seedWorkspace(ctx, e.hub); err != nil {
		t.Fatal(err)
	}
	e.c = e.signIn()
	t.Cleanup(e.close)
	return e
}

// seedWorkspace creates the owner and a small team: Mira, Oren, and Pip on
// Codex; the Atlas and Beacon projects; and the Security, Engineering, and
// Reverse engineering rooms. It returns engineer handle → ID.
func seedWorkspace(ctx context.Context, h *hub.Hub) (map[string]string, error) {
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		return nil, err
	}
	user, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Brayden's workspace", Name: "Brayden", Handle: ownerHandle, Password: ownerPassword})
	if err != nil {
		return nil, err
	}
	codex := protocol.ProviderPreference{Provider: "codex"}
	engineers := map[string]string{}
	for _, req := range []protocol.CreateEngineerRequest{
		{Name: "Mira", Role: "Platform engineer", Provider: codex, CapabilityTags: []string{"go", "backend"}},
		{Name: "Oren", Role: "Security engineer", Provider: codex, CapabilityTags: []string{"security", "review"}},
		{Name: "Pip", Role: "Reverse engineer", Provider: codex, CapabilityTags: []string{"tracing", "documentation"}},
	} {
		eng, err := h.CreateEngineer(ctx, user.ID, req)
		if err != nil {
			return nil, err
		}
		engineers[eng.Handle] = eng.ID
	}
	atlas, err := h.CreateProject(ctx, user.ID, protocol.CreateProjectRequest{Name: "Atlas", Description: "Identity and session services.",
		Policy: protocol.ProjectPolicy{RequirePeerReview: true, ExecutionProfile: "native"}})
	if err != nil {
		return nil, err
	}
	beacon, err := h.CreateProject(ctx, user.ID, protocol.CreateProjectRequest{Name: "Beacon", Description: "Request gateway and background workers.",
		Policy: protocol.ProjectPolicy{ExecutionProfile: "native"}})
	if err != nil {
		return nil, err
	}
	for _, r := range []struct{ project, name string }{{atlas.ID, "atlas"}, {beacon.ID, "beacon-gateway"}} {
		if _, err := h.PutRepo(ctx, user.ID, r.project, "", protocol.PutRepoRequest{Name: r.name, RemoteURL: "file:///srv/git/" + r.name + ".git", DefaultBranch: "main"}); err != nil {
			return nil, err
		}
	}
	for _, g := range []struct{ project, eng, access string }{
		{atlas.ID, "mira", "write"}, {atlas.ID, "oren", "read"}, {beacon.ID, "mira", "read"}, {beacon.ID, "pip", "write"},
	} {
		if _, err := h.PutGrant(ctx, user.ID, g.project, engineers[g.eng], protocol.PutGrantRequest{Access: g.access}); err != nil {
			return nil, err
		}
	}
	for _, rq := range []protocol.CreateRoomRequest{
		{Name: "Security", EngineerIDs: []string{engineers["mira"], engineers["oren"]}, ProjectIDs: []string{atlas.ID}, ReplyMode: protocol.ReplyModeQuiet},
		{Name: "Engineering", EngineerIDs: []string{engineers["mira"], engineers["pip"]}, ProjectIDs: []string{atlas.ID, beacon.ID},
			ReplyMode: protocol.ReplyModeSteward, StewardID: engineers["mira"]},
		{Name: "Reverse engineering", EngineerIDs: []string{engineers["pip"]}, ProjectIDs: []string{beacon.ID}, ReplyMode: protocol.ReplyModeQuiet},
	} {
		if _, err := h.CreateRoom(ctx, user.ID, rq); err != nil {
			return nil, err
		}
	}
	return engineers, nil
}

func (e *env) startHub(lim domain.Limits) {
	h, err := hub.Open(e.ctx, hub.Config{DataDir: filepath.Join(e.dir, "hub"), Version: "test", Limits: lim, Logger: quietLogger(),
		RunnerURL: "https://127.0.0.1:7443", ForgeFactory: e.opts.forge, GitHubRepo: e.opts.githubRepo, WebhookVerifier: github.New(github.Options{}).VerifyWebhook})
	if err != nil {
		e.t.Fatal(err)
	}
	h.Start(e.ctx)
	e.hub = h
	e.browser = httptest.NewServer(httpapi.New(h, httpapi.Options{Logger: quietLogger()}))
}

// restartHub simulates a hub crash and restart on the same data directory.
func (e *env) restartHub() {
	e.browser.Close()
	_ = e.hub.Close()
	lim := e.hub.Limits()
	e.startHub(lim)
	e.c = e.signIn()
}

func (e *env) close() {
	e.cancel()
	e.browser.Close()
	_ = e.hub.Close()
}

func (e *env) waitFor(what string, timeout time.Duration, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		e.hub.Kick()
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("timed out waiting for %s", what)
}

// ---- browser client ----

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf string
	boot protocol.Bootstrap
	// idemKey, when set, is sent as the Idempotency-Key header.
	idemKey string
	// lastReplayed reports whether the last response was an idempotent replay.
	lastReplayed *bool
}

func (e *env) signIn() *client {
	jar, _ := cookiejar.New(nil)
	c := &client{t: e.t, base: e.browser.URL, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	if err := c.do("POST", "/v1/session", protocol.SignInRequest{Handle: ownerHandle, Password: ownerPassword}, nil); err != nil {
		e.t.Fatal(err)
	}
	if err := c.do("GET", "/v1/bootstrap", nil, &c.boot); err != nil {
		e.t.Fatal(err)
	}
	c.csrf = c.boot.CSRFToken
	return c
}

type apiError struct {
	status int
	protocol.APIError
}

func (a *apiError) Error() string { return fmt.Sprintf("HTTP %d %s: %s", a.status, a.Code, a.Message) }

// withKey sends the next requests with an Idempotency-Key.
func (c *client) withKey(key string) *client {
	cp := *c
	cp.idemKey = key
	return &cp
}

func (c *client) do(method, path string, body, out any) error {
	var rd io.Reader
	ctype := "application/json"
	if raw, ok := body.([]byte); ok {
		rd, ctype = bytes.NewReader(raw), "application/octet-stream" // an upload
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Origin", c.base)
	if c.csrf != "" {
		req.Header.Set("X-Yip-Csrf", c.csrf)
	}
	if c.idemKey != "" {
		req.Header.Set("Idempotency-Key", c.idemKey)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if c.lastReplayed != nil {
		*c.lastReplayed = resp.Header.Get("Idempotent-Replayed") == "true"
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		ae := &apiError{status: resp.StatusCode}
		_ = json.Unmarshal(raw, &ae.APIError)
		return ae
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *client) must(method, path string, body, out any) {
	c.t.Helper()
	if err := c.do(method, path, body, out); err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (e *env) roomID(name string) string {
	e.t.Helper()
	var rooms []protocol.Room
	e.c.must("GET", "/v1/rooms", nil, &rooms)
	for _, r := range rooms {
		if r.Name == name {
			return r.ID
		}
	}
	e.t.Fatalf("no room %q", name)
	return ""
}

func (e *env) engineerID(handle string) string {
	id, ok := e.engineers[handle]
	if !ok {
		e.t.Fatalf("no engineer %q", handle)
	}
	return id
}

func (e *env) project(name string) protocol.Project {
	var ps []protocol.Project
	e.c.must("GET", "/v1/projects", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	e.t.Fatalf("no project %s", name)
	return protocol.Project{}
}

func (e *env) post(room, body string, mentions []string, mod func(*protocol.PostMessageRequest)) protocol.PostMessageResponse {
	e.t.Helper()
	req := protocol.PostMessageRequest{Body: body, ClientKey: domain.NewID(), Mentions: []protocol.Mention{}}
	for _, m := range mentions {
		req.Mentions = append(req.Mentions, protocol.Mention{Kind: protocol.ActorEngineer, ID: e.engineerID(m)})
	}
	if mod != nil {
		mod(&req)
	}
	var resp protocol.PostMessageResponse
	e.c.must("POST", "/v1/rooms/"+e.roomID(room)+"/messages", req, &resp)
	return resp
}

func (e *env) messages(room string) []protocol.Message {
	var page protocol.MessagePage
	e.c.must("GET", "/v1/rooms/"+e.roomID(room)+"/messages?limit=200", nil, &page)
	return page.Messages
}

// waitMessage waits for a message in the room whose body starts with prefix.
func (e *env) waitMessage(room, prefix string) protocol.Message {
	e.t.Helper()
	var got protocol.Message
	e.waitFor("a message starting "+strconv.Quote(prefix)+" in "+room, 30*time.Second, func() bool {
		for _, m := range e.messages(room) {
			if strings.HasPrefix(m.Body, prefix) {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

func (e *env) jobs() []protocol.Job {
	var jobs []protocol.Job
	e.c.must("GET", "/v1/jobs?limit=200", nil, &jobs)
	return jobs
}

func (e *env) job(prefix string) (protocol.Job, bool) {
	for _, j := range e.jobs() {
		if strings.HasPrefix(j.Title, prefix) {
			return j, true
		}
	}
	return protocol.Job{}, false
}

func (e *env) jobDetail(id string) protocol.JobDetail {
	var d protocol.JobDetail
	e.c.must("GET", "/v1/jobs/"+id, nil, &d)
	return d
}

func (e *env) waitJob(prefix string, states ...protocol.JobState) protocol.Job {
	e.t.Helper()
	var got protocol.Job
	e.waitFor(prefix+" in "+fmt.Sprint(states), 60*time.Second, func() bool {
		j, ok := e.job(prefix)
		if !ok {
			return false
		}
		got = j
		for _, s := range states {
			if j.State == s {
				return true
			}
		}
		return false
	})
	return got
}

func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == status
}
