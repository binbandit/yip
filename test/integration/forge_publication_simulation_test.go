package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

type publicationFixture struct {
	actor   atomic.Value
	posted  atomic.Int32
	mu      sync.Mutex
	reviews []map[string]any
}

// The real runner records a verdict and keeps its lease alive while the tests
// call the same authenticated hub tool that the bridge uses. Only GitHub's
// HTTP responses are simulated, including delayed review-list visibility.
func publicationEnv(t *testing.T, postStatus func(int) int, visible func() bool, beforeResponse ...func()) (*env, *publicationFixture, protocol.Run, string) {
	t.Helper()
	f := &publicationFixture{}
	f.actor.Store("reviewer-before")
	var head string
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"login": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "type": "User"})
	})
	mux.HandleFunc("GET /repos/acme/atlas/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"number": 42, "title": "Review retry", "html_url": "https://github.com/acme/atlas/pull/42", "state": "open",
			"user": map[string]any{"login": "author"}, "head": map[string]any{"ref": "main", "sha": head},
			"base": map[string]any{"ref": "main", "sha": head}, "updated_at": time.Now().UTC()})
	})
	mux.HandleFunc("GET /repos/acme/atlas/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		if visible != nil && !visible() {
			write(w, []any{})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.reviews == nil {
			write(w, []any{})
		} else {
			write(w, f.reviews)
		}
	})
	mux.HandleFunc("POST /repos/acme/atlas/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		n := int(f.posted.Add(1))
		var body struct{ Body string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status := http.StatusCreated
		if postStatus != nil {
			status = postStatus(n)
		}
		review := map[string]any{"id": 100 + n, "body": body.Body, "state": "APPROVED", "commit_id": head,
			"user": map[string]any{"login": strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")}}
		if status < 400 || status >= 500 {
			f.mu.Lock()
			f.reviews = append(f.reviews, review)
			f.mu.Unlock()
		}
		for _, wait := range beforeResponse {
			wait()
		}
		w.WriteHeader(status)
		if status >= 400 {
			write(w, map[string]any{"message": "simulated publication failure"})
		} else {
			write(w, review)
		}
	})
	mux.HandleFunc("GET /repos/acme/atlas/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"total_count": 0, "check_runs": []any{}})
	})
	mux.HandleFunc("GET /repos/acme/atlas/commits/{sha}/status", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"state": "success", "total_count": 0, "statuses": []any{}})
	})
	gh := httptest.NewServer(mux)
	t.Cleanup(gh.Close)
	e := newEnv(t, envOptions{
		forge: func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
			return github.New(github.Options{APIBase: gh.URL, Token: func(context.Context) (string, error) { return f.actor.Load().(string), nil }}),
				forge.RepoRef{Host: "github.com", Owner: "acme", Name: "atlas"}, nil
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Security", "publication audit"):
				return script(toolStep("work_create", map[string]any{"title": "Publication audit", "objective": "Review retry", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
			case m.Review != nil:
				return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Verified head", "message": "Approved"}, ""), fake.Step{Fault: "hang"})
			case m.Job.Title == "Publication audit" && m.Purpose == "start":
				return script(toolStep("forge_link_pr", map[string]any{"number": 42}, ""),
					toolStep("work_request_review", map[string]any{"reviewer": "oren", "pullRequest": 42, "message": "Please review PR"}, ""),
					toolStep("work_wait", map[string]any{"reason": "review"}, ""))
			case m.Job.Title == "Publication audit":
				return script(fake.Step{Status: "Audit complete"})
			}
			return nil
		},
	})
	head = gitOut(t, filepath.Join(e.dir, "fixtures", "atlas.git"), "rev-parse", "main")
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/repos/"+a.Repos[0].ID, protocol.PutRepoRequest{Name: "atlas", RemoteURL: a.Repos[0].RemoteURL, DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"}, nil)
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("oren"), protocol.PutGrantRequest{Access: "read", Actions: []string{"publish_review"}}, nil)
	e.post("Security", "@Mira publication audit", []string{"mira"}, nil)
	var run protocol.Run
	var roundID string
	e.waitFor("an approved PR review with a live reviewer lease", 30*time.Second, func() bool {
		job, ok := e.job("Publication audit")
		if !ok {
			return false
		}
		d := e.jobDetail(job.ID)
		if len(d.Reviews) != 1 || len(d.Reviews[0].Rounds) != 1 {
			return false
		}
		round := d.Reviews[0].Rounds[0]
		if round.State != protocol.ReviewApproved {
			return false
		}
		runs := e.jobDetail(round.ReviewJobID).Runs
		if len(runs) == 0 || runs[len(runs)-1].State != protocol.RunRunning {
			return false
		}
		run, roundID = runs[len(runs)-1], round.ID
		return true
	})
	return e, f, run, roundID
}

func publishReviewCall(e *env, run protocol.Run, callID string) protocol.ToolResult {
	return e.hub.HandleToolCall(e.ctx, run.NodeID, run.ID, run.LeaseEpoch, protocol.ToolCall{CallID: callID, Tool: "forge_publish_review", Args: json.RawMessage(`{}`)})
}

func publicationDelivery(t *testing.T, e *env, roundID string) store.ForgeDelivery {
	t.Helper()
	d, err := store.GetForgeDelivery(e.ctx, e.hub.Store().R(), "publish:"+roundID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSimulationPublicationFailedThenSuccessfulRetryIsDurable(t *testing.T) {
	e, f, run, round := publicationEnv(t, func(n int) int {
		if n == 1 {
			return http.StatusForbidden
		}
		return http.StatusCreated
	}, func() bool { return false })
	if result := publishReviewCall(e, run, "rejected"); result.OK {
		t.Fatalf("definite rejection reported success: %+v", result)
	}
	f.actor.Store("reviewer-after")
	result := publishReviewCall(e, run, "successful-retry")
	if !result.OK {
		t.Fatalf("retry failed: %+v", result)
	}
	if strings.Contains(string(result.Result), "reviewer-before") {
		t.Fatalf("publication reported an outdated credential identity: %s", result.Result)
	}
	if d := publicationDelivery(t, e, round); d.Status != "published" || d.ExternalID != "102" {
		t.Fatalf("successful retry was not saved at the canonical key: %+v", d)
	}
	result = publishReviewCall(e, run, "after-confirmed-success")
	if !result.OK || !strings.Contains(string(result.Result), `"duplicate":true`) || f.posted.Load() != 2 {
		t.Fatalf("confirmed publication was repeated despite a durable result: posts=%d result=%+v", f.posted.Load(), result)
	}
}

func TestSimulationPublicationAmbiguousRetryWaitsForVisibleReview(t *testing.T) {
	var visible atomic.Bool
	e, f, run, round := publicationEnv(t, func(int) int { return http.StatusServiceUnavailable }, visible.Load)
	if result := publishReviewCall(e, run, "ambiguous"); result.OK {
		t.Fatalf("ambiguous publication reported success: %+v", result)
	}
	if result := publishReviewCall(e, run, "listing-still-lags"); result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "unresolved") {
		t.Fatalf("missing marker must leave the publication unresolved: %+v", result)
	}
	if d := publicationDelivery(t, e, round); d.Status != "unknown" || f.posted.Load() != 1 {
		t.Fatalf("ambiguous publication was retried: posts=%d delivery=%+v", f.posted.Load(), d)
	}
	visible.Store(true)
	result := publishReviewCall(e, run, "listing-caught-up")
	if !result.OK || !strings.Contains(string(result.Result), `"reconciled":true`) || f.posted.Load() != 1 {
		t.Fatalf("existing review was not reconciled: posts=%d result=%+v", f.posted.Load(), result)
	}
	if d := publicationDelivery(t, e, round); d.Status != "published" || d.ExternalID != "101" {
		t.Fatalf("reconciled result was not saved: %+v", d)
	}
}

func TestSimulationPublicationConcurrentCallsSendOnce(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	e, f, run, _ := publicationEnv(t, func(n int) int {
		if n == 1 {
			close(entered)
			<-release
		}
		return http.StatusCreated
	}, nil)
	first := make(chan protocol.ToolResult, 1)
	go func() { first <- publishReviewCall(e, run, "first-concurrent") }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first publication never reached the forge")
	}
	if result := publishReviewCall(e, run, "second-concurrent"); result.OK || f.posted.Load() != 1 {
		t.Errorf("in-flight publication allowed a concurrent POST: posts=%d result=%+v", f.posted.Load(), result)
	}
	unblock()
	select {
	case result := <-first:
		if !result.OK {
			t.Fatalf("first publication failed: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first publication did not finish")
	}
}

func TestSimulationPublicationPersistenceFailureDoesNotReportSuccess(t *testing.T) {
	e, f, run, round := publicationEnv(t, nil, nil)
	_, err := e.hub.Store().R().ExecContext(e.ctx, `CREATE TRIGGER fail_publication_save BEFORE UPDATE ON forge_deliveries WHEN NEW.status = 'published' BEGIN SELECT RAISE(FAIL, 'simulated disk write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	result := publishReviewCall(e, run, "write-failed")
	if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "could not be saved") {
		t.Fatalf("failed persistence reported success: %+v", result)
	}
	if d := publicationDelivery(t, e, round); d.Status != "pending" {
		t.Fatalf("failed save must remain available for reconciliation: %+v", d)
	}
	if _, err := e.hub.Store().R().ExecContext(e.ctx, `DROP TRIGGER fail_publication_save`); err != nil {
		t.Fatal(err)
	}
	result = publishReviewCall(e, run, "write-recovered")
	if !result.OK || f.posted.Load() != 1 {
		t.Fatalf("publication was not safely reconciled after persistence recovered: posts=%d result=%+v", f.posted.Load(), result)
	}
}

func TestSimulationPublicationConcurrentReconciliationSurvivesLateError(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	e, f, run, round := publicationEnv(t, func(int) int { return http.StatusServiceUnavailable }, nil, func() {
		close(entered)
		<-release
	})
	first := make(chan protocol.ToolResult, 1)
	go func() { first <- publishReviewCall(e, run, "slow-ambiguous-response") }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("publication never reached the forge")
	}
	result := publishReviewCall(e, run, "reconcile-before-response")
	if !result.OK || !strings.Contains(string(result.Result), `"reconciled":true`) {
		t.Fatalf("review already visible on the forge was not reconciled: %+v", result)
	}
	unblock()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("ambiguous response did not finish")
	}
	if d := publicationDelivery(t, e, round); d.Status != "published" || d.ExternalID != "101" || f.posted.Load() != 1 {
		t.Fatalf("late error erased a confirmed publication: posts=%d delivery=%+v", f.posted.Load(), d)
	}
}

func TestSimulationPublicationAdoptsLegacyRetryJournal(t *testing.T) {
	for _, status := range []string{"published", "unknown"} {
		t.Run(status, func(t *testing.T) {
			e, f, run, roundID := publicationEnv(t, nil, nil)
			_, round, err := store.ReviewByReviewJob(e.ctx, e.hub.Store().R(), run.JobID)
			if err != nil {
				t.Fatal(err)
			}
			key := "publish:" + roundID
			for _, d := range []store.ForgeDelivery{
				{ID: "legacy-first-" + roundID, PRID: round.Target.PullRequestID, Kind: "publish_review", DedupeKey: key, Status: "failed", ReviewRoundID: roundID},
				{ID: "legacy-retry-" + roundID, PRID: round.Target.PullRequestID, Kind: "publish_review", DedupeKey: key + ":legacy-retry-" + roundID, Status: status, ExternalID: map[string]string{"published": "701"}[status], ReviewRoundID: roundID},
			} {
				if inserted, err := store.InsertForgeDelivery(e.ctx, e.hub.Store().R(), d); err != nil || !inserted {
					t.Fatalf("seed pre-fix publication journal: inserted=%v err=%v", inserted, err)
				}
			}
			f.actor.Store("reviewer-after")
			for _, callID := range []string{"after-upgrade", "repeated-after-upgrade"} {
				result := publishReviewCall(e, run, callID)
				if status == "published" {
					if !result.OK || !strings.Contains(string(result.Result), `"externalId":"701"`) {
						t.Errorf("confirmed legacy publication was not reused: %+v", result)
					}
				} else if result.OK || result.Error == nil || !strings.Contains(result.Error.Message, "unresolved") {
					t.Errorf("unknown legacy publication should remain unresolved after account rotation: %+v", result)
				}
			}
			if d := publicationDelivery(t, e, roundID); d.ID != "legacy-retry-"+roundID || d.Status != status || f.posted.Load() != 0 {
				t.Fatalf("legacy retry was not safely adopted: posts=%d delivery=%+v", f.posted.Load(), d)
			}
			if status == "unknown" {
				f.actor.Store("reviewer-before")
				f.mu.Lock()
				f.reviews = []map[string]any{{"id": 702, "body": github.MarkerComment(roundID), "state": "APPROVED", "user": map[string]any{"login": "reviewer-before"}}}
				f.mu.Unlock()
				if result := publishReviewCall(e, run, "legacy-marker-now-visible"); !result.OK || f.posted.Load() != 0 {
					t.Fatalf("legacy unknown publication was not reconciled when its marker became visible: %+v", result)
				}
			}
		})
	}
}
