package hub

import (
	"log/slog"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/domain"
)

func TestHumanReason(t *testing.T) {
	cases := map[string]string{
		`work_update failed: {"code":"incomplete","message":"Not complete yet. Missing: a passing run of go test.","recoverable":true}`: "Not complete yet. Missing: a passing run of go test",
		"workspace: git fetch failed": "workspace: git fetch failed",
		"exit status 1: {broken":      "exit status 1",
		"":                            "it stopped before finishing (the details are in the work)",
		"panic: runtime error\ngoroutine 1 [running]:": "it stopped before finishing (the details are in the work)",
	}
	for in, want := range cases {
		if got := humanReason(in); got != want {
			t.Errorf("humanReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLeasesWaitAfterTheHubItselfWasAsleep(t *testing.T) {
	h := &Hub{lim: domain.DefaultLimits(), log: slog.New(slog.DiscardHandler)}
	if !h.awake() {
		t.Fatal("a hub that has just started judges leases normally")
	}
	h.lastTick = time.Now().Add(-16 * time.Minute) // the lid was closed
	if h.awake() {
		t.Fatal("right after a pause, machines get a lease period to report in")
	}
	h.resumedAt = time.Now().Add(-h.lim.LeaseDuration)
	if !h.awake() {
		t.Fatal("a lease period after resuming, silent machines' runs expire again")
	}
}
