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
	for _, args := range [][]string{
		{"init", "--quiet"},
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
	check := func(command string) <-chan checkResult {
		out := make(chan checkResult, 1)
		raw, _ := json.Marshal(bridge.WorkRunCheckArgs{Command: command})
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

	// An allowed check runs and is recorded with the same command.
	done = check("touch checked")
	resolve(approvalFor(), "allow", "")
	call := nextOfType(t, frames, protocol.EvToolCall)
	var tc protocol.ToolCall
	_ = json.Unmarshal(call.Payload, &tc)
	var rec protocol.CheckRecord
	_ = json.Unmarshal(tc.Args, &rec)
	if tc.Tool != bridge.RecordCheck || rec.Command != "touch checked" || rec.ExitCode != 0 || !ran("checked") {
		t.Fatalf("an allowed check should run and be recorded: %s %+v", tc.Tool, rec)
	}
	b, _ := json.Marshal(protocol.ToolResult{CallID: tc.CallID, OK: true, Result: json.RawMessage(`{}`)})
	if err := r.handle(ctx, c, protocol.Frame{Type: protocol.CmdToolResult, ID: "result:" + tc.CallID, Payload: b}); err != nil {
		t.Fatal(err)
	}
	if got := result(done); got.err != nil {
		t.Fatalf("the allowed check failed: %+v", got.err)
	}

	// A run that has stopped admitting work runs nothing, without asking.
	ar.admit.Store(false)
	if got := result(check("touch late")); got.err == nil || got.err.Code != "forbidden" || ran("late") {
		t.Fatalf("a stopping run must not run checks: %+v", got)
	}
}
