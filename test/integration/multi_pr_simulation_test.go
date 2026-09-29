package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

type multiPRJob struct {
	job    protocol.Job
	repo   protocol.Repo
	number int
}

func multiPREnv(t *testing.T) (*env, []multiPRJob, *atomic.Int32) {
	t.Helper()
	var version atomic.Int32
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"login": "reviewer", "type": "User"})
	})
	mux.HandleFunc("GET /repos/acme/{repo}/pulls/{number}", func(w http.ResponseWriter, r *http.Request) {
		number, _ := strconv.Atoi(r.PathValue("number"))
		write(w, map[string]any{"number": number, "title": fmt.Sprintf("%s #%d version %d", r.PathValue("repo"), number, version.Load()),
			"html_url": fmt.Sprintf("https://github.com/acme/%s/pull/%d", r.PathValue("repo"), number), "state": "open", "mergeable": true, "mergeable_state": "clean",
			"user": map[string]any{"login": "author"}, "head": map[string]any{"ref": "change", "sha": strings.Repeat(strconv.Itoa(number%10), 40)},
			"base": map[string]any{"ref": "main", "sha": strings.Repeat("b", 40)}, "updated_at": time.Now().UTC()})
	})
	mux.HandleFunc("GET /repos/acme/{repo}/pulls/{number}/reviews", func(w http.ResponseWriter, r *http.Request) {
		number, _ := strconv.Atoi(r.PathValue("number"))
		write(w, []any{map[string]any{"id": number, "body": r.PathValue("repo") + " review", "state": "APPROVED",
			"commit_id": strings.Repeat(strconv.Itoa(number%10), 40), "user": map[string]any{"login": "reviewer"}}})
	})
	mux.HandleFunc("GET /repos/acme/{repo}/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		conclusion := "success"
		if strings.HasPrefix(r.PathValue("sha"), "3") {
			conclusion = "failure"
		}
		write(w, map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"name": "ci", "status": "completed", "conclusion": conclusion}}})
	})
	mux.HandleFunc("GET /repos/acme/{repo}/commits/{sha}/status", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"state": "success", "total_count": 0, "statuses": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	specs := []struct {
		title, project, repo, room, engineer string
		number                               int
	}{
		{"Multi PR Atlas first", "Atlas", "atlas", "Security", "mira", 42},
		{"Multi PR Atlas second", "Atlas", "atlas", "Security", "mira", 43},
		{"Multi PR Beacon first", "Beacon", "beacon-gateway", "Engineering", "pip", 42},
	}
	e := newEnv(t, envOptions{
		forge: func(_ context.Context, _ *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
			return github.New(github.Options{APIBase: srv.URL, Token: func(context.Context) (string, error) { return "simulation", nil }}),
				forge.RepoRef{Host: "github.com", Owner: "acme", Name: repo.Name}, nil
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			for _, spec := range specs {
				if replyTo(m, spec.room, spec.title) {
					return script(toolStep("work_create", map[string]any{"title": spec.title, "objective": "Track the independently reviewed pull request", "kind": "code", "project": spec.project, "repo": spec.repo}, ""))
				}
				if m.Job.Title == spec.title {
					return script(toolStep("human_ask", map[string]any{"question": "Which pull request should I track?", "missingFact": "pull request", "contextChecked": "Assignment context"}, ""),
						toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
				}
			}
			return nil
		},
	})
	var jobs []multiPRJob
	for _, spec := range specs {
		project := e.project(spec.project)
		var repo protocol.Repo
		for _, candidate := range project.Repos {
			if candidate.Name == spec.repo {
				repo = candidate
			}
		}
		if repo.ID == "" {
			t.Fatalf("missing fixture repository %s", spec.repo)
		}
		e.c.must("PUT", "/v1/projects/"+project.ID+"/repos/"+repo.ID, protocol.PutRepoRequest{Name: repo.Name, RemoteURL: repo.RemoteURL,
			DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/" + repo.Name}, nil)
		thread := e.post(spec.room, "Track "+spec.title, nil, nil)
		e.post(spec.room, spec.title, []string{spec.engineer}, func(r *protocol.PostMessageRequest) { r.ThreadID = thread.Message.ID })
		jobs = append(jobs, multiPRJob{e.waitJob(spec.title, protocol.JobWaiting), repo, spec.number})
	}
	return e, jobs, &version
}

func linkMultiPR(e *env, job multiPRJob) protocol.PullRequest {
	var pr protocol.PullRequest
	e.c.must("POST", "/v1/pull-requests/link", protocol.LinkPullRequestRequest{JobID: job.job.ID, RepoID: job.repo.ID, Number: job.number}, &pr)
	return pr
}

func TestSimulationMultipleLinkedPRsKeepIndependentIdentity(t *testing.T) {
	t.Parallel()
	e, jobs, version := multiPREnv(t)
	linked := make([]protocol.PullRequest, len(jobs))
	seen := map[string]bool{}
	for i, job := range jobs {
		linked[i] = linkMultiPR(e, job)
		if linked[i].ID == "" || seen[linked[i].ID] {
			t.Errorf("%s #%d received missing or reused PR ID %q", job.repo.Name, job.number, linked[i].ID)
		}
		seen[linked[i].ID] = true
	}
	for i, job := range jobs {
		detail := e.jobDetail(job.job.ID)
		if len(detail.PullRequests) != 1 || detail.PullRequests[0].ID != linked[i].ID || detail.PullRequests[0].Name != job.repo.Name || detail.PullRequests[0].Number != job.number {
			t.Errorf("linking another PR replaced %s's facts: %+v", job.job.Title, detail.PullRequests)
		}
	}
	if t.Failed() {
		return
	}
	version.Store(1)
	for i, job := range jobs {
		var refreshed protocol.PullRequest
		e.c.must("GET", "/v1/pull-requests/"+linked[i].ID+"?refresh=1", nil, &refreshed)
		wantChecks := "success"
		if job.number == 43 {
			wantChecks = "failure"
		}
		if refreshed.ID != linked[i].ID || refreshed.JobID != job.job.ID || refreshed.RepoID != job.repo.ID || refreshed.Number != job.number ||
			refreshed.Title != fmt.Sprintf("%s #%d version 1", job.repo.Name, job.number) || refreshed.Checks.State != wantChecks || len(refreshed.RemoteReviews) != 1 {
			t.Fatalf("refresh mixed independent PR facts: %+v", refreshed)
		}
		if again := linkMultiPR(e, job); again.ID != linked[i].ID {
			t.Fatalf("relink changed the PR identity from %s to %s", linked[i].ID, again.ID)
		}
	}
	e.restartHub()
	for i, job := range jobs {
		detail := e.jobDetail(job.job.ID)
		if len(detail.PullRequests) != 1 || detail.PullRequests[0].ID != linked[i].ID || detail.PullRequests[0].JobID != job.job.ID {
			t.Fatalf("restart lost %s's independent PR identity: %+v", job.job.Title, detail.PullRequests)
		}
	}
	prs, err := store.ListPRs(e.ctx, e.hub.Store().R(), "")
	if err != nil || len(prs) != 3 {
		t.Fatalf("three remote PR identities became %d stored rows: %v", len(prs), err)
	}
}

func TestSimulationLegacyEmptyPRIdentitySurvivesNewLinks(t *testing.T) {
	t.Parallel()
	e, jobs, _ := multiPREnv(t)
	legacy := linkMultiPR(e, jobs[0])
	reviewID, roundID := domain.NewID(), domain.NewID()
	// Seed the identity and reference shape written by the pre-fix hub.
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(e.ctx, `UPDATE pull_requests SET id = '' WHERE id = ?`, legacy.ID); err != nil {
			return err
		}
		review := protocol.Review{ID: reviewID, JobID: jobs[0].job.ID, AuthorID: e.engineerID("mira"), ReviewerID: e.engineerID("oren"),
			Source: jobs[0].job.Source, TargetKind: "pr", State: protocol.ReviewApproved, CurrentRound: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := store.InsertReview(e.ctx, tx, e.hub.Org().ID, review); err != nil {
			return err
		}
		round := protocol.ReviewRound{ID: roundID, ReviewID: reviewID, Number: 1, Target: protocol.ReviewTarget{Kind: "pr", RepoID: legacy.RepoID, Base: legacy.Base, Head: legacy.Head},
			State: protocol.ReviewApproved, Summary: "Approved before upgrade", CreatedAt: time.Now()}
		if err := store.InsertRound(e.ctx, tx, round, "legacy-pr-identity"); err != nil {
			return err
		}
		_, err := store.InsertForgeDelivery(e.ctx, tx, store.ForgeDelivery{ID: domain.NewID(), Kind: "publish_review", DedupeKey: "publish:" + roundID,
			Status: "published", ExternalID: "legacy-remote-review", ReviewRoundID: roundID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs[1:] {
		if pr := linkMultiPR(e, job); pr.ID == "" {
			t.Fatal("a new PR reused the legacy empty identity")
		}
	}
	if refreshed := linkMultiPR(e, jobs[0]); refreshed.ID != "" || refreshed.Number != 42 || refreshed.JobID != jobs[0].job.ID {
		t.Fatalf("refresh remapped the legacy identity or lost its original assignment: %+v", refreshed)
	}
	e.restartHub()
	var review protocol.Review
	e.c.must("GET", "/v1/reviews/"+reviewID, nil, &review)
	if review.PullRequestID != "" || len(review.Rounds) != 1 || review.Rounds[0].Target.PullRequestID != "" || review.Rounds[0].Target.Head != legacy.Head || review.Rounds[0].State != protocol.ReviewApproved {
		t.Fatalf("upgrade lost the legacy immutable review reference: %+v", review)
	}
	pr, err := store.GetPR(e.ctx, e.hub.Store().R(), review.Rounds[0].Target.PullRequestID)
	if err != nil || pr.JobID != jobs[0].job.ID || pr.RepoID != legacy.RepoID || pr.Number != legacy.Number {
		t.Fatalf("the legacy review now resolves to another PR: %+v (%v)", pr, err)
	}
	delivery, err := store.GetForgeDelivery(e.ctx, e.hub.Store().R(), "publish:"+roundID)
	if err != nil || delivery.Status != "published" || delivery.ExternalID != "legacy-remote-review" || delivery.ReviewRoundID != roundID {
		t.Fatalf("upgrade lost the published review receipt: %+v (%v)", delivery, err)
	}
	prs, err := store.ListPRs(e.ctx, e.hub.Store().R(), "")
	if err != nil || len(prs) != 3 {
		t.Fatalf("legacy plus two new PRs became %d rows: %v", len(prs), err)
	}
}
