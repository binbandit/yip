package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/protocol"
)

// Checks summary states.
const (
	ChecksSuccess = "success"
	ChecksFailure = "failure"
	ChecksPending = "pending"
	ChecksNone    = "none"
	ChecksUnknown = "unknown"
)

type ghCheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued | in_progress | completed | waiting | requested | pending
	Conclusion string `json:"conclusion"` // success | failure | neutral | cancelled | skipped | timed_out | action_required | stale | startup_failure | null
}

type ghCheckRunsPage struct {
	TotalCount int          `json:"total_count"`
	CheckRuns  []ghCheckRun `json:"check_runs"`
}

type ghStatus struct {
	Context string `json:"context"`
	State   string `json:"state"` // error | failure | pending | success
}

type ghCombinedStatus struct {
	State      string     `json:"state"`
	TotalCount int        `json:"total_count"`
	Statuses   []ghStatus `json:"statuses"`
}

// tally accumulates check outcomes from one source.
type tally struct {
	passed, failed, pending int
	// unreadable is set when the credential cannot read this source, or
	// the listing was truncated, so its absence of failures proves nothing.
	unreadable bool
	notFound   bool
}

// Checks combines the latest check runs (GET .../commits/{sha}/check-runs)
// and the combined commit status (GET .../commits/{sha}/status) for sha.
//
// A definite failure from either source yields "failure". Otherwise, if
// either source is unreadable (403, missing checks:read or statuses:read
// permission) or truncated, the result is "unknown" — never "success".
// Then any pending item yields "pending", no items at all "none", and
// otherwise "success". Rate limits and other errors are returned as errors.
func (c *Connector) Checks(ctx context.Context, repo forge.RepoRef, sha string) (protocol.ChecksSummary, error) {
	rp, err := c.repoPath(repo)
	if err != nil {
		return protocol.ChecksSummary{}, err
	}
	if !shaRe.MatchString(sha) {
		return protocol.ChecksSummary{}, fmt.Errorf("github: invalid commit SHA %q", sha)
	}
	base := rp + "/commits/" + url.PathEscape(sha)

	runs, err := c.checkRuns(ctx, base+"/check-runs")
	if err != nil {
		return protocol.ChecksSummary{}, err
	}
	statuses, err := c.commitStatuses(ctx, base+"/status")
	if err != nil {
		return protocol.ChecksSummary{}, err
	}
	if runs.notFound && statuses.notFound {
		return protocol.ChecksSummary{}, fmt.Errorf("github: checks for %s@%s: %w", rp, sha, forge.ErrNotFound)
	}
	return summarize(runs, statuses), nil
}

// sourceErr decides whether an error from one checks source degrades that
// source to "unknown" (permission/visibility) or aborts the whole call.
func sourceErr(t *tally, err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && !errors.Is(err, ErrRateLimited) {
		switch apiErr.StatusCode {
		case 403:
			t.unreadable = true
			return nil
		case 404:
			t.unreadable = true
			t.notFound = true
			return nil
		}
	}
	return err
}

func (c *Connector) checkRuns(ctx context.Context, path string) (tally, error) {
	var t tally
	q := url.Values{"filter": {"latest"}, "per_page": {strconv.Itoa(perPage)}}
	total, seen := 0, 0
	more, err := c.paginate(ctx, c.endpoint(path, q), c.maxPages, func(body []byte) error {
		var page ghCheckRunsPage
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("github: decode GET %s: %w", path, err)
		}
		total = page.TotalCount
		for _, r := range page.CheckRuns {
			seen++
			switch {
			case r.Status != "completed":
				t.pending++
			case r.Conclusion == "success" || r.Conclusion == "neutral" || r.Conclusion == "skipped":
				t.passed++
			case r.Conclusion == "":
				// completed without a conclusion should not happen;
				// don't count it as passing.
				t.pending++
			default: // failure, cancelled, timed_out, action_required, stale, startup_failure
				t.failed++
			}
		}
		return nil
	})
	if err != nil {
		t = tally{} // discard partial counts from earlier pages
		if err := sourceErr(&t, err); err != nil {
			return tally{}, err
		}
		return t, nil
	}
	if more || seen < total {
		t.unreadable = true
	}
	return t, nil
}

func (c *Connector) commitStatuses(ctx context.Context, path string) (tally, error) {
	var t tally
	q := url.Values{"per_page": {strconv.Itoa(perPage)}}
	total, seen := 0, 0
	more, err := c.paginate(ctx, c.endpoint(path, q), c.maxPages, func(body []byte) error {
		var page ghCombinedStatus
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("github: decode GET %s: %w", path, err)
		}
		total = page.TotalCount
		// The combined state is "pending" when there are no statuses at
		// all, so count the individual (latest-per-context) statuses.
		for _, s := range page.Statuses {
			seen++
			switch s.State {
			case "success":
				t.passed++
			case "failure", "error":
				t.failed++
			default:
				t.pending++
			}
		}
		return nil
	})
	if err != nil {
		t = tally{} // discard partial counts from earlier pages
		if err := sourceErr(&t, err); err != nil {
			return tally{}, err
		}
		return t, nil
	}
	if more || seen < total {
		t.unreadable = true
	}
	return t, nil
}

func summarize(sources ...tally) protocol.ChecksSummary {
	var s protocol.ChecksSummary
	unreadable := false
	for _, t := range sources {
		s.Passed += t.passed
		s.Failed += t.failed
		s.Pending += t.pending
		unreadable = unreadable || t.unreadable
	}
	s.Total = s.Passed + s.Failed + s.Pending
	switch {
	case s.Failed > 0:
		s.State = ChecksFailure
	case unreadable:
		s.State = ChecksUnknown
	case s.Pending > 0:
		s.State = ChecksPending
	case s.Total == 0:
		s.State = ChecksNone
	default:
		s.State = ChecksSuccess
	}
	return s
}
