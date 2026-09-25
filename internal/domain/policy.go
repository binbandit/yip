package domain

import "time"

// Limits are the initial operating defaults from the MVP brief §7. They are
// tunable runtime parameters, not delivery estimates or quota guarantees.
type Limits struct {
	ActiveRunsPerEngineer int
	ActiveRunsPerOrg      int
	ActiveRunsPerAccount  int
	MaxDelegationDepth    int
	MaxWakeupsPerRoot     int
	MaxAutoRetries        int
	MaxReviewRounds       int
	HeartbeatInterval     time.Duration
	SuspectAfterMisses    int
	LeaseDuration         time.Duration
	StopMargin            time.Duration
	ApprovalTTL           time.Duration
	OfferAckTimeout       time.Duration
	RunTimeout            time.Duration
	ReplayRetention       int64 // events kept for SSE replay before a reset is required
	ContextMessageLimit   int
	ContextCharBudget     int
	SSEClientBuffer       int
	MaxToolPayloadBytes   int
	RepeatedFailureLimit  int
	CheckTimeout          time.Duration
}

// DefaultLimits returns the MVP defaults.
func DefaultLimits() Limits {
	return Limits{
		ActiveRunsPerEngineer: 1,
		ActiveRunsPerOrg:      4,
		ActiveRunsPerAccount:  1,
		MaxDelegationDepth:    3,
		MaxWakeupsPerRoot:     12,
		MaxAutoRetries:        2,
		MaxReviewRounds:       3,
		HeartbeatInterval:     10 * time.Second,
		SuspectAfterMisses:    3,
		LeaseDuration:         60 * time.Second,
		StopMargin:            10 * time.Second,
		ApprovalTTL:           15 * time.Minute,
		OfferAckTimeout:       30 * time.Second,
		RunTimeout:            2 * time.Hour,
		ReplayRetention:       200000,
		ContextMessageLimit:   40,
		ContextCharBudget:     60000,
		SSEClientBuffer:       512,
		MaxToolPayloadBytes:   256 * 1024,
		RepeatedFailureLimit:  3,
		CheckTimeout:          15 * time.Minute,
	}
}
