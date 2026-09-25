package cursor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Real smoke tests against an installed, signed-in Cursor CLI. They spend
// real allowance, so they only run with YIP_REAL_PROVIDER_TESTS=1 and skip
// when `agent`/`cursor-agent` is not installed or not signed in.
//
//	YIP_REAL_PROVIDER_TESTS=1 go test -run TestReal -v ./internal/providers/cursor/
//
// Optional: YIP_CURSOR_MODEL selects a model for the runs.
func realAdapter(t *testing.T) (*Adapter, protocol.ProviderInstallation) {
	t.Helper()
	if os.Getenv("YIP_REAL_PROVIDER_TESTS") != "1" {
		t.Skip("set YIP_REAL_PROVIDER_TESTS=1 to run against a real Cursor CLI")
	}
	env := providers.BaseEnv([]string{"CURSOR_API_KEY", "CURSOR_CONFIG_DIR"})
	a := newAdapter(WithProbeEnv(env))
	inst := a.Probe(context.Background())
	t.Logf("probe: version=%q path=%q auth=%s (%s) account=%q caps=%+v models=%d", inst.Version, inst.Path, inst.AuthState, inst.AuthDetail, inst.Account, inst.Capabilities, len(inst.Models))
	for _, l := range inst.Limitations {
		t.Logf("limitation: %s", l)
	}
	switch inst.AuthState {
	case protocol.AuthNotInstalled:
		t.Skip("Cursor CLI not installed")
	case protocol.AuthNeedsSignIn:
		t.Skip("Cursor CLI not signed in; run `agent login` first")
	}
	return a, inst
}

func realSpec(t *testing.T, mode, prompt string) providers.StartSpec {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# smoke\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return providers.StartSpec{
		RunID:        "real-smoke",
		Workdir:      dir,
		Mode:         mode,
		Model:        os.Getenv("YIP_CURSOR_MODEL"),
		Instructions: "You are a test engineer for the yip adapter smoke test. Be brief.",
		Prompt:       prompt,
		Env:          providers.BaseEnv([]string{"CURSOR_API_KEY", "CURSOR_CONFIG_DIR"}),
		Timeout:      5 * time.Minute,
	}
}

func runReal(t *testing.T, s providers.Session, onEvent func(providers.Event)) (providers.Result, []providers.Event) {
	t.Helper()
	var evs []providers.Event
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range s.Events() {
			t.Logf("event %s tool=%q text=%q", e.Kind, e.Tool, oneLine(e.Text, 200))
			evs = append(evs, e)
			if onEvent != nil {
				onEvent(e)
			}
		}
	}()
	res := s.Wait()
	<-done
	t.Logf("result: outcome=%s exitConfirmed=%v session=%s error=%q final=%q", res.Outcome, res.ExitConfirmed, res.VendorSessionID, res.Error, oneLine(res.FinalText, 300))
	return res, evs
}

func TestRealConversationReadOnly(t *testing.T) {
	a, _ := realAdapter(t)
	spec := realSpec(t, protocol.ModeConversation, "Reply with exactly the text yip-cursor-ok and nothing else. Do not use any tools.")
	s, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, _ := runReal(t, s, func(e providers.Event) {
		if e.Kind == providers.EventApprovalRequest {
			_ = s.ResolveApproval(context.Background(), e.Approval.ID, providers.ApprovalDecision{Allow: false, Reason: "smoke test"})
		}
		if e.Kind == providers.EventQuestion {
			_ = s.AnswerQuestion(context.Background(), e.Question.ID, providers.QuestionAnswer{Selected: -1, Declined: true})
		}
	})
	if res.Outcome != protocol.OutcomeSucceeded || !strings.Contains(res.FinalText, "yip-cursor-ok") || !res.ExitConfirmed {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestRealEditApprovalDenialAndResume(t *testing.T) {
	a, inst := realAdapter(t)
	spec := realSpec(t, protocol.ModeEdit, "Create a file named hello.txt containing the single line hello from yip. Then run the shell command `rm README.md`. Report what happened.")
	s, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var sawApproval, deniedRm bool
	res, _ := runReal(t, s, func(e providers.Event) {
		switch e.Kind {
		case providers.EventApprovalRequest:
			sawApproval = true
			allow := !strings.Contains(e.Approval.Action.Command, "rm ")
			if !allow {
				deniedRm = true
			}
			t.Logf("approval %s: %+v allow=%v", e.Approval.ID, e.Approval.Action, allow)
			_ = s.ResolveApproval(context.Background(), e.Approval.ID, providers.ApprovalDecision{Allow: allow})
		case providers.EventQuestion:
			_ = s.AnswerQuestion(context.Background(), e.Question.ID, providers.QuestionAnswer{Selected: -1, Declined: true})
		}
	})
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	if _, err := os.Stat(filepath.Join(spec.Workdir, "README.md")); err != nil {
		t.Errorf("README.md was removed although the command was denied")
	}
	t.Logf("sawApproval=%v deniedRm=%v hello.txt exists=%v", sawApproval, deniedRm, fileExists(filepath.Join(spec.Workdir, "hello.txt")))

	if !inst.Capabilities.SessionResume || res.VendorSessionID == "" {
		t.Skip("resume not advertised")
	}
	spec2 := spec
	spec2.ResumeSessionID = res.VendorSessionID
	spec2.Prompt = "What file did you create earlier? Answer with just the file name."
	s2, err := a.Start(context.Background(), spec2)
	if err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	res2, _ := runReal(t, s2, nil)
	if res2.Outcome != protocol.OutcomeSucceeded || res2.VendorSessionID != res.VendorSessionID {
		t.Errorf("resume result %+v", res2)
	}
}

func TestRealCancel(t *testing.T) {
	a, _ := realAdapter(t)
	spec := realSpec(t, protocol.ModeConversation, "Count slowly from 1 to 500, one number per line.")
	s, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancelled := make(chan struct{})
	once := false
	res, _ := runReal(t, s, func(e providers.Event) {
		if e.Kind == providers.EventMessageDelta && !once {
			once = true
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := s.Cancel(ctx); err != nil {
					t.Errorf("Cancel: %v", err)
				}
				close(cancelled)
			}()
		}
	})
	if once {
		<-cancelled
	}
	if res.Outcome != protocol.OutcomeCancelled || !res.ExitConfirmed {
		t.Errorf("cancel result %+v", res)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
