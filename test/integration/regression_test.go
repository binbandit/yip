package integration

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// Regression tests for the backend review. Each names the defect it guards.

func (e *env) ownerID() string {
	us, _ := store.ListUsers(e.ctx, e.hub.Store().R())
	return us[0].ID
}

// testNode pairs a machine and connects it in-process, recording every frame
// the hub sends it, so tests can play the runner's side exactly.
type testNode struct {
	id     string
	conn   *hub.NodeConn
	inst   protocol.ProviderInstallation
	mu     sync.Mutex
	frames []protocol.Frame
	taken  map[string]bool
	seq    map[string]int64
}

// connectNode connects an in-process machine offering a signed-in Codex.
func (e *env) connectNode(name string) *testNode {
	return e.connectNodeWith(name, protocol.ProviderInstallation{Provider: "codex", AuthState: protocol.AuthReady, ProfileID: "codex:local",
		Capabilities: protocol.ProviderCapabilities{ReadOnly: true}})
}

// connectNodeWith connects an in-process machine offering one installation.
func (e *env) connectNodeWith(name string, inst protocol.ProviderInstallation) *testNode {
	e.t.Helper()
	_, csr, err := auth.NewNodeKeyAndCSR(name)
	if err != nil {
		e.t.Fatal(err)
	}
	pr, err := e.hub.PairLocal(e.ctx, name, csr)
	if err != nil {
		e.t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(pr.CertPEM))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		e.t.Fatal(err)
	}
	id, serial := auth.NodeIDFromCert(cert)
	n := &testNode{id: id, inst: inst, taken: map[string]bool{}, seq: map[string]int64{}}
	n.conn = &hub.NodeConn{NodeID: id, Serial: serial}
	n.connect(e, nil)
	return n
}

// connect (re)connects the machine to the env's current hub, reporting the
// attempts its journal holds, and sends its capabilities.
func (n *testNode) connect(e *env, runs []protocol.JournalRunState) {
	e.t.Helper()
	n.conn = &hub.NodeConn{NodeID: n.id, Serial: n.conn.Serial, ConnectedAt: time.Now(), Send: func(f protocol.Frame) error {
		n.mu.Lock()
		n.frames = append(n.frames, f)
		n.mu.Unlock()
		return nil
	}}
	if err := e.hub.ConnectRunner(e.ctx, n.conn, protocol.Hello{NodeID: n.id, ProtocolVersion: protocol.RunnerProtocolVersion, Runs: runs}); err != nil {
		e.t.Fatal(err)
	}
	caps, _ := json.Marshal(protocol.RunnerCapabilities{Slots: 2,
		Profiles:  []protocol.ExecutionProfile{{Name: "native", Available: true}, {Name: "readonly", Available: true}},
		Providers: []protocol.ProviderInstallation{n.inst}})
	e.hub.RunnerFrame(e.ctx, n.conn, protocol.Frame{Type: protocol.EvCapabilities, ID: domain.NewID(), Payload: caps})
}

func (n *testNode) send(e *env, typ, runID string, epoch int64, payload any) {
	b, _ := json.Marshal(payload)
	e.hub.RunnerFrame(e.ctx, n.conn, protocol.Frame{Type: typ, ID: domain.NewID(), RunID: runID, LeaseEpoch: epoch, Payload: b})
}

// find returns the first frame matching the predicate.
func (n *testNode) find(match func(protocol.Frame) bool) (protocol.Frame, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, f := range n.frames {
		if match(f) {
			return f, true
		}
	}
	return protocol.Frame{}, false
}

func (n *testNode) waitFrame(e *env, what string, match func(protocol.Frame) bool) protocol.Frame {
	e.t.Helper()
	var got protocol.Frame
	e.waitFor(what, 15*time.Second, func() bool {
		e.hub.Tick(e.ctx)
		f, ok := n.find(match)
		got = f
		return ok
	})
	return got
}

// Cancelling work whose run was offered but not yet started frees the
// engineer: the run is cancelled (not stuck stopping), the runner is told
// to drop the attempt, and the next request is offered normally.
func TestRegressionCancelOfferedRunIsFreed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	e.post("Engineering", "@Mira hello", []string{"mira"}, nil)
	offer := n.waitFrame(e, "first offer", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
	run, _ := store.GetRun(e.ctx, e.hub.Store().R(), offer.RunID)
	if _, err := e.hub.CancelJob(e.ctx, e.ownerID(), run.JobID, protocol.CancelJobRequest{Reason: "changed my mind"}); err != nil {
		t.Fatal(err)
	}
	// The runner's acceptance arrives after the cancel was committed.
	n.send(e, protocol.EvRunAck, offer.RunID, offer.LeaseEpoch, protocol.RunAck{CommandID: offer.ID, Accepted: true, State: "accepted"})
	n.waitFrame(e, "cancel sent to the runner", func(f protocol.Frame) bool {
		return f.Type == protocol.CmdCancelRun && f.RunID == offer.RunID
	})
	run, _ = store.GetRun(e.ctx, e.hub.Store().R(), offer.RunID)
	if run.State != protocol.RunCancelled {
		t.Fatalf("an offered run should be cancelled outright, got %s", run.State)
	}
	e.post("Engineering", "@Mira second", []string{"mira"}, nil)
	n.waitFrame(e, "second offer", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun && f.RunID != offer.RunID })
}

// A tool call re-sent after a reconnect returns the recorded result instead
// of running its mutation twice.
func TestRegressionToolCallRetryRunsOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	e.post("Engineering", "@Mira hello", []string{"mira"}, nil)
	offer := n.waitFrame(e, "offer", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
	n.send(e, protocol.EvRunAck, offer.RunID, offer.LeaseEpoch, protocol.RunAck{CommandID: offer.ID, Accepted: true, State: "accepted"})
	args, _ := json.Marshal(map[string]any{"body": "posted exactly once"})
	call := protocol.ToolCall{CallID: "call-1", Tool: "room_post", Args: args}
	first := e.hub.HandleToolCall(e.ctx, n.id, offer.RunID, offer.LeaseEpoch, call)
	again := e.hub.HandleToolCall(e.ctx, n.id, offer.RunID, offer.LeaseEpoch, call)
	if !first.OK || !again.OK || string(first.Result) != string(again.Result) {
		t.Fatalf("a retried call should return the recorded result: %+v / %+v", first, again)
	}
	count := 0
	for _, m := range e.messages("Engineering") {
		if m.Body == "posted exactly once" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the mutation ran %d times", count)
	}
	// After cancellation no further calls are admitted.
	run, _ := store.GetRun(e.ctx, e.hub.Store().R(), offer.RunID)
	if _, err := e.hub.CancelJob(e.ctx, e.ownerID(), run.JobID, protocol.CancelJobRequest{}); err != nil {
		t.Fatal(err)
	}
	late := e.hub.HandleToolCall(e.ctx, n.id, offer.RunID, offer.LeaseEpoch, protocol.ToolCall{CallID: "call-2", Tool: "room_post", Args: args})
	if late.OK {
		t.Fatalf("a call after cancellation must be refused")
	}
}

// The hub acknowledges a terminal report explicitly, including one for a
// run it doesn't know, so the runner stops re-sending it.
func TestRegressionTerminalAckIsExplicit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	n.send(e, protocol.EvRunTerminal, "no-such-run", 3, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, LastSeq: 4, ExitConfirmed: true})
	f, ok := n.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdAck && f.RunID == "no-such-run" })
	if !ok {
		t.Fatalf("no ack for an unknown run's terminal report")
	}
	var a protocol.Ack
	_ = json.Unmarshal(f.Payload, &a)
	if !a.Terminal || f.LeaseEpoch != 3 {
		t.Fatalf("the ack must settle the terminal report for that epoch: %+v epoch=%d", a, f.LeaseEpoch)
	}
}

// Runner artifact access follows the current credential and assignment.
func TestRegressionRunnerArtifactAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	art := protocol.Artifact{ID: domain.NewID(), Kind: "bundle", Name: "x.bundle", Hash: strings.Repeat("a", 64), Size: 1, CreatedAt: time.Now()}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error { return store.InsertArtifact(e.ctx, tx, e.hub.Org().ID, art) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.hub.OpenArtifactForNode(e.ctx, n.id, art.ID); err == nil {
		t.Fatalf("a machine could fetch a bundle no run of its names")
	}
	if e.hub.NodeHoldsRun(e.ctx, n.id) {
		t.Fatalf("an idle machine should not be allowed to upload")
	}
	if err := e.hub.AuthorizeNode(e.ctx, n.id, n.conn.Serial); err != nil {
		t.Fatalf("the current credential should be accepted: %v", err)
	}
	if err := e.hub.AuthorizeNode(e.ctx, n.id, "superseded"); err == nil {
		t.Fatalf("a superseded certificate was accepted")
	}
	if err := e.hub.RevokeNode(e.ctx, e.ownerID(), n.id); err != nil {
		t.Fatal(err)
	}
	if err := e.hub.AuthorizeNode(e.ctx, n.id, n.conn.Serial); err == nil {
		t.Fatalf("a revoked credential was accepted")
	}
}

// An answer that lands after an attempt was offered but before it reports
// that it started (its machine is still preparing the workspace) is missing
// from the manifest the attempt was planned from, even though the runner
// hands it to the provider as soon as the session starts. When that attempt
// parks on the question again, the job resumes with the answer instead of
// waiting forever. The machine is played by hand to hold that window open.
func TestRegressionAnswerWhileStartingResumes(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("slow-box")
	seen := map[string]bool{}
	accept := func(what string, jobID string) protocol.Frame {
		t.Helper()
		f := n.waitFrame(e, what, func(f protocol.Frame) bool {
			if f.Type != protocol.CmdOfferRun || seen[f.RunID] {
				return false
			}
			run, err := store.GetRun(e.ctx, e.hub.Store().R(), f.RunID)
			return err == nil && (jobID == "" || run.JobID == jobID)
		})
		seen[f.RunID] = true
		n.send(e, protocol.EvRunAck, f.RunID, f.LeaseEpoch, protocol.RunAck{CommandID: f.ID, Accepted: true, State: "accepted"})
		return f
	}
	seq := map[string]int64{}
	event := func(offer protocol.Frame, kind string, data any) {
		seq[offer.RunID]++
		ev := protocol.RunEvent{Seq: seq[offer.RunID], Kind: kind, At: time.Now()}
		if data != nil {
			ev.Data, _ = json.Marshal(data)
		}
		n.send(e, protocol.EvRunEvent, offer.RunID, offer.LeaseEpoch, ev)
	}
	call := func(offer protocol.Frame, tool string, args map[string]any) {
		t.Helper()
		b, _ := json.Marshal(args)
		if res := e.hub.HandleToolCall(e.ctx, n.id, offer.RunID, offer.LeaseEpoch, protocol.ToolCall{CallID: domain.NewID(), Tool: tool, Args: b}); !res.OK {
			t.Fatalf("%s: %+v", tool, res.Error)
		}
	}
	finish := func(offer protocol.Frame) {
		n.send(e, protocol.EvRunTerminal, offer.RunID, offer.LeaseEpoch, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true, LastSeq: seq[offer.RunID]})
	}
	manifestOf := func(offer protocol.Frame) manifest.Manifest {
		t.Helper()
		run, err := store.GetRun(e.ctx, e.hub.Store().R(), offer.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var m manifest.Manifest
		if err := json.Unmarshal(run.Manifest, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	answered := func(m manifest.Manifest) bool {
		for _, in := range m.Inputs {
			if in.Kind == "answer" && strings.Contains(in.Body, "us-east-1") {
				return true
			}
		}
		return false
	}

	e.post("Engineering", "@Mira plan the rollout", []string{"mira"}, nil)
	reply := accept("the reply", "")
	event(reply, protocol.RunEvStarted, nil)
	call(reply, "work_create", map[string]any{"title": "Plan rollout", "objective": "plan", "kind": "investigation", "project": "Atlas"})
	finish(reply)
	j, ok := e.job("Plan rollout")
	if !ok {
		t.Fatal("the work was not created")
	}

	first := accept("the first attempt", j.ID)
	event(first, protocol.RunEvStarted, nil)
	call(first, "human_ask", map[string]any{"question": "Which region first?", "missingFact": "region", "contextChecked": "docs"})
	call(first, "work_wait", map[string]any{"reason": "missing_information"})
	finish(first)
	e.waitJob("Plan rollout", protocol.JobWaiting)
	qs := e.jobDetail(j.ID).Questions
	if len(qs) != 1 {
		t.Fatalf("questions: %+v", qs)
	}

	// A clarification starts a second attempt, planned before any answer.
	e.c.must("POST", "/v1/jobs/"+j.ID+"/input", protocol.JobInputRequest{Body: "also keep the error codes", ClientKey: domain.NewID()}, nil)
	second := accept("the clarification's attempt", j.ID)
	if answered(manifestOf(second)) {
		t.Fatal("the second attempt was planned after the answer; this test needs it planned before")
	}

	// Its machine is still preparing the workspace when the owner answers.
	answer := e.post("Engineering", "us-east-1", nil, func(r *protocol.PostMessageRequest) { r.ReplyToID = qs[0].MessageID })
	if len(answer.Resolved) != 1 {
		t.Fatalf("the answer did not reach the question: %+v", answer)
	}
	deliver := n.waitFrame(e, "the answer sent to the preparing attempt", func(f protocol.Frame) bool {
		return f.Type == protocol.CmdDeliverInput && f.RunID == second.RunID
	})
	var in protocol.DeliverInput
	if err := json.Unmarshal(deliver.Payload, &in); err != nil {
		t.Fatal(err)
	}

	// The session starts and takes the answer at once, but this turn was
	// planned without it: it parks on the question again.
	event(second, protocol.RunEvStarted, nil)
	event(second, protocol.RunEvInputDelivered, protocol.InputDelivered{InputID: in.InputID, Mode: "immediate"})
	call(second, "work_wait", map[string]any{"reason": "missing_information"})
	finish(second)

	third := accept("an attempt planned with the answer", j.ID)
	if m := manifestOf(third); m.Purpose != "answer" || !answered(m) {
		t.Fatalf("the resumed attempt should carry the answer: purpose %q, inputs %+v", m.Purpose, m.Inputs)
	}
}

// A forge refresh doesn't hold the single writer while it waits on the
// network.
func TestRegressionForgeRefreshOutsideWriter(t *testing.T) {
	// Not parallel: it bounds how long a write waits.
	e := newEnv(t, envOptions{forge: func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
		return slowForge{d: 700 * time.Millisecond}, forge.RepoRef{Host: "github.com", Owner: "acme", Name: "atlas"}, nil
	}})
	repos, _ := store.ListRepos(e.ctx, e.hub.Store().R())
	var prID string
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(e.ctx, `UPDATE repos SET forge = 'github', forge_repo = 'acme/atlas' WHERE id = ?`, repos[0].ID); err != nil {
			return err
		}
		var err error
		prID, err = store.UpsertPR(e.ctx, tx, e.hub.Org().ID, protocol.PullRequest{RepoID: repos[0].ID, Forge: "github", Host: "github.com", Owner: "acme", Name: "atlas", Number: 7})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.hub.GetPullRequest(context.Background(), e.ownerID(), prID, true)
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	e.post("Engineering", "unrelated note", nil, nil)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("an unrelated write waited %v behind a forge request", d)
	}
	<-done
}

// Sign-in attempts are bounded per client, and oversized input is refused
// before any password hashing.
func TestRegressionSignInIsBounded(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	if _, _, err := e.hub.SignIn(e.ctx, "brayden", strings.Repeat("x", 5000), "client-a", "test"); err == nil {
		t.Fatalf("an oversized password was accepted")
	}
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		_, _, err := e.hub.SignIn(e.ctx, "user"+string(rune('a'+i%26))+strings.Repeat("z", i/26), "wrong-password", "client-b", "test")
		limited = domain.AsError(err).Code == "limit_reached"
	}
	if !limited {
		t.Fatalf("rotating handles bypassed the per-client limit")
	}
	if _, _, err := e.hub.SignIn(e.ctx, "brayden", "correct-horse-battery", "client-c", "test"); err != nil {
		t.Fatalf("another client should still sign in: %v", err)
	}
}

// slowForge answers like GitHub after a delay.
type slowForge struct{ d time.Duration }

func (s slowForge) Name() string { return "github" }
func (s slowForge) Viewer(ctx context.Context) (forge.Actor, error) {
	time.Sleep(s.d)
	return forge.Actor{Login: "bot"}, nil
}
func (s slowForge) ParseURL(u string) (forge.RepoRef, int, error) { return forge.RepoRef{}, 0, nil }
func (s slowForge) GetPR(ctx context.Context, r forge.RepoRef, n int) (forge.PR, error) {
	time.Sleep(s.d)
	return forge.PR{Number: n, State: "open", Head: "h", Base: "b"}, nil
}
func (s slowForge) Files(ctx context.Context, r forge.RepoRef, n int) ([]forge.FileDiff, error) {
	return nil, nil
}
func (s slowForge) Checks(ctx context.Context, r forge.RepoRef, sha string) (protocol.ChecksSummary, error) {
	return protocol.ChecksSummary{}, nil
}
func (s slowForge) Reviews(ctx context.Context, r forge.RepoRef, n int) ([]protocol.RemoteReview, error) {
	return nil, nil
}
func (s slowForge) MergeStatus(ctx context.Context, r forge.RepoRef, n int) (protocol.MergeStatus, error) {
	return protocol.MergeStatus{}, nil
}
func (s slowForge) PublishReview(ctx context.Context, r forge.RepoRef, n int, req forge.PublishReview) (string, error) {
	return "", nil
}
func (s slowForge) FindReviewByMarker(ctx context.Context, r forge.RepoRef, n int, m string) (string, bool, error) {
	return "", false, nil
}
func (s slowForge) VerifyWebhook(secret []byte, headers map[string]string, body []byte) (forge.Webhook, error) {
	return forge.Webhook{}, nil
}

// No silent fallback to API billing, and a pinned account is respected:
// work waits (with the reason) instead of running on the wrong terms.
func TestRegressionBillingGateAndAccountPin(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNodeWith("api-box", protocol.ProviderInstallation{Provider: "codex", AuthState: protocol.AuthReady, ProfileID: "codex:api-key",
		Billing: protocol.BillingAPI, Capabilities: protocol.ProviderCapabilities{ReadOnly: true}})
	offered := func() bool {
		_, ok := n.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
		return ok
	}
	e.post("Engineering", "@Mira hello", []string{"mira"}, nil)
	for i := 0; i < 5; i++ {
		e.hub.Tick(e.ctx)
		time.Sleep(50 * time.Millisecond)
	}
	if offered() {
		t.Fatalf("an API-billed install was used without the engineer allowing API billing")
	}
	var reply protocol.Job
	e.waitFor("the reason is shown", 10*time.Second, func() bool {
		for _, j := range e.jobsWithReplies() {
			if j.Kind == protocol.JobKindReply && strings.Contains(j.StateDetail, "API key") {
				reply = j
				return true
			}
		}
		e.hub.Tick(e.ctx)
		return false
	})
	_ = reply
	// Pin Mira to a different account: still nothing runs.
	var mira protocol.Engineer
	e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
		Engineer *protocol.Engineer `json:"engineer"`
	}{&mira})
	pref := mira.Provider
	pref.ProfileID, pref.AllowAPIBilling = "codex:someone-else", true
	e.c.must("PATCH", "/v1/engineers/"+mira.ID, protocol.UpdateEngineerRequest{Version: mira.Version, Provider: &pref}, &mira)
	for i := 0; i < 5; i++ {
		e.hub.Tick(e.ctx)
		time.Sleep(50 * time.Millisecond)
	}
	if offered() {
		t.Fatalf("a run was offered to an account the engineer is not pinned to")
	}
	// Allowing API billing on the right account lets it run.
	pref.ProfileID = "codex:api-key"
	e.c.must("PATCH", "/v1/engineers/"+mira.ID, protocol.UpdateEngineerRequest{Version: mira.Version, Provider: &pref}, &mira)
	e.post("Engineering", "@Mira hello again", []string{"mira"}, nil)
	n.waitFrame(e, "an offer once allowed", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
}

func (e *env) jobsWithReplies() []protocol.Job {
	js, _ := store.ListJobs(e.ctx, e.hub.Store().R(), store.JobFilter{IncludeReply: true})
	out := make([]protocol.Job, 0, len(js))
	for _, j := range js {
		out = append(out, j.Job)
	}
	return out
}

// Search finds work by its short ID, and a project filter narrows results
// to that project before ranking.
func TestRegressionSearchByWorkIDAndProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	e.post("Engineering", "@Mira look at the quartzite cache", []string{"mira"}, nil)
	var job protocol.Job
	e.waitFor("the work exists", 5*time.Second, func() bool {
		for _, j := range e.jobsWithReplies() {
			job = j
			return true
		}
		return false
	})
	short := job.ID[len(job.ID)-6:]
	for _, q := range []string{short, "#" + short, job.ID} {
		var rs []protocol.SearchResult
		e.c.must("GET", "/v1/search?q="+url.QueryEscape(q), nil, &rs)
		found := false
		for _, r := range rs {
			found = found || (r.Kind == "job" && r.ID == job.ID)
		}
		if !found {
			t.Errorf("searching %q did not find the work: %+v", q, rs)
		}
	}
	// A project no room here is linked to hides the room's messages and work.
	var p protocol.Project
	e.c.must("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Elsewhere"}, &p)
	var rs []protocol.SearchResult
	e.c.must("GET", "/v1/search?q=quartzite&project="+p.ID, nil, &rs)
	if len(rs) != 0 {
		t.Errorf("the project filter let other rooms through: %+v", rs)
	}
	e.c.must("GET", "/v1/search?q=quartzite", nil, &rs)
	if len(rs) == 0 {
		t.Errorf("unfiltered search found nothing")
	}
}

// Output a machine sends under an old lease epoch never changes the work;
// it is kept, once, as quarantined diagnostic evidence.
func TestRegressionStaleOutputIsQuarantined(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("old-box")
	e.post("Engineering", "@Mira hello", []string{"mira"}, nil)
	offer := n.waitFrame(e, "an offer", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
	old := offer.LeaseEpoch
	// The lease moves on (e.g. the hub reassigned it after a partition).
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE runs SET lease_epoch = lease_epoch + 1 WHERE id = ?`, offer.RunID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ev := protocol.RunEvent{Seq: 1, Kind: protocol.RunEvStatus, Text: "Pushed token=abcdefghijklmnopqrstuvwxyz0123 to origin", At: time.Now()}
	n.send(e, protocol.EvRunEvent, offer.RunID, old, ev)
	n.send(e, protocol.EvRunEvent, offer.RunID, old, ev) // redelivery
	n.send(e, protocol.EvRunTerminal, offer.RunID, old, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, FinalText: "All done!", ExitConfirmed: true})
	n.send(e, protocol.EvToolCall, offer.RunID, old, protocol.ToolCall{CallID: "c1", Tool: "room_post", Args: json.RawMessage(`{"body":"late"}`)})
	var d protocol.JobDetail
	e.waitFor("three quarantined reports", 10*time.Second, func() bool {
		js := e.jobsWithReplies()
		if len(js) == 0 {
			return false
		}
		d = e.jobDetail(js[0].ID)
		return len(d.Quarantined) == 3
	})
	kinds := map[string]bool{}
	for _, q := range d.Quarantined {
		kinds[q.Kind] = true
		if q.Epoch != old || q.CurrentEpoch != old+1 {
			t.Errorf("epochs not recorded: %+v", q)
		}
		if strings.Contains(q.Summary, "abcdefghijklmnopqrstuvwxyz0123") {
			t.Errorf("a secret survived into quarantine: %q", q.Summary)
		}
	}
	if !kinds["event"] || !kinds["terminal"] || !kinds["tool_call"] {
		t.Fatalf("missing kinds: %v", kinds)
	}
	if d.Job.State == protocol.JobCompleted {
		t.Fatalf("a stale report completed the work")
	}
	for _, m := range e.messages("Engineering") {
		if m.Body == "All done!" || m.Body == "late" {
			t.Fatalf("stale output reached the room: %q", m.Body)
		}
	}
}

// A change sent twice with the same Idempotency-Key acts once and replays its
// first result; the key can't be reused for a different request.
func TestRegressionIdempotencyKeys(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	var replayed bool
	c := e.c.withKey("create-project-once-123")
	c.lastReplayed = &replayed
	var a, b protocol.Project
	c.must("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Gamma"}, &a)
	if replayed {
		t.Fatalf("the first request must run, not replay")
	}
	c.must("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Gamma"}, &b)
	if !replayed || a.ID != b.ID {
		t.Fatalf("the repeat should replay the first result: %s vs %s (replayed %v)", a.ID, b.ID, replayed)
	}
	var ps []protocol.Project
	e.c.must("GET", "/v1/projects", nil, &ps)
	n := 0
	for _, p := range ps {
		if p.Name == "Gamma" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected one Gamma project, got %d", n)
	}
	if err := c.do("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Delta"}, nil); !isStatus(err, 400) {
		t.Fatalf("reusing a key for a different request must be refused: %v", err)
	}
	// A request that failed validation replays the same answer, not a new attempt.
	bad := e.c.withKey("bad-project-once-456")
	if err := bad.do("POST", "/v1/projects", protocol.CreateProjectRequest{Name: ""}, nil); !isStatus(err, 400) {
		t.Fatalf("expected a validation error: %v", err)
	}
	if err := bad.do("POST", "/v1/projects", protocol.CreateProjectRequest{Name: ""}, nil); !isStatus(err, 400) {
		t.Fatalf("expected the same validation error on replay: %v", err)
	}
	// Without a key, requests behave as before.
	e.c.must("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Epsilon"}, nil)
}
