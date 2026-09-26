package domain

import (
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestJobTransitions(t *testing.T) {
	allowed := [][2]protocol.JobState{
		{protocol.JobQueued, protocol.JobRunning},
		{protocol.JobRunning, protocol.JobReviewReady},
		{protocol.JobReviewReady, protocol.JobCompleted},
		{protocol.JobReviewReady, protocol.JobQueued},
		{protocol.JobWaiting, protocol.JobQueued},
		{protocol.JobFailed, protocol.JobQueued},
		{protocol.JobCancelled, protocol.JobQueued}, // explicit resume
	}
	for _, p := range allowed {
		if !CanTransitionJob(p[0], p[1]) {
			t.Errorf("%s → %s should be allowed", p[0], p[1])
		}
	}
	denied := [][2]protocol.JobState{
		{protocol.JobCompleted, protocol.JobQueued},
		{protocol.JobCancelled, protocol.JobRunning},
		{protocol.JobFailed, protocol.JobCompleted},
		{protocol.JobQueued, protocol.JobCompleted},
	}
	for _, p := range denied {
		if CanTransitionJob(p[0], p[1]) {
			t.Errorf("%s → %s should be refused", p[0], p[1])
		}
	}
}

func TestLostRunIsUnknownNotRetryable(t *testing.T) {
	if !CanTransitionRun(protocol.RunRunning, protocol.RunUnknown) {
		t.Fatal("running → unknown must be allowed")
	}
	if CanTransitionRun(protocol.RunUnknown, protocol.RunRunning) || CanTransitionRun(protocol.RunUnknown, protocol.RunCreated) {
		t.Fatal("an unknown attempt must never silently resume or be re-queued")
	}
	if !CanTransitionRun(protocol.RunOffered, protocol.RunCreated) {
		t.Fatal("an unacknowledged offer never started and may return to the queue")
	}
	if RunHoldsLease(protocol.RunUnknown) || !RunHoldsLease(protocol.RunStopping) {
		t.Fatal("lease accounting is wrong")
	}
}

func TestReviewVerdictsAreFinal(t *testing.T) {
	for _, v := range []protocol.ReviewState{protocol.ReviewApproved, protocol.ReviewChangesRequested, protocol.ReviewCommentsOnly, protocol.ReviewUnable} {
		if !ReviewVerdict(v) || ReviewOpen(v) || CanTransitionReview(v, protocol.ReviewReviewing) {
			t.Errorf("%s should be a closed verdict", v)
		}
	}
}

func TestHandle(t *testing.T) {
	for in, want := range map[string]string{"Mira": "mira", "Mira Chen": "mira-chen", "  Pip!  ": "pip", "Ö-ren 2": "ö-ren-2"} {
		if got := Handle(in); got != want {
			t.Errorf("Handle(%q) = %q, want %q", in, got, want)
		}
	}
}
