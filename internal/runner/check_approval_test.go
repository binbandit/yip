package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/protocol"
)

type checkResult struct {
	res any
	err *protocol.APIError
}

// work_run_check asks the hub about the exact command and revision before
// running anything, and runs nothing unless the answer is allow. The hub's
// policy is what makes a push or network command need the owner
// (internal/hub/policy_test.go, TestExactActionApprovals); this is the
// runner's half of that gate.
func TestRunCheckAsksBeforeRunning(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "tracked.txt"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "base"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	head, err := git(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	artifacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not stored in this test", http.StatusServiceUnavailable)
	}))
	t.Cleanup(artifacts.Close)

	r := &Runner{paths: Paths{t.TempDir()}, log: slog.New(slog.DiscardHandler), httpc: artifacts.Client(),
		id: Identity{Name: "test", HubURL: artifacts.URL}, ws: newWorkspaces(Paths{t.TempDir()}),
		runs: map[string]*activeRun{}, tokens: map[string]string{}, tools: map[string]*pendingCall{},
		approvals: map[string]*pendingApproval{}}
	j, err := OpenJournal(filepath.Join(r.paths.Dir, "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	r.journal = j
	c, frames := hubPeer(t)
	r.conn = c
	ctx := context.Background()

	ar := &activeRun{m: protocol.ExecutionManifest{RunID: "run-1", Provider: "codex", Mode: protocol.ModeEdit}, epoch: 1,
		ws: &Workspace{Dir: repo}, done: make(chan struct{})}
	ar.admit.Store(true)
	timeoutSeconds := 0
	check := func(command string) <-chan checkResult {
		out := make(chan checkResult, 1)
		raw, _ := json.Marshal(bridge.WorkRunCheckArgs{Command: command, TimeoutSeconds: timeoutSeconds})
		go func() {
			res, e := r.toolRunCheck(ctx, ar, raw)
			out <- checkResult{res, e}
		}()
		return out
	}
	approvalFor := func() protocol.ApprovalRequest {
		t.Helper()
		var req protocol.ApprovalRequest
		f := nextOfType(t, frames, protocol.EvApprovalRequest)
		if err := json.Unmarshal(f.Payload, &req); err != nil {
			t.Fatal(err)
		}
		return req
	}
	resolve := func(req protocol.ApprovalRequest, decision, reason string) {
		t.Helper()
		b, _ := json.Marshal(protocol.ResolveApproval{RequestID: req.RequestID, Decision: decision, Reason: reason})
		if err := r.handle(ctx, c, protocol.Frame{Type: protocol.CmdResolveApproval, ID: "resolve:" + req.RequestID, RunID: "run-1", LeaseEpoch: 1, Payload: b}); err != nil {
			t.Fatal(err)
		}
	}
	result := func(done <-chan checkResult) checkResult {
		t.Helper()
		select {
		case got := <-done:
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("the check did not return")
			return checkResult{}
		}
	}
	ran := func(name string) bool {
		_, err := os.Stat(filepath.Join(repo, name))
		return err == nil
	}

	push := "git push origin HEAD; touch pushed"
	done := check(push)
	req := approvalFor()
	if req.Action.Kind != "exec" || req.Action.Command != push || req.Action.Target != head {
		t.Fatalf("the hub must be asked about the exact command at the exact revision: %+v", req.Action)
	}
	if ran("pushed") {
		t.Fatal("the check ran before the hub answered")
	}
	resolve(req, "deny", "a push needs the owner's approval")
	got := result(done)
	if got.err == nil || got.err.Code != "forbidden" {
		t.Fatalf("a denied check must be refused: %+v", got)
	}
	if ran("pushed") {
		t.Fatal("a denied check ran anyway")
	}

	// An allowed check keeps its command and exit status, but only a stable
	// clean workspace may certify a published revision.
	afterApproval := func() {}
	allowed := func(command string, wantExit ...int) protocol.CheckRecord {
		t.Helper()
		expectedExit := 0
		if len(wantExit) > 0 {
			expectedExit = wantExit[0]
		}
		done := check(command)
		resolve(approvalFor(), "allow", "")
		afterApproval()
		call := nextOfType(t, frames, protocol.EvToolCall)
		var tc protocol.ToolCall
		_ = json.Unmarshal(call.Payload, &tc)
		var rec protocol.CheckRecord
		_ = json.Unmarshal(tc.Args, &rec)
		if tc.Tool != bridge.RecordCheck || rec.Command != command || rec.ExitCode != expectedExit {
			t.Fatalf("an allowed check should run and be recorded: %s %+v", tc.Tool, rec)
		}
		b, _ := json.Marshal(protocol.ToolResult{CallID: tc.CallID, OK: true, Result: json.RawMessage(`{}`)})
		if err := r.handle(ctx, c, protocol.Frame{Type: protocol.CmdToolResult, ID: "result:" + tc.CallID, Payload: b}); err != nil {
			t.Fatal(err)
		}
		got := result(done)
		if got.err != nil {
			t.Fatalf("the allowed check failed: %+v", got.err)
		}
		res := got.res.(map[string]any)
		if res["revision"] != rec.Revision || res["passed"] != (expectedExit == 0) {
			t.Fatalf("tool result and recorded evidence disagree: %+v, %+v", res, rec)
		}
		if rec.Revision != head && res["note"] == nil {
			t.Fatal("unverified evidence needs an explanation")
		}
		return rec
	}
	if rec := allowed("true"); rec.Revision != head {
		t.Fatalf("clean check lost its revision: %+v", rec)
	}
	if rec := allowed("touch checked"); rec.Revision == head || !ran("checked") {
		t.Fatalf("a check that changes the workspace must not certify the original clean revision: %+v", rec)
	}
	if rec := allowed("true"); rec.Revision != head+"+uncommitted" {
		t.Fatalf("dirty check should remain uncommitted: %+v", rec)
	}
	if rec := allowed("rm checked"); rec.Revision == head {
		t.Fatalf("removing initial uncommitted changes must not certify the clean revision: %+v", rec)
	}
	if rec := allowed("printf changed > tracked.txt"); rec.Revision == head {
		t.Fatalf("editing tracked content must not certify the original revision: %+v", rec)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	if rec := allowed("git -c user.name=t -c user.email=t@example.com commit --quiet --allow-empty -m check"); rec.Revision == head {
		t.Fatalf("a moved HEAD must not certify the original revision: %+v", rec)
	}
	head, err = git(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if rec := allowed("mv .git .git-away"); rec.Revision == head {
		t.Fatalf("an unreadable final revision must not certify the original revision: %+v", rec)
	}
	if err := os.Rename(filepath.Join(repo, ".git-away"), filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}

	// Finishing the shell must also stop its owned background children.
	allowed("(sleep 0.3; touch late-child) >/dev/null 2>&1 &")
	time.Sleep(400 * time.Millisecond)
	if ran("late-child") {
		t.Error("a check left a background child modifying the workspace after it returned")
	}

	// A process that handles cancellation with exit 0 still did not finish
	// the requested check, and cannot certify its starting revision.
	timeoutSeconds = 1
	if rec := allowed("trap 'exit 0' TERM; while :; do sleep 1; done", 124); rec.Revision == head {
		t.Fatalf("a timed-out check must not certify the original revision: %+v", rec)
	}
	timeoutSeconds = 0

	afterApproval = func() {
		ready := filepath.Join(r.scratchDir(ar.m.RunID), "home", "ready")
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("cancellation check never became ready")
			}
			time.Sleep(5 * time.Millisecond)
		}
		ar.admit.Store(false)
	}
	if rec := allowed(`trap 'exit 0' TERM; touch "$HOME/ready"; while :; do sleep 1; done`); rec.Revision == head {
		t.Fatalf("a stopped check that exits zero must not certify the original revision: %+v", rec)
	}
	ar.admit.Store(true)
	afterApproval = func() {}

	// Investigation workspaces have no repository; checks still run there,
	// with an empty revision that cannot satisfy a code completion gate.
	savedWorkspace, savedHead := ar.ws, head
	ar.ws, head = &Workspace{Dir: t.TempDir(), Scratch: true}, ""
	if rec := allowed("true"); rec.Revision != "" {
		t.Fatalf("scratch check should have no revision: %+v", rec)
	}
	ar.ws, head = savedWorkspace, savedHead

	// Permission for an earlier clean revision cannot run after that tree changes.
	done = check("touch stale-approval")
	req = approvalFor()
	if err := os.WriteFile(filepath.Join(repo, "intervening-edit"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolve(req, "allow", "")
	if got := result(done); got.err == nil || got.err.Code != "conflict" || ran("stale-approval") {
		t.Fatalf("stale permission must run nothing: %+v", got)
	}

	// An unreadable starting revision must fail before requesting permission.
	ar.ws.Dir = filepath.Join(repo, "missing")
	if got := result(check("touch unknown-head")); got.err == nil || got.err.Code != "internal" {
		t.Fatalf("unknown starting revision must be rejected: %+v", got)
	}
	ar.ws.Dir = repo

	// A run that has stopped admitting work runs nothing, without asking.
	ar.admit.Store(false)
	if got := result(check("touch late")); got.err == nil || got.err.Code != "forbidden" || ran("late") {
		t.Fatalf("a stopping run must not run checks: %+v", got)
	}
}
