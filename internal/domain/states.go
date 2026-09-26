// Package domain holds yip's state machines, identifiers, and operating
// policy. It is pure: no I/O, no database access.
package domain

import (
	"fmt"

	"github.com/binbandit/yip/protocol"
)

// jobTransitions encodes the job state machine from the architecture:
// queued → running → completed | waiting | review_ready | failed | cancelled;
// waiting → queued; review_ready → completed | queued; failed → queued only by
// retry policy or explicit retry.
var jobTransitions = map[protocol.JobState][]protocol.JobState{
	protocol.JobQueued: {
		protocol.JobRunning, protocol.JobWaiting, protocol.JobCancelled, protocol.JobFailed,
	},
	protocol.JobRunning: {
		protocol.JobCompleted, protocol.JobWaiting, protocol.JobReviewReady,
		protocol.JobFailed, protocol.JobCancelled, protocol.JobQueued,
	},
	protocol.JobWaiting: {
		protocol.JobQueued, protocol.JobCancelled, protocol.JobFailed, protocol.JobReviewReady,
	},
	protocol.JobReviewReady: {
		protocol.JobCompleted, protocol.JobQueued, protocol.JobCancelled, protocol.JobWaiting,
	},
	protocol.JobFailed: {
		protocol.JobQueued, protocol.JobCancelled,
	},
	protocol.JobCompleted: {},
	// Stopped work can be resumed, but only by an explicit owner retry
	// (RetryJob); automatic paths check JobLive and never revive it.
	protocol.JobCancelled: {protocol.JobQueued},
}

// CanTransitionJob reports whether a job may move from one state to another.
func CanTransitionJob(from, to protocol.JobState) bool {
	if from == to {
		return true
	}
	for _, s := range jobTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// CheckJobTransition returns a descriptive error for an invalid transition.
func CheckJobTransition(from, to protocol.JobState) error {
	if !CanTransitionJob(from, to) {
		return fmt.Errorf("job cannot move from %s to %s", from, to)
	}
	return nil
}

// JobTerminal reports whether a job state is final.
func JobTerminal(s protocol.JobState) bool {
	return s == protocol.JobCompleted || s == protocol.JobCancelled
}

// JobLive reports whether a job can still make progress.
func JobLive(s protocol.JobState) bool {
	return !JobTerminal(s) && s != protocol.JobFailed
}

// runTransitions encodes: created → offered → preparing → running →
// awaiting_input | stopping | succeeded | failed | cancelled | unknown. A lost
// running machine produces unknown, never "failed, safe to repeat".
var runTransitions = map[protocol.RunState][]protocol.RunState{
	protocol.RunCreated: {protocol.RunOffered, protocol.RunCancelled, protocol.RunFailed},
	// An offer that was never acknowledged was never started, so it may
	// safely return to created for re-offer under a new lease epoch.
	protocol.RunOffered: {
		protocol.RunPreparing, protocol.RunCreated, protocol.RunCancelled,
		protocol.RunRunning, protocol.RunUnknown, protocol.RunFailed, protocol.RunStopping,
	},
	protocol.RunPreparing: {
		protocol.RunRunning, protocol.RunFailed, protocol.RunCancelled, protocol.RunStopping,
		protocol.RunUnknown, protocol.RunSucceeded,
	},
	protocol.RunRunning: {
		protocol.RunAwaitingInput, protocol.RunStopping, protocol.RunSucceeded,
		protocol.RunFailed, protocol.RunCancelled, protocol.RunUnknown,
	},
	protocol.RunAwaitingInput: {
		protocol.RunRunning, protocol.RunStopping, protocol.RunSucceeded,
		protocol.RunFailed, protocol.RunCancelled, protocol.RunUnknown,
	},
	protocol.RunStopping: {
		protocol.RunCancelled, protocol.RunFailed, protocol.RunSucceeded, protocol.RunUnknown,
	},
	// Unknown is resolved only by reconciliation with the runner's journal.
	protocol.RunUnknown: {
		protocol.RunSucceeded, protocol.RunFailed, protocol.RunCancelled,
	},
	protocol.RunSucceeded: {},
	protocol.RunFailed:    {},
	protocol.RunCancelled: {},
}

func CanTransitionRun(from, to protocol.RunState) bool {
	if from == to {
		return true
	}
	for _, s := range runTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

func CheckRunTransition(from, to protocol.RunState) error {
	if !CanTransitionRun(from, to) {
		return fmt.Errorf("run cannot move from %s to %s", from, to)
	}
	return nil
}

// RunTerminal reports whether a run attempt is finished.
func RunTerminal(s protocol.RunState) bool {
	return s == protocol.RunSucceeded || s == protocol.RunFailed || s == protocol.RunCancelled
}

// RunHoldsLease reports whether a run occupies execution capacity on a node.
func RunHoldsLease(s protocol.RunState) bool {
	switch s {
	case protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
		protocol.RunAwaitingInput, protocol.RunStopping:
		return true
	}
	return false
}

// RunActive reports whether a run is scheduled or executing (for per-engineer
// and per-account concurrency).
func RunActive(s protocol.RunState) bool {
	return s == protocol.RunCreated || RunHoldsLease(s)
}

var reviewTransitions = map[protocol.ReviewState][]protocol.ReviewState{
	protocol.ReviewRequested: {protocol.ReviewQueued, protocol.ReviewReviewing, protocol.ReviewCancelled, protocol.ReviewUnable},
	protocol.ReviewQueued:    {protocol.ReviewReviewing, protocol.ReviewCancelled, protocol.ReviewUnable},
	protocol.ReviewReviewing: {
		protocol.ReviewApproved, protocol.ReviewChangesRequested, protocol.ReviewCommentsOnly,
		protocol.ReviewUnable, protocol.ReviewCancelled, protocol.ReviewQueued,
	},
	protocol.ReviewApproved:         {},
	protocol.ReviewChangesRequested: {},
	protocol.ReviewCommentsOnly:     {},
	protocol.ReviewUnable:           {},
	protocol.ReviewCancelled:        {},
}

func CanTransitionReview(from, to protocol.ReviewState) bool {
	if from == to {
		return true
	}
	for _, s := range reviewTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ReviewVerdict reports whether a state is a reviewer's decision.
func ReviewVerdict(s protocol.ReviewState) bool {
	switch s {
	case protocol.ReviewApproved, protocol.ReviewChangesRequested,
		protocol.ReviewCommentsOnly, protocol.ReviewUnable:
		return true
	}
	return false
}

// ReviewOpen reports whether a round still awaits the reviewer.
func ReviewOpen(s protocol.ReviewState) bool {
	return s == protocol.ReviewRequested || s == protocol.ReviewQueued || s == protocol.ReviewReviewing
}
