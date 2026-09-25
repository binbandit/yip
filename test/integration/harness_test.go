// Package integration runs the hub, a paired runner, the yip bridge, and the
// deterministic fake provider in-process against a real temporary SQLite
// database and git fixtures, and drives them through the browser API.
package integration

import (
	"bytes"
	"database/sql"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/bridge"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/demo"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/protocol"
)

// TestMain lets this test binary act as `yip bridge` for provider sessions.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "bridge" {
		mode := "conversation"
		for i, a := range os.Args {
			if a == "--mode" && i+1 < len(os.Args) {
				mode = os.Args[i+1]
			}
		}
		if err := bridge.RunFromEnv(mode, "test"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type envOptions struct {
	limits   func(*domain.Limits)
	director func(m *manifest.Manifest) json.RawMessage
	noRunner bool
	slots    int
}

type env struct {
	t         *testing.T
	dir       string
	hub       *hub.Hub
	browser   *httptest.Server
	runnerLn  net.Listener
	runnerSrv *http.Server
	runnerURL string
	stopRun   func()
	c         *client
	seed      demo.Result
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
	dir := t.TempDir()
	lim := domain.DefaultLimits()
	lim.HeartbeatInterval = 500 * time.Millisecond
	if opts.limits != nil {
		opts.limits(&lim)
	}
	prev := hub.FakeScripter
	hub.FakeScripter = func(m *manifest.Manifest) json.RawMessage {
		if opts.director != nil {
			if s := opts.director(m); s != nil {
				return s
			}
		}
		return fake.Direct(m)
	}
	t.Cleanup(func() { hub.FakeScripter = prev })
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{t: t, dir: dir, ctx: ctx, cancel: cancel, opts: opts}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.runnerLn = ln
	e.runnerURL = "https://" + ln.Addr().String()
	e.startHub(lim)
	repos, err := demo.MaterializeRepos(filepath.Join(dir, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	e.seed, err = demo.Seed(ctx, e.hub, repos, "Brayden", "brayden", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	e.c = e.signIn()
	if !opts.noRunner {
		e.startRunner()
	}
	t.Cleanup(e.close)
	return e
}

func (e *env) startHub(lim domain.Limits) {
	h, err := hub.Open(e.ctx, hub.Config{DataDir: filepath.Join(e.dir, "hub"), Version: "test", Limits: lim, Logger: quietLogger(),
		RunnerURL: e.runnerURL, Demo: true})
	if err != nil {
		e.t.Fatal(err)
	}
	h.Start(e.ctx)
	e.hub = h
	e.browser = httptest.NewServer(httpapi.New(h, httpapi.Options{Logger: quietLogger()}))
	tlsConf, err := h.CA().ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		e.t.Fatal(err)
	}
	if e.runnerLn == nil {
		ln, err := net.Listen("tcp", strings.TrimPrefix(e.runnerURL, "https://"))
		if err != nil {
			e.t.Fatal(err)
		}
		e.runnerLn = ln
	}
	e.runnerSrv = &http.Server{Handler: httpapi.NewRunnerServer(h, nil)}
	go e.runnerSrv.Serve(tls.NewListener(e.runnerLn, tlsConf))
}

// restartHub simulates a hub crash and restart on the same data directory.
func (e *env) restartHub() {
	e.runnerSrv.Close()
	e.runnerLn = nil
	e.browser.Close()
	_ = e.hub.Close()
	lim := e.hub.Limits()
	e.startHub(lim)
	e.c = e.signIn()
}

func (e *env) startRunner() {
	dir := filepath.Join(e.dir, "runner")
	if _, err := runner.LoadIdentity(dir); err != nil {
		keyPEM, csrPEM, err := auth.NewNodeKeyAndCSR("Test mini")
		if err != nil {
			e.t.Fatal(err)
		}
		pr, err := e.hub.PairLocal(e.ctx, "Test mini", csrPEM)
		if err != nil {
			e.t.Fatal(err)
		}
		if _, err := runner.SaveLocalIdentity(dir, runner.Identity{NodeID: pr.NodeID, Name: "Test mini", HubURL: e.runnerURL,
			Fingerprint: e.hub.CA().Fingerprint()}, keyPEM, []byte(pr.CertPEM), []byte(pr.CAPEM)); err != nil {
			e.t.Fatal(err)
		}
	}
	slots := e.opts.slots
	if slots == 0 {
		slots = 3
	}
	r, err := runner.New(runner.Options{StateDir: dir, Slots: slots, Adapters: map[string]providers.Adapter{"fake": fake.New(0)},
		BridgeExe: os.Args[0], Version: "test", Logger: quietLogger(), ServerName: "127.0.0.1"})
	if err != nil {
		e.t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(rctx)
	}()
	e.stopRun = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
		}
	}
	e.waitFor("runner connected", 20*time.Second, func() bool {
		nodes, _ := e.hub.ListNodes(e.ctx)
		for _, n := range nodes {
			if n.Status == protocol.NodeOnline && len(n.Providers) > 0 {
				return true
			}
		}
		return false
	})
}

// killRunner stops the runner abruptly (its connection drops).
func (e *env) killRunner() {
	if e.stopRun != nil {
		e.stopRun()
		e.stopRun = nil
	}
}

func (e *env) close() {
	e.killRunner()
	e.cancel()
	e.browser.Close()
	e.runnerSrv.Close()
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
}

func (e *env) signIn() *client {
	jar, _ := cookiejar.New(nil)
	c := &client{t: e.t, base: e.browser.URL, hc: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
	if err := c.do("POST", "/v1/session", protocol.SignInRequest{Handle: "brayden", Password: "correct-horse-battery"}, nil); err != nil {
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

func (c *client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.base)
	if c.csrf != "" {
		req.Header.Set("X-Yip-Csrf", c.csrf)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
	id, ok := e.seed.Engines[handle]
	if !ok {
		e.t.Fatalf("no engineer %q", handle)
	}
	return id
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

// script builds a fake-provider script from steps.
func script(steps ...fake.Step) json.RawMessage {
	b, _ := json.Marshal(fake.Script{Steps: steps})
	return b
}

func toolStep(name string, args map[string]any, save string) fake.Step {
	b, _ := json.Marshal(args)
	return fake.Step{Tool: name, Args: b, Save: save}
}

func isStatus(err error, status int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == status
}

type sqlTx = sql.Tx
