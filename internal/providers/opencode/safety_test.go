package opencode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

func TestNativeShellNeverReachesApproval(t *testing.T) {
	a, spec, _ := fixture(t, "shell", protocol.ModeEdit)
	s, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for event := range s.Events() {
		if event.Kind == providers.EventApprovalRequest {
			t.Fatal("native shell was offered for approval")
		}
	}
	if result := s.Wait(); result.Outcome != protocol.OutcomeSucceeded || !result.ExitConfirmed {
		t.Fatal(result)
	}
}

func TestBlockedProtocolWriteCannotPreventShutdown(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		name := "cancel"
		if timeout {
			name = "timeout"
		}
		t.Run(name, func(t *testing.T) {
			a, spec, _ := fixture(t, "no-read", protocol.ModeEdit)
			spec.Prompt = strings.Repeat("x", 8*1024*1024)
			if timeout {
				spec.Timeout = 250 * time.Millisecond
			}
			s, err := a.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if !timeout {
				// The peer stops reading after model selection. Allow the large
				// prompt to fill the pipe before requesting protocol cancellation.
				time.Sleep(50 * time.Millisecond)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := s.Cancel(ctx); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan providers.Result, 1)
			go func() { done <- s.Wait() }()
			select {
			case result := <-done:
				want := protocol.OutcomeCancelled
				if timeout {
					want = protocol.OutcomeFailed
				}
				if result.Outcome != want || !result.ExitConfirmed {
					t.Fatalf("blocked write result: %+v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown blocked behind an ACP write")
			}
		})
	}
}
