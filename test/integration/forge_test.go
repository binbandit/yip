package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

// fakeGitHub serves one open PR whose author is the same account the shared
// credential authenticates as, with failing checks and a blocked merge.
func fakeGitHub(t *testing.T, head string, posted *atomic.Int32) *httptest.Server {
	var h atomic.Value
	h.Store(head)
	return fakeGitHubHead(t, &h, posted)
}

func fakeGitHubHead(t *testing.T, headV *atomic.Value, posted *atomic.Int32) *httptest.Server {
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	user := map[string]any{"login": "shared-bot", "type": "User"}
	pr := func() map[string]any {
		return map[string]any{"number": 42, "html_url": "https://github.com/acme/atlas/pull/42", "title": "Fix session expiry", "state": "open",
			"merged": false, "draft": false, "mergeable": true, "mergeable_state": "blocked", "user": user, "updated_at": time.Now().UTC(),
			"head": map[string]any{"ref": "fix", "sha": headV.Load().(string)}, "base": map[string]any{"ref": "main", "sha": "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"}}
	}
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) { j(w, user) })
	mux.HandleFunc("GET /repos/acme/atlas/pulls/42", func(w http.ResponseWriter, r *http.Request) { j(w, pr()) })
	mux.HandleFunc("GET /repos/acme/atlas/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) { j(w, []any{}) })
	mux.HandleFunc("POST /repos/acme/atlas/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		posted.Add(1)
		w.WriteHeader(422)
		j(w, map[string]any{"message": "Unprocessable Entity", "errors": []string{"Can not approve your own pull request"}})
	})
	mux.HandleFunc("GET /repos/acme/atlas/commits/{sha}/check-runs", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]any{"total_count": 1, "check_runs": []any{map[string]any{"name": "ci", "status": "completed", "conclusion": "failure"}}})
	})
	mux.HandleFunc("GET /repos/acme/atlas/commits/{sha}/status", func(w http.ResponseWriter, r *http.Request) {
		j(w, map[string]any{"state": "success", "total_count": 0, "statuses": []any{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// A40, A44: engineers sharing the PR author's GitHub credential record an
// internal review, but no remote approval is fabricated; peer approval,
// remote reviews, checks, and merge state stay separate facts.
func TestSharedCredentialCannotFabricateApproval(t *testing.T) {
	var posted atomic.Int32
	var head string
	var gh *httptest.Server
	e := newEnv(t, envOptions{
		forge: func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
			return github.New(github.Options{APIBase: gh.URL, Token: func(ctx context.Context) (string, error) { return "t", nil }}),
				forge.RepoRef{Host: "github.com", Owner: "acme", Name: "atlas"}, nil
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Security", "PR"):
				return script(toolStep("work_create", map[string]any{"title": "Review PR 42", "objective": "Get PR 42 reviewed", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
			case m.Review != nil:
				return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Checked the PR head.", "message": "Approved."}, ""),
					toolStep("forge_publish_review", map[string]any{}, "p"),
					toolStep("room_post", map[string]any{"body": "publish: {{p.error.message}}"}, ""))
			case m.Job.Title == "Review PR 42":
				return script(toolStep("forge_link_pr", map[string]any{"number": 42}, ""),
					toolStep("work_request_review", map[string]any{"reviewer": "oren", "pullRequest": 42, "message": "@oren can you review PR #42?"}, ""),
					toolStep("work_wait", map[string]any{"reason": "review"}, ""))
			}
			return nil
		}})
	// The PR head is a real commit in the fixture repository.
	out, err := exec.Command("git", "-C", filepath.Join(e.dir, "fixtures", "atlas.git"), "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(string(out))
	gh = fakeGitHub(t, head, &posted)
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/repos/"+a.Repos[0].ID, protocol.PutRepoRequest{Name: "atlas", RemoteURL: a.Repos[0].RemoteURL,
		DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"}, nil)
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("oren"), protocol.PutGrantRequest{Access: "read", Actions: []string{"publish_review"}}, nil)
	e.post("Security", "@Mira get the PR reviewed", []string{"mira"}, nil)
	msg := e.waitMessage("Security", "publish:")
	if !strings.Contains(msg.Body, "eligible") {
		t.Fatalf("publishing an approval as the PR author must be refused: %s", msg.Body)
	}
	if posted.Load() != 0 {
		t.Fatalf("an ineligible approval must not be posted to the forge (%d posts)", posted.Load())
	}
	j, _ := e.job("Review PR 42")
	d := e.jobDetail(j.ID)
	if len(d.PullRequests) != 1 {
		t.Fatalf("PR not linked: %+v", d.PullRequests)
	}
	pr := d.PullRequests[0]
	if len(pr.RemoteReviews) != 0 || pr.Checks.State != "failure" || pr.Merge.Merged || pr.Merge.Mergeable != "blocked" {
		t.Fatalf("remote facts must stay as the forge reports them: %+v", pr)
	}
	if len(d.Reviews) != 1 || d.Reviews[0].State != protocol.ReviewApproved || d.Reviews[0].Rounds[0].Target.Head != head {
		t.Fatalf("the internal approval should be recorded against the PR head: %+v", d.Reviews)
	}
}

// A18: removing an engineer's project access mid-job denies further tools.
func TestAccessRevokedMidJob(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "revoke"):
			return script(toolStep("work_create", map[string]any{"title": "Revoke test", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Job.Title == "Revoke test":
			return script(fake.Step{Sleep: "2s"}, fake.Step{Write: &fake.WriteFile{Path: "NOTES.md", Content: "change\n"}},
				toolStep("work_publish_revision", map[string]any{"summary": "late change"}, "p"))
		}
		return nil
	}})
	e.post("Security", "@Mira revoke test", []string{"mira"}, nil)
	j := e.waitJob("Revoke test", protocol.JobRunning)
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "read"}, nil)
	e.waitFor("attempt ended", 30*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return len(d.Runs) > 0 && d.Runs[0].EndedAt != nil
	})
	d := e.jobDetail(j.ID)
	if d.Job.Revision != nil && d.Job.Revision.Head != "" {
		t.Fatalf("a revision was published after write access was revoked")
	}
	var acts []protocol.RunActivity
	e.c.must("GET", "/v1/jobs/"+j.ID+"/runs/"+d.Runs[0].ID+"/activity", nil, &acts)
	denied := false
	for _, a := range acts {
		if strings.Contains(a.Text, "write access to this project was removed") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("the denial should be visible in the run's activity: %+v", acts)
	}
}

// A20: "done" without the expected evidence leaves the work incomplete with
// the missing evidence named, and creates no human-approval task.
func TestDoneWithoutEvidence(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "claim"):
			return script(toolStep("work_create", map[string]any{"title": "Claim test", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Job.Title == "Claim test" && m.Purpose == "start":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "All done!"}, "u"),
				toolStep("room_post", map[string]any{"body": "claim: {{u.error.message}}"}, ""))
		case m.Job.Title == "Claim test":
			return script(fake.Step{Status: "Nothing more to do"})
		}
		return nil
	}})
	e.post("Security", "@Mira claim it's done", []string{"mira"}, nil)
	msg := e.waitMessage("Security", "claim:")
	if !strings.Contains(msg.Body, "published result revision") {
		t.Fatalf("missing evidence should be named: %s", msg.Body)
	}
	j := e.waitJob("Claim test", protocol.JobWaiting)
	if j.WaitingReason != protocol.WaitStalled || !strings.Contains(j.StateDetail, "Missing") {
		t.Fatalf("incomplete work should say what's missing: %s / %s", j.WaitingReason, j.StateDetail)
	}
	for _, m := range e.messages("Security") {
		if m.Kind == protocol.MessageApproval || m.Kind == protocol.MessageQuestion {
			t.Fatalf("no human task should be created: %s", m.Body)
		}
	}
}

// A39, A41: a signed webhook for new PR commits supersedes the open round on
// the old head and schedules exactly one new round; a replayed delivery
// changes nothing.
func TestWebhookSupersedesReviewOnNewCommits(t *testing.T) {
	var posted atomic.Int32
	var headV atomic.Value
	var gh *httptest.Server
	e := newEnv(t, envOptions{
		forge: func(ctx context.Context, h *hub.Hub, repo protocol.Repo) (forge.Connector, forge.RepoRef, error) {
			return github.New(github.Options{APIBase: gh.URL, Token: func(ctx context.Context) (string, error) { return "t", nil }}),
				forge.RepoRef{Host: "github.com", Owner: "acme", Name: "atlas"}, nil
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Security", "PR"):
				return script(toolStep("work_create", map[string]any{"title": "Webhook test", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
			case m.Review != nil && m.Review.Round == 1:
				return script(fake.Step{Fault: "hang"})
			case m.Review != nil:
				return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "New head checked.", "message": "Approved the new head."}, ""))
			case m.Job.Title == "Webhook test":
				return script(toolStep("forge_link_pr", map[string]any{"number": 42}, ""),
					toolStep("work_request_review", map[string]any{"reviewer": "oren", "pullRequest": 42, "message": "@oren PR #42 please"}, ""),
					toolStep("work_wait", map[string]any{"reason": "review"}, ""))
			}
			return nil
		}})
	bare := filepath.Join(e.dir, "fixtures", "atlas.git")
	old := gitOut(t, bare, "rev-parse", "main")
	headV.Store(old)
	gh = fakeGitHubHead(t, &headV, &posted)
	// A second real commit on a PR branch in the remote.
	work := t.TempDir()
	gitOut(t, "", "clone", "-q", bare, work)
	gitOut(t, work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "new PR commit")
	gitOut(t, work, "push", "-q", "origin", "HEAD:refs/heads/pr-42")
	newHead := gitOut(t, work, "rev-parse", "HEAD")

	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/repos/"+a.Repos[0].ID, protocol.PutRepoRequest{Name: "atlas", RemoteURL: a.Repos[0].RemoteURL,
		DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"}, nil)
	if err := e.hub.SetWebhookSecret(e.ctx, "github", "s3cret"); err != nil {
		t.Fatal(err)
	}
	e.post("Security", "@Mira review the PR", []string{"mira"}, nil)
	j := e.waitJob("Webhook test", protocol.JobReviewReady, protocol.JobWaiting)
	e.waitFor("round 1 in progress", 30*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return len(d.Reviews) == 1 && len(d.Reviews[0].Rounds) == 1 && d.Reviews[0].Rounds[0].Target.Head == old
	})
	headV.Store(newHead)
	body := []byte(`{"action":"synchronize","number":42,"pull_request":{"number":42,"head":{"sha":"` + newHead + `"}},"repository":{"name":"atlas","full_name":"acme/atlas","owner":{"login":"acme"}}}`)
	send := func() int {
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(body)
		req, _ := http.NewRequest("POST", e.browser.URL+"/v1/forge/github/webhook", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		req.Header.Set("X-GitHub-Delivery", "delivery-1")
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := send(); code != 204 {
		t.Fatalf("webhook status %d", code)
	}
	e.waitFor("round 2 approved on the new head", 30*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		if len(d.Reviews) != 1 || len(d.Reviews[0].Rounds) != 2 {
			return false
		}
		r1, r2 := d.Reviews[0].Rounds[0], d.Reviews[0].Rounds[1]
		return r1.State == protocol.ReviewCancelled && r1.SupersededBy == r2.ID && r2.Target.Head == newHead && r2.State == protocol.ReviewApproved
	})
	if code := send(); code != 204 {
		t.Fatalf("replay status %d", code)
	}
	time.Sleep(time.Second)
	if n := len(e.jobDetail(j.ID).Reviews[0].Rounds); n != 2 {
		t.Fatalf("a replayed delivery scheduled another round (%d rounds)", n)
	}
	if _, ok := e.roomMessage("Security", "PR #42 moved"); !ok {
		t.Fatalf("the move should be explained in the conversation")
	}
	bad := []byte(`{}`)
	req, _ := http.NewRequest("POST", e.browser.URL+"/v1/forge/github/webhook", bytes.NewReader(bad))
	req.Header.Set("X-Hub-Signature-256", "sha256=00")
	req.Header.Set("X-GitHub-Delivery", "d2")
	req.Header.Set("X-GitHub-Event", "pull_request")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("a bad signature must be refused, got %d", resp.StatusCode)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
