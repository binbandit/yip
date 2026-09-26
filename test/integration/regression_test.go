package integration

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// Regression tests for the backend review. Each names the defect it guards.

func (e *env) ownerID() string {
	us, _ := store.ListUsers(e.ctx, e.hub.Store().R())
	return us[0].ID
}

// fakeNode pairs a machine and connects it in-process, recording every frame
// the hub sends it, so tests can play the runner's side exactly.
type fakeNode struct {
	id     string
	conn   *hub.NodeConn
	mu     sync.Mutex
	frames []protocol.Frame
}

func (e *env) fakeNode(name string) *fakeNode {
	return e.fakeNodeWith(name, protocol.ProviderInstallation{Provider: "fake", AuthState: protocol.AuthReady, ProfileID: "fake:local",
		Capabilities: protocol.ProviderCapabilities{ReadOnly: true}})
}

// fakeNodeWith connects an in-process machine offering one installation.
func (e *env) fakeNodeWith(name string, inst protocol.ProviderInstallation) *fakeNode {
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
	n := &fakeNode{id: id}
	n.conn = &hub.NodeConn{NodeID: id, Serial: serial, ConnectedAt: time.Now(), Send: func(f protocol.Frame) error {
		n.mu.Lock()
		n.frames = append(n.frames, f)
		n.mu.Unlock()
		return nil
	}}
	if err := e.hub.ConnectRunner(e.ctx, n.conn, protocol.Hello{NodeID: id, ProtocolVersion: protocol.RunnerProtocolVersion}); err != nil {
		e.t.Fatal(err)
	}
	caps, _ := json.Marshal(protocol.RunnerCapabilities{Slots: 2,
		Profiles:  []protocol.ExecutionProfile{{Name: "native", Available: true}, {Name: "readonly", Available: true}},
		Providers: []protocol.ProviderInstallation{inst}})
	e.hub.RunnerFrame(e.ctx, n.conn, protocol.Frame{Type: protocol.EvCapabilities, ID: domain.NewID(), Payload: caps})
	return n
}

func (n *fakeNode) send(e *env, typ, runID string, epoch int64, payload any) {
	b, _ := json.Marshal(payload)
	e.hub.RunnerFrame(e.ctx, n.conn, protocol.Frame{Type: typ, ID: domain.NewID(), RunID: runID, LeaseEpoch: epoch, Payload: b})
}

// find returns the first frame matching the predicate.
func (n *fakeNode) find(match func(protocol.Frame) bool) (protocol.Frame, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, f := range n.frames {
		if match(f) {
			return f, true
		}
	}
	return protocol.Frame{}, false
}

func (n *fakeNode) waitFrame(e *env, what string, match func(protocol.Frame) bool) protocol.Frame {
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

// A conversational reply that reports work_update(failed) must not wedge
// the engineer or the machine: the next request is still answered.
func TestRegressionReplyFailureDoesNotWedge(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "impossible"):
			return script(toolStep("work_update", map[string]any{"state": "failed", "summary": "Can't do that."}, "u"),
				toolStep("room_post", map[string]any{"body": "update: {{u.note}}{{u.error.message}}"}, ""),
				fake.Step{Final: "Sorry, not possible."})
		case replyTo(m, "Engineering", "second"):
			return script(fake.Step{Final: "second request answered"})
		}
		return nil
	}})
	e.post("Engineering", "@Mira do the impossible", []string{"mira"}, nil)
	e.waitMessage("Engineering", "update:")
	e.post("Engineering", "@Mira second request", []string{"mira"}, nil)
	e.waitMessage("Engineering", "second request answered")
	e.waitFor("every attempt settled", 20*time.Second, func() bool {
		held, _ := store.RunsInStates(e.ctx, e.hub.Store().R(), protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
			protocol.RunAwaitingInput, protocol.RunStopping, protocol.RunUnknown)
		return len(held) == 0
	})
}

// work_respond only answers a help request addressed to the caller; a code
// job can't use it to complete itself without evidence.
func TestRegressionWorkRespondCannotSelfComplete(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "fix"):
			return script(toolStep("work_create", map[string]any{"title": "Fix Y", "objective": "fix y", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Job.Title == "Fix Y":
			return script(toolStep("work_respond", map[string]any{"requestId": m.Job.ID, "body": "Done, trust me."}, "r"),
				toolStep("room_post", map[string]any{"body": "respond: {{r.error.message}}"}, ""),
				toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
		}
		return nil
	}})
	e.post("Engineering", "@Mira fix Y", []string{"mira"}, nil)
	msg := e.waitMessage("Engineering", "respond:")
	if strings.TrimSpace(strings.TrimPrefix(msg.Body, "respond:")) == "" {
		t.Fatalf("work_respond on its own job must be refused: %q", msg.Body)
	}
	j := e.waitJob("Fix Y", protocol.JobFailed)
	if j.State == protocol.JobCompleted {
		t.Fatalf("the job completed itself without evidence")
	}
}

// An engineer removed from a private room is neither woken by replies in a
// thread they started nor given the room's new messages.
func TestRegressionRemovedMemberNotRoutedOrLeaked(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.RoomName == "Incident" && m.Job.Kind == "reply" {
			mu.Lock()
			for _, c := range m.Conversation {
				seen = append(seen, m.Engineer.Name+": "+c.Body)
			}
			if m.Request != nil {
				seen = append(seen, m.Engineer.Name+": "+m.Request.Body)
			}
			mu.Unlock()
			return script(fake.Step{Final: "Replying from " + m.Engineer.Name})
		}
		return nil
	}})
	var incident protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Incident", Private: true,
		EngineerIDs: []string{e.engineerID("mira"), e.engineerID("oren")}}, &incident)
	first := e.post("Incident", "@Mira start here", []string{"mira"}, nil)
	e.waitMessage("Incident", "Replying from")
	e.c.must("DELETE", "/v1/rooms/"+incident.ID+"/members/"+e.engineerID("mira"), nil, nil)
	resp := e.post("Incident", "SECRET-CANARY: the breach came from host db-7", nil,
		func(r *protocol.PostMessageRequest) { r.ThreadID = first.Message.ID })
	for _, id := range resp.Dispatched {
		if id == e.engineerID("mira") {
			t.Fatalf("a removed engineer was woken by a reply in their old thread")
		}
	}
	time.Sleep(2 * time.Second)
	e.hub.Tick(e.ctx)
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if strings.HasPrefix(s, "Mira: ") && strings.Contains(s, "SECRET-CANARY") {
			t.Fatalf("the removed engineer received the room's new message: %q", s)
		}
	}
}

// Cancelling work whose run was offered but not yet started frees the
// engineer: the run is cancelled (not stuck stopping), the runner is told
// to drop the attempt, and the next request is offered normally.
func TestRegressionCancelOfferedRunIsFreed(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNode("fake-runner")
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
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNode("fake-runner")
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
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNode("fake-runner")
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
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNode("fake-runner")
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

// An owner's answer that lands while the asking run is still going is not
// lost when that run then parks the job: the job resumes with the answer.
func TestRegressionAnswerBeforeWaitResumes(t *testing.T) {
	var attempts atomic.Int32
	var answer atomic.Value
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "rollout"):
			return script(toolStep("work_create", map[string]any{"title": "Plan rollout", "objective": "plan", "kind": "investigation", "project": "Atlas"}, ""))
		case m.Job.Title == "Plan rollout":
			if attempts.Add(1) > 1 {
				for _, in := range m.Inputs {
					if in.Kind == "answer" {
						answer.Store(in.Body)
					}
				}
				return script(toolStep("room_post", map[string]any{"body": "rollout resumed"}, ""),
					toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
			}
			return script(toolStep("human_ask", map[string]any{"question": "Which region first?", "missingFact": "region", "contextChecked": "docs"}, ""),
				fake.Step{Sleep: "2s"},
				toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
		}
		return nil
	}})
	e.post("Engineering", "@Mira plan the rollout", []string{"mira"}, nil)
	var q protocol.Message
	e.waitFor("question", 30*time.Second, func() bool {
		for _, m := range e.messages("Engineering") {
			if m.Kind == protocol.MessageQuestion && strings.Contains(m.Body, "Which region") {
				q = m
				return true
			}
		}
		return false
	})
	e.post("Engineering", "us-east-1", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = q.ID })
	e.waitMessage("Engineering", "rollout resumed")
	if a, _ := answer.Load().(string); !strings.Contains(a, "us-east-1") {
		t.Fatalf("the resumed run did not receive the answer: %q", a)
	}
}

// A delegated child that fails resolves its parent's dependency, and the
// parent is told the outcome.
func TestRegressionFailedChildResolvesParent(t *testing.T) {
	var resumed atomic.Value
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "survey"):
			return script(toolStep("work_create", map[string]any{"title": "Survey", "objective": "survey", "kind": "investigation", "project": "Atlas"}, ""))
		case m.Job.Title == "Survey":
			for _, in := range m.Inputs {
				if in.Kind == "dependency_outcome" {
					resumed.Store(in.Body)
					return script(toolStep("room_post", map[string]any{"body": "survey resumed"}, ""),
						toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
				}
			}
			return script(toolStep("work_create", map[string]any{"title": "Sub-survey", "objective": "part", "kind": "investigation", "project": "Atlas", "owner": "oren"}, ""),
				toolStep("work_wait", map[string]any{"reason": "dependency"}, ""))
		case m.Job.Title == "Sub-survey":
			return script(toolStep("work_update", map[string]any{"state": "failed", "summary": "No access to the survey data."}, ""))
		}
		return nil
	}})
	e.post("Security", "@Mira survey the fleet", []string{"mira"}, nil)
	e.waitMessage("Security", "survey resumed")
	if b, _ := resumed.Load().(string); !strings.Contains(b, "No access") {
		t.Fatalf("the parent resumed without the child's outcome: %q", b)
	}
}

// One approval doesn't outvote another reviewer's requested changes.
func TestRegressionCompletionNeedsEveryReviewer(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "both"):
			return script(toolStep("work_create", map[string]any{"title": "Two reviewers", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Review != nil && m.Engineer.Name == "Oren":
			return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Fine.", "message": "Approved."}, ""))
		case m.Review != nil:
			return script(toolStep("work_review", map[string]any{"verdict": "changes_requested", "expectedHead": m.Review.Head, "summary": "Not yet.",
				"message": "Changes needed.", "findings": []map[string]any{{"severity": "blocking", "file": "NOTES.md", "line": 1, "body": "Wrong note."}}}, ""))
		case m.Job.Title == "Two reviewers" && m.Job.Head == "":
			return script(fake.Step{Write: &fake.WriteFile{Path: "NOTES.md", Content: "note\n"}},
				toolStep("work_publish_revision", map[string]any{"summary": "note"}, ""),
				toolStep("work_run_check", map[string]any{"command": "true"}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "@oren please review"}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "pip", "message": "@pip please review"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))
		case m.Job.Title == "Two reviewers":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "done"}, "u"),
				toolStep("room_post", map[string]any{"body": "complete: {{u.error.message}}"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))
		}
		return nil
	}})
	e.c.must("PUT", "/v1/rooms/"+e.roomID("Security")+"/members/"+e.engineerID("pip"), nil, nil)
	e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+e.engineerID("pip"), protocol.PutGrantRequest{Access: "read"}, nil)
	e.post("Security", "@Mira both reviewers", []string{"mira"}, nil)
	e.waitFor("both verdicts", 60*time.Second, func() bool {
		j, ok := e.job("Two reviewers")
		if !ok {
			return false
		}
		decided := 0
		for _, r := range e.jobDetail(j.ID).Reviews {
			if r.State == protocol.ReviewApproved || r.State == protocol.ReviewChangesRequested {
				decided++
			}
		}
		return decided == 2
	})
	e.waitFor("a refused completion naming Pip", 60*time.Second, func() bool {
		for _, m := range e.messages("Security") {
			if strings.HasPrefix(m.Body, "complete:") && strings.Contains(m.Body, "Pip") {
				return true
			}
		}
		return false
	})
	if j, _ := e.job("Two reviewers"); j.State == protocol.JobCompleted {
		t.Fatalf("completed despite a reviewer's requested changes")
	}
}

// Deleting a message removes its text from events, job text, run context
// and search.
func TestRegressionRedactionIsComplete(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if replyTo(m, "Engineering", "REDACT-CANARY") {
			return script(fake.Step{Final: "Noted."})
		}
		return nil
	}})
	sent := e.post("Engineering", "@Mira REDACT-CANARY is the staging password", []string{"mira"}, nil)
	e.waitMessage("Engineering", "Noted.")
	e.c.must("DELETE", "/v1/messages/"+sent.Message.ID, nil, nil)
	q := e.hub.Store().R()
	for what, query := range map[string]string{
		"events":   `SELECT COUNT(*) FROM events WHERE payload LIKE '%REDACT-CANARY%'`,
		"jobs":     `SELECT COUNT(*) FROM jobs WHERE title LIKE '%REDACT-CANARY%' OR objective LIKE '%REDACT-CANARY%'`,
		"runs":     `SELECT COUNT(*) FROM runs WHERE manifest LIKE '%REDACT-CANARY%'`,
		"messages": `SELECT COUNT(*) FROM messages WHERE body LIKE '%REDACT-CANARY%'`,
	} {
		var n int
		if err := q.QueryRowContext(e.ctx, query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if n != 0 {
			t.Errorf("the deleted text survives in %s (%d rows)", what, n)
		}
	}
	var results []protocol.SearchResult
	e.c.must("GET", "/v1/search?q=REDACT-CANARY", nil, &results)
	if len(results) != 0 {
		t.Errorf("search still finds the deleted text: %+v", results)
	}
}

// A forge refresh doesn't hold the single writer while it waits on the
// network.
func TestRegressionForgeRefreshOutsideWriter(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true, forge: func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
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

// work_run_check goes through the permission policy and runs without the
// machine owner's credentials; conversation replies can't run checks.
func TestRegressionRunCheckIsPolicedAndIsolated(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "env.txt")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/fake-agent.sock")
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "status"):
			return script(toolStep("work_run_check", map[string]any{"command": "true"}, "r"),
				toolStep("room_post", map[string]any{"body": "reply check: {{r.error}}"}, ""))
		case replyTo(m, "Security", "isolate"):
			return script(toolStep("work_create", map[string]any{"title": "Isolation", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Job.Title == "Isolation":
			return script(toolStep("work_run_check", map[string]any{"command": "echo \"HOME=$HOME SOCK=$SSH_AUTH_SOCK\" > " + marker}, ""),
				toolStep("work_run_check", map[string]any{"command": "git -C . push origin HEAD"}, "p"),
				toolStep("room_post", map[string]any{"body": "push check: {{p.error.message}}"}, ""),
				toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
		}
		return nil
	}})
	e.post("Security", "@Mira status?", []string{"mira"}, nil)
	if msg := e.waitMessage("Security", "reply check:"); !strings.Contains(msg.Body, "not available") {
		t.Fatalf("a conversation reply ran a check: %q", msg.Body)
	}
	e.post("Security", "@Mira isolate", []string{"mira"}, nil)
	var ap protocol.Approval
	e.waitFor("an approval request for the push", 60*time.Second, func() bool {
		j, ok := e.job("Isolation")
		if !ok {
			return false
		}
		for _, a := range e.jobDetail(j.ID).Approvals {
			if a.Status == "pending" {
				ap = a
				return true
			}
		}
		return false
	})
	if !strings.Contains(ap.Action.Command, "push") {
		t.Fatalf("unexpected approval: %+v", ap.Action)
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the routine check did not run: %v", err)
	}
	env := string(b)
	if !strings.Contains(env, string(filepath.Separator)+"scratch"+string(filepath.Separator)) || strings.Contains(env, "fake-agent") {
		t.Fatalf("the check ran with the owner's HOME or SSH agent: %q", env)
	}
	e.c.must("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "reject", Version: ap.Version}, nil)
	if msg := e.waitMessage("Security", "push check:"); !strings.Contains(msg.Body, "Not permitted") {
		t.Fatalf("a rejected check should not run: %q", msg.Body)
	}
}

// Sign-in attempts are bounded per client, and oversized input is refused
// before any password hashing.
func TestRegressionSignInIsBounded(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
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

// A check that outlives its timeout is stopped as a process group: it ends
// with exit 124 promptly and nothing it would have done afterwards happens.
func TestRegressionCheckTimeoutStopsProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late.txt")
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "slow"):
			return script(toolStep("work_create", map[string]any{"title": "Slow check", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Job.Title == "Slow check":
			return script(toolStep("work_run_check", map[string]any{"command": "sleep 20; echo late > " + marker, "timeoutSeconds": 1}, "r"),
				toolStep("room_post", map[string]any{"body": "check exit: {{r.exitCode}}"}, ""),
				toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
		}
		return nil
	}})
	start := time.Now()
	e.post("Security", "@Mira slow check", []string{"mira"}, nil)
	msg := e.waitMessage("Security", "check exit:")
	if msg.Body != "check exit: 124" {
		t.Fatalf("a timed-out check should report exit 124: %q", msg.Body)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("the check was not stopped at its timeout (took %v)", d)
	}
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the check's process kept running after its timeout")
	}
}

// No silent fallback to API billing, and a pinned account is respected:
// work waits (with the reason) instead of running on the wrong terms.
func TestRegressionBillingGateAndAccountPin(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNodeWith("api-box", protocol.ProviderInstallation{Provider: "fake", AuthState: protocol.AuthReady, ProfileID: "fake:api-key",
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
	pref.ProfileID, pref.AllowAPIBilling = "fake:someone-else", true
	e.c.must("PATCH", "/v1/engineers/"+mira.ID, protocol.UpdateEngineerRequest{Version: mira.Version, Provider: &pref}, &mira)
	for i := 0; i < 5; i++ {
		e.hub.Tick(e.ctx)
		time.Sleep(50 * time.Millisecond)
	}
	if offered() {
		t.Fatalf("a run was offered to an account the engineer is not pinned to")
	}
	// Allowing API billing on the right account lets it run.
	pref.ProfileID = "fake:api-key"
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

// "Since you were here" reports each job's current state: an earlier wait
// is history once the job is running again.
func TestRegressionCatchupShowsCurrentState(t *testing.T) {
	var attempts atomic.Int32
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "plan"):
			return script(toolStep("work_create", map[string]any{"title": "Plan migration", "objective": "plan", "kind": "investigation", "project": "Atlas"}, ""))
		case m.Job.Title == "Plan migration":
			if attempts.Add(1) > 1 {
				return script(toolStep("room_post", map[string]any{"body": "migration resumed"}, ""), fake.Step{Sleep: "4s"},
					toolStep("work_update", map[string]any{"state": "failed", "summary": "test done"}, ""))
			}
			return script(toolStep("human_ask", map[string]any{"question": "Which database first?", "missingFact": "database", "contextChecked": "docs"}, ""),
				toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
		}
		return nil
	}})
	e.post("Engineering", "@Mira plan the migration", []string{"mira"}, nil)
	var q protocol.Message
	e.waitFor("question", 30*time.Second, func() bool {
		for _, m := range e.messages("Engineering") {
			if m.Kind == protocol.MessageQuestion {
				q = m
				return true
			}
		}
		return false
	})
	e.waitJob("Plan migration", protocol.JobWaiting)
	e.post("Engineering", "Postgres first.", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = q.ID })
	e.waitMessage("Engineering", "migration resumed")
	var ov protocol.Overview
	e.c.must("GET", "/v1/overview", nil, &ov)
	for _, c := range ov.Catchup {
		if strings.Contains(c.Title, "Plan migration") && c.Kind == "blocker" {
			t.Fatalf("a job that is running again was reported as blocked: %+v", c)
		}
	}
}

// Search finds work by its short ID, and a project filter narrows results
// to that project before ranking.
func TestRegressionSearchByWorkIDAndProject(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
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

// With no other engineer in the conversation, code work is reviewed by the
// owner instead of waiting forever for a peer.
func TestRegressionSoloEngineerOwnerReviews(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.project("Atlas")
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Solo", EngineerIDs: []string{e.engineerID("mira")}, ProjectIDs: []string{a.ID}}, nil)
	e.post("Solo", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	j := e.waitJob("Fix Atlas session expiry", protocol.JobReviewReady, protocol.JobCompleted)
	if j.RequiresPeerReview || !j.RequiresHumanReview {
		t.Fatalf("expected owner review in place of peer review: peer=%v human=%v", j.RequiresPeerReview, j.RequiresHumanReview)
	}
	e.waitFor("ready for the owner's review", 60*time.Second, func() bool {
		j, _ = e.job("Fix Atlas session expiry")
		return j.State == protocol.JobReviewReady && j.Revision != nil && j.Revision.Head != ""
	})
	var done protocol.Job
	e.c.must("POST", "/v1/jobs/"+j.ID+"/accept", protocol.AcceptJobRequest{Revision: j.Revision.Head, Version: j.Version}, &done)
	if done.State != protocol.JobCompleted {
		t.Fatalf("accept should complete: %s", done.State)
	}
}

// Asking the Overview where things stand covers every project the user's
// rooms reach, naming quiet ones, and what waits on the user.
func TestRegressionOverviewStatusCoversAllProjects(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.post("Engineering", "@Pip can you investigate Beacon's request flow?", []string{"pip"}, nil)
	e.waitJob("Document Beacon", protocol.JobRunning, protocol.JobWaiting, protocol.JobReviewReady, protocol.JobCompleted)
	var rooms []protocol.Room
	e.c.must("GET", "/v1/rooms", nil, &rooms)
	overview := ""
	for _, r := range rooms {
		if r.Kind == protocol.RoomKindOverview {
			overview = r.ID
		}
	}
	e.c.must("POST", "/v1/rooms/"+overview+"/messages", protocol.PostMessageRequest{Body: "Where are we with everything?", ClientKey: "status-1"}, nil)
	var answer string
	e.waitFor("the ledger answer", 10*time.Second, func() bool {
		var page protocol.MessagePage
		e.c.must("GET", "/v1/rooms/"+overview+"/messages?limit=50", nil, &page)
		for _, m := range page.Messages {
			if m.Kind == protocol.MessageStatus {
				answer = m.Body
				return true
			}
		}
		return false
	})
	if !strings.Contains(answer, "**Beacon**") {
		t.Errorf("active project missing:\n%s", answer)
	}
	if !strings.Contains(answer, "Quiet: Atlas") {
		t.Errorf("a quiet project should be named:\n%s", answer)
	}
}

// The diagnostic bundle is useful without carrying personal or secret data.
func TestRegressionDiagnosticBundle(t *testing.T) {
	e := newEnv(t, envOptions{})
	var raw json.RawMessage
	e.c.must("GET", "/v1/diagnostics/bundle", nil, &raw)
	var b protocol.DiagnosticBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion == 0 || b.Counts["engineers"] == 0 || len(b.Machines) == 0 || len(b.Machines[0].Providers) == 0 || len(b.Health) == 0 {
		t.Fatalf("bundle is missing basics: %s", raw)
	}
	for _, leak := range []string{`"account"`, `"hostname"`, `"fingerprint"`, `"body"`} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("bundle carries %s: %s", leak, raw)
		}
	}
}

// A23: a project that needs a tool no machine has waits with that reason and
// never launches elsewhere; bad requirement names are refused.
func TestRegressionProjectToolchainRequirement(t *testing.T) {
	e := newEnv(t, envOptions{})
	p := e.project("Atlas")
	pol := p.Policy
	pol.Requires = []string{"rm -rf"}
	if err := e.c.do("PATCH", "/v1/projects/"+p.ID, protocol.UpdateProjectRequest{Version: p.Version, Policy: &pol}, nil); !isStatus(err, 400) {
		t.Fatalf("an invalid requirement should be refused: %v", err)
	}
	pol.Requires = []string{"COBOL", "cobol"}
	var updated protocol.Project
	e.c.must("PATCH", "/v1/projects/"+p.ID, protocol.UpdateProjectRequest{Version: p.Version, Policy: &pol}, &updated)
	if len(updated.Policy.Requires) != 1 || updated.Policy.Requires[0] != "cobol" {
		t.Fatalf("requirements should be normalized: %v", updated.Policy.Requires)
	}
	e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	var j protocol.Job
	e.waitFor("the work explains the missing tool", 30*time.Second, func() bool {
		var ok bool
		j, ok = e.job("Fix Atlas session expiry")
		e.hub.Tick(e.ctx)
		return ok && strings.Contains(j.StateDetail, "needs cobol")
	})
	if len(e.jobDetail(j.ID).Runs) != 1 || e.jobDetail(j.ID).Runs[0].NodeID != "" {
		t.Fatalf("the run should not have been placed on a machine: %+v", e.jobDetail(j.ID).Runs)
	}
}

// A request made in the thread of finished work starts follow-up work that
// links to the original, and the engineer sees what it follows (spec §8).
func TestRegressionFollowUpLinksOriginal(t *testing.T) {
	var sawFollow atomic.Bool
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "map the queue"):
			return script(toolStep("work_create", map[string]any{"title": "Map the queue", "objective": "Map the queue", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Map the queue":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "The queue lives in worker/queue.go."}, ""))
		case replyTo(m, "Engineering", "also map retries"):
			return script(toolStep("work_create", map[string]any{"title": "Map the retries", "objective": "Map retries too", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Map the retries":
			if m.Job.FollowsUp != nil && m.Job.FollowsUp.Title == "Map the queue" && strings.Contains(m.Job.FollowsUp.Summary, "worker/queue.go") {
				sawFollow.Store(true)
			}
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "Retries are in worker/retry.go."}, ""))
		}
		return nil
	}})
	first := e.post("Engineering", "@Pip can you map the queue?", []string{"pip"}, nil)
	orig := e.waitJob("Map the queue", protocol.JobCompleted)
	e.post("Engineering", "@Pip also map retries please", []string{"pip"}, func(r *protocol.PostMessageRequest) { r.ThreadID = first.Message.ID })
	next := e.waitJob("Map the retries", protocol.JobCompleted)
	if next.FollowsID != orig.ID {
		t.Fatalf("follow-up should link to %s, got %q", orig.ID, next.FollowsID)
	}
	if d := e.jobDetail(orig.ID); len(d.FollowUps) != 1 || d.FollowUps[0].ID != next.ID {
		t.Fatalf("the original should list its follow-up: %+v", d.FollowUps)
	}
	if !sawFollow.Load() {
		t.Fatalf("the engineer's context should say what the work follows up")
	}
}

// §8D: "interrupt and restart" stops the current attempt without closing the
// work and starts the next one at once, told why.
func TestRegressionInterruptAndRestart(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Reverse engineering", "investigate"):
			return script(toolStep("work_create", map[string]any{"title": "Investigate retries", "objective": "Investigate retries", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Investigate retries" && m.Purpose == "restart":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "Restarted and done."}, ""))
		case m.Job.Title == "Investigate retries":
			return script(fake.Step{Status: "Working"}, fake.Step{Fault: "hang"})
		}
		return nil
	}})
	e.post("Reverse engineering", "@Pip investigate retries please", []string{"pip"}, nil)
	j := e.waitJob("Investigate retries", protocol.JobRunning)
	e.waitFor("attempt running", 10*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return len(d.Runs) > 0 && d.Runs[len(d.Runs)-1].State == protocol.RunRunning
	})
	e.c.must("POST", "/v1/jobs/"+j.ID+"/restart", struct{}{}, nil)
	j = e.waitJob("Investigate retries", protocol.JobCompleted)
	states := map[protocol.RunState]int{}
	e.waitFor("both attempts settled", 10*time.Second, func() bool {
		states = map[protocol.RunState]int{}
		for _, r := range e.jobDetail(j.ID).Runs {
			states[r.State]++
		}
		return states[protocol.RunCancelled] == 1 && states[protocol.RunSucceeded] == 1 && len(e.jobDetail(j.ID).Runs) == 2
	})
	// Nothing is running any more: a second restart is refused.
	if err := e.c.do("POST", "/v1/jobs/"+j.ID+"/restart", struct{}{}, nil); !isStatus(err, 409) {
		t.Fatalf("restarting finished work should conflict: %v", err)
	}
}

// A provider crash that changed nothing outside the workspace is retried
// automatically after a backoff, a bounded number of times.
func TestRegressionCrashRetriesAutomatically(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Reverse engineering", "investigate"):
			return script(toolStep("work_create", map[string]any{"title": "Investigate retries", "objective": "Investigate retries", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Investigate retries" && m.Purpose == "retry":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "Done on the second try."}, ""))
		case m.Job.Title == "Investigate retries":
			return script(fake.Step{Status: "Working"}, fake.Step{Fault: "crash"})
		}
		return nil
	}})
	e.post("Reverse engineering", "@Pip investigate retries please", []string{"pip"}, nil)
	var j protocol.Job
	e.waitFor("an automatic retry is scheduled", 20*time.Second, func() bool {
		j, _ = e.job("Investigate retries")
		return j.State == protocol.JobWaiting && j.WaitingReason == protocol.WaitRecovery && strings.Contains(j.StateDetail, "tries again")
	})
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE jobs SET retry_at = ? WHERE id = ?`, store.TS(time.Now().Add(-time.Second)), j.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.hub.Tick(e.ctx)
	j = e.waitJob("Investigate retries", protocol.JobCompleted)
	if n := len(e.jobDetail(j.ID).Runs); n != 2 {
		t.Fatalf("expected one automatic retry, got %d attempts", n)
	}
}

// Output a machine sends under an old lease epoch never changes the work;
// it is kept, once, as quarantined diagnostic evidence.
func TestRegressionStaleOutputIsQuarantined(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	n := e.fakeNode("old-box")
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

// A24: the owner deletes a workspace from Machines by explicit selection.
// Uncommitted work needs an explicit confirmation of the loss; open work's
// workspace can't be deleted; the machine itself removes the files.
func TestRegressionRemoveWorkspaceFromMachines(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Reverse engineering", "map the gateway"):
			return script(toolStep("work_create", map[string]any{"title": "Map the gateway", "objective": "Map it", "kind": "investigation", "project": "Beacon", "repo": "beacon-gateway"}, ""))
		case m.Job.Title == "Map the gateway":
			return script(fake.Step{Write: &fake.WriteFile{Path: "notes.txt", Content: "draft notes"}},
				toolStep("work_update", map[string]any{"state": "completed", "summary": "Mapped."}, ""))
		case replyTo(m, "Reverse engineering", "keep working"):
			return script(toolStep("work_create", map[string]any{"title": "Long map", "objective": "Keep going", "kind": "investigation", "project": "Beacon", "repo": "beacon-gateway"}, ""))
		case m.Job.Title == "Long map":
			return script(fake.Step{Status: "Working"}, fake.Step{Fault: "hang"})
		}
		return nil
	}})
	e.post("Reverse engineering", "@Pip can you map the gateway?", []string{"pip"}, nil)
	done := e.waitJob("Map the gateway", protocol.JobCompleted)
	e.post("Reverse engineering", "@Pip keep working on a long map", []string{"pip"}, nil)
	long := e.waitJob("Long map", protocol.JobRunning)
	node := e.nodeByName("Test mini")
	var mine, busy protocol.NodeWorkspace
	e.waitFor("both workspaces reported", 20*time.Second, func() bool {
		e.c.must("POST", "/v1/nodes/"+node.ID+"/probe", struct{}{}, nil)
		time.Sleep(200 * time.Millisecond)
		mine, busy = protocol.NodeWorkspace{}, protocol.NodeWorkspace{}
		for _, w := range e.nodeByName("Test mini").Workspaces {
			switch w.JobID {
			case done.ID:
				mine = w
			case long.ID:
				busy = w
			}
		}
		return mine.Name != "" && busy.Name != "" && busy.InUse
	})
	if mine.Changes < 1 || mine.Published || mine.Blocked != "" {
		t.Fatalf("finished work with a draft should be deletable but unpublished: %+v", mine)
	}
	base := "/v1/nodes/" + node.ID + "/workspaces/"
	if err := e.c.do("POST", base+busy.Name+"/remove", protocol.CleanupWorkspaceRequest{Confirm: busy.Name, Force: true}, nil); !isStatus(err, 409) {
		t.Fatalf("open work's workspace must not be deleted: %v", err)
	}
	if err := e.c.do("POST", base+mine.Name+"/remove", protocol.CleanupWorkspaceRequest{Confirm: "nope"}, nil); !isStatus(err, 400) {
		t.Fatalf("a wrong confirmation must be refused: %v", err)
	}
	err := e.c.do("POST", base+mine.Name+"/remove", protocol.CleanupWorkspaceRequest{Confirm: mine.Name}, nil)
	if !isStatus(err, 409) || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("losing uncommitted work needs explicit confirmation: %v", err)
	}
	dir := filepath.Join(e.dir, "runner", "work", mine.Name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("workspace should exist before removal: %v", err)
	}
	var after protocol.Node
	e.c.must("POST", base+mine.Name+"/remove", protocol.CleanupWorkspaceRequest{Confirm: mine.Name, Force: true}, &after)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the machine should have deleted %s: %v", dir, err)
	}
	for _, w := range after.Workspaces {
		if w.Name == mine.Name {
			t.Fatalf("the deleted workspace is still listed")
		}
	}
}

// A repository with no reachable remote is imported from a git bundle; the
// machine builds its copy from the bundle, and a newer bundle refreshes it.
func TestRegressionImportRepoFromBundle(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Reverse engineering", "read the notes"):
			return script(toolStep("work_create", map[string]any{"title": "Read the notes " + strings.Fields(m.Request.Body)[len(strings.Fields(m.Request.Body))-1],
				"objective": "Read them", "kind": "investigation", "project": "Beacon", "repo": "laptop-notes"}, ""))
		case strings.HasPrefix(m.Job.Title, "Read the notes"):
			return script(fake.Step{Shell: "cat NOTES.md", Save: "n"}, toolStep("work_update", map[string]any{"state": "completed", "summary": "Notes: {{n.out}}"}, ""))
		}
		return nil
	}})
	// A folder on the laptop: a repository with history but no remote.
	src := filepath.Join(e.dir, "laptop-notes")
	gitOut(t, "", "init", "-q", "-b", "trunk", src)
	commit := func(text string) {
		if err := os.WriteFile(filepath.Join(src, "NOTES.md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, src, "add", "-A")
		gitOut(t, src, "-c", "user.name=Owner", "-c", "user.email=owner@example.com", "commit", "-q", "-m", text)
	}
	bundle := func() []byte {
		out := filepath.Join(e.dir, "notes.bundle")
		gitOut(t, src, "bundle", "create", "-q", out, "--all")
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	commit("first draft")
	beacon := e.project("Beacon")
	base := "/v1/projects/" + beacon.ID + "/repos/import"
	if err := e.c.do("POST", base+"?name=junk", []byte("not a bundle at all"), nil); !isStatus(err, 400) {
		t.Fatalf("a non-bundle must be refused: %v", err)
	}
	commit("second draft")
	gitOut(t, src, "bundle", "create", "-q", filepath.Join(e.dir, "partial.bundle"), "HEAD~1..HEAD")
	partial, _ := os.ReadFile(filepath.Join(e.dir, "partial.bundle"))
	if err := e.c.do("POST", base+"?name=partial", partial, nil); !isStatus(err, 400) || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("a bundle missing history must be refused with how to fix it: %v", err)
	}
	var p protocol.Project
	e.c.must("POST", base+"?name=laptop-notes", bundle(), &p)
	var repo protocol.Repo
	for _, r := range p.Repos {
		if r.Name == "laptop-notes" {
			repo = r
		}
	}
	if repo.SourceBundleID == "" || repo.RemoteURL != "" || repo.DefaultBranch != "trunk" || repo.ImportedAt == nil {
		t.Fatalf("imported repository not recorded as expected: %+v", repo)
	}
	e.post("Reverse engineering", "@Pip please read the notes v1", []string{"pip"}, nil)
	j := e.waitJob("Read the notes v1", protocol.JobCompleted)
	if !strings.Contains(e.jobDetail(j.ID).Job.Summary, "second draft") {
		t.Fatalf("the machine should read the imported code: %q", e.jobDetail(j.ID).Job.Summary)
	}
	// A newer bundle replaces the old one; the machine refreshes its copy.
	commit("third draft")
	e.c.must("POST", base+"?repo="+repo.ID, bundle(), &p)
	e.post("Reverse engineering", "@Pip please read the notes v2", []string{"pip"}, nil)
	j = e.waitJob("Read the notes v2", protocol.JobCompleted)
	if !strings.Contains(e.jobDetail(j.ID).Job.Summary, "third draft") {
		t.Fatalf("the machine should use the newer bundle: %q", e.jobDetail(j.ID).Job.Summary)
	}
	// A repository with a remote can't be replaced by a bundle.
	var gw protocol.Repo
	for _, r := range p.Repos {
		if r.RemoteURL != "" {
			gw = r
		}
	}
	if err := e.c.do("POST", base+"?repo="+gw.ID, bundle(), nil); !isStatus(err, 409) {
		t.Fatalf("a remote-backed repository must not be replaced by a bundle: %v", err)
	}
}

// A change sent twice with the same Idempotency-Key acts once and replays its
// first result; the key can't be reused for a different request.
func TestRegressionIdempotencyKeys(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
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

// Engineer notes (spec §8 layer 5): kept automatically from the engineer's
// own finished work, used only where their sources are visible, proposals
// otherwise (no queue), correctable by supersession, and left out of
// context once due for review until renewed.
func TestRegressionEngineerNotes(t *testing.T) {
	var seen sync.Map // room name → notes Pip saw
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.Job.Kind == "reply" {
			var bodies []string
			for _, n := range m.Notes {
				bodies = append(bodies, n.Body)
			}
			seen.Store(m.RoomName+"|"+m.Request.Body, strings.Join(bodies, "\n"))
		}
		switch {
		case replyTo(m, "Reverse engineering", "map the queue"):
			return script(toolStep("work_create", map[string]any{"title": "Map the queue", "objective": "Map it", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Map the queue":
			return script(toolStep("note_record", map[string]any{"body": "Beacon's queue lives in worker/queue.go", "sources": []string{}}, "n"),
				toolStep("work_update", map[string]any{"state": "completed", "summary": "Mapped: {{n.status}}"}, ""))
		case replyTo(m, "Hush", "secret"):
			return script(toolStep("note_record", map[string]any{"body": "The incident was caused by the retry storm", "sources": []string{m.Request.ID}}, ""),
				fake.Step{Final: "Noted."})
		}
		return script(fake.Step{Final: "OK"})
	}})
	pip := e.engineerID("pip")
	beacon := e.project("Beacon")
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Hush", Private: true, EngineerIDs: []string{pip}, ProjectIDs: []string{beacon.ID}}, nil)

	e.post("Reverse engineering", "@Pip please map the queue", []string{"pip"}, nil)
	j := e.waitJob("Map the queue", protocol.JobCompleted)
	if !strings.Contains(e.jobDetail(j.ID).Job.Summary, "proposed") {
		t.Fatalf("a note from open work should start as a proposal: %q", e.jobDetail(j.ID).Job.Summary)
	}
	notes := func() []protocol.EngineerNote {
		var ns []protocol.EngineerNote
		e.c.must("GET", "/v1/engineers/"+pip+"/notes", nil, &ns)
		return ns
	}
	e.waitFor("the note is kept once the work is finished", 10*time.Second, func() bool {
		ns := notes()
		return len(ns) == 1 && ns[0].Status == "accepted" && ns[0].AcceptedBy != nil && ns[0].AcceptedBy.Kind == protocol.ActorSystem
	})
	// A private room's note is a proposal (message source) and stays there.
	e.post("Hush", "@Pip this is secret: the retry storm did it", []string{"pip"}, nil)
	var secret protocol.EngineerNote
	e.waitFor("the private note is proposed", 10*time.Second, func() bool {
		for _, n := range notes() {
			if strings.Contains(n.Body, "retry storm") {
				secret = n
			}
		}
		return secret.ID != ""
	})
	if secret.Status != "proposed" || len(secret.VisibleRoomIDs) != 1 || secret.VisibleRoomIDs[0] != e.roomID("Hush") {
		t.Fatalf("a private note should be a proposal visible only in its room: %+v", secret)
	}
	e.c.must("POST", "/v1/notes/"+secret.ID, protocol.NoteActionRequest{Action: "accept", Version: secret.Version}, &secret)

	ask := func(room, body string) string {
		e.post(room, body, []string{"pip"}, nil)
		var got string
		e.waitFor("Pip's context in "+room, 10*time.Second, func() bool {
			v, ok := seen.Load(room + "|" + body)
			if ok {
				got = v.(string)
			}
			return ok
		})
		return got
	}
	got := ask("Reverse engineering", "@Pip where is the queue?")
	if !strings.Contains(got, "worker/queue.go") || strings.Contains(got, "retry storm") {
		t.Fatalf("Reverse engineering should see the Beacon note and not the private one:\n%s", got)
	}
	if got := ask("Hush", "@Pip anything to recall here?"); !strings.Contains(got, "retry storm") || !strings.Contains(got, "worker/queue.go") {
		t.Fatalf("Hush (linked to Beacon) should see both notes:\n%s", got)
	}
	// The owner corrects the queue note; the old one is superseded.
	var queue protocol.EngineerNote
	for _, n := range notes() {
		if strings.Contains(n.Body, "queue.go") {
			queue = n
		}
	}
	var fixed protocol.EngineerNote
	e.c.must("POST", "/v1/engineers/"+pip+"/notes", protocol.NoteRequest{Body: "Beacon's queue moved to worker/jobs/queue.go", SupersedesID: queue.ID}, &fixed)
	if fixed.Status != "accepted" || fixed.Scope != queue.Scope {
		t.Fatalf("a correction is kept at once with the original's scope: %+v", fixed)
	}
	if got := ask("Reverse engineering", "@Pip where is it now?"); !strings.Contains(got, "worker/jobs/queue.go") || strings.Contains(got, "lives in worker/queue.go") {
		t.Fatalf("the correction should replace the old note in context:\n%s", got)
	}
	// Due for review: left out of context until renewed.
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE engineer_notes SET review_after = ? WHERE id = ?`, store.TS(time.Now().Add(-time.Hour)), fixed.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := ask("Reverse engineering", "@Pip still there?"); strings.Contains(got, "queue") {
		t.Fatalf("a note due for review should be left out:\n%s", got)
	}
	for _, n := range notes() {
		if n.ID == fixed.ID {
			fixed = n
		}
	}
	e.c.must("POST", "/v1/notes/"+fixed.ID, protocol.NoteActionRequest{Action: "renew", Version: fixed.Version}, &fixed)
	if got := ask("Reverse engineering", "@Pip and now?"); !strings.Contains(got, "worker/jobs/queue.go") {
		t.Fatalf("a renewed note is back in context:\n%s", got)
	}
}
