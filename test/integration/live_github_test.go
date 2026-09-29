package integration

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

// Exercises the real browser API, hub, paired runner and bridge against a
// synthetic public GitHub PR. The provider deliberately emits deterministic
// tool calls; this is separate from the real-model conversation campaign.
func TestLiveGitHubReviewThroughHub(t *testing.T) {
	t.Parallel()
	repo := os.Getenv("YIP_GITHUB_REPO")
	token := os.Getenv("YIP_GITHUB_TOKEN")
	number, _ := strconv.Atoi(os.Getenv("YIP_GITHUB_PR"))
	if os.Getenv("YIP_GITHUB_HUB_WRITE") != "1" || repo == "" || token == "" || number <= 0 {
		t.Skip("set YIP_GITHUB_HUB_WRITE=1 plus REPO, PR and TOKEN for an isolated live GitHub playground")
	}
	if repo != "binbandit/yip-simulation-public" {
		t.Fatal("this live integration fixture only mutates the dedicated public playground")
	}
	owner, name, _ := strings.Cut(repo, "/")
	ref := forge.RepoRef{Host: "github.com", Owner: owner, Name: name}
	expected := os.Getenv("YIP_GITHUB_HUB_EXPECT")
	if expected != "published" && expected != "no_grant" && expected != "ineligible" {
		t.Fatal("YIP_GITHUB_HUB_EXPECT must be published, no_grant or ineligible")
	}
	var e *env
	e = newEnv(t, envOptions{
		forge: func(ctx context.Context, h *hub.Hub, _ protocol.Repo) (forge.Connector, forge.RepoRef, error) {
			return github.New(github.Options{Token: func(ctx context.Context) (string, error) {
				return h.ForgeToken(ctx, "github", "github.com")
			}}), ref, nil
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Security", "live playground"):
				return script(toolStep("work_create", map[string]any{
					"title": "Live playground review", "objective": "Exercise a synthetic GitHub review through yip", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
			case m.Review != nil:
				return script(toolStep("work_review", map[string]any{
					"verdict": "approved", "expectedHead": m.Review.Head,
					"summary": "Synthetic yip integration fixture: exact-head review recorded.", "message": "Simulation review recorded."}, ""),
					toolStep("forge_publish_review", map[string]any{"body": "Synthetic yip hub/runner/bridge simulation. This is test activity, not a production review."}, "p"),
					toolStep("room_post", map[string]any{"body": "live publication: {{p.externalId}} {{p.error.message}}"}, ""),
					fake.Step{Final: "Simulation review finished."})
			case m.Job.Title == "Live playground review":
				return script(toolStep("forge_link_pr", map[string]any{"number": number}, ""),
					toolStep("work_request_review", map[string]any{"reviewer": "oren", "pullRequest": number, "message": "Review the synthetic playground PR."}, ""),
					toolStep("work_wait", map[string]any{"reason": "review"}, ""))
			}
			return nil
		},
	})
	if err := e.hub.PutForgeToken(e.ctx, "github", "github.com", "isolated simulation", token); err != nil {
		t.Fatal(err)
	}
	project := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+project.ID+"/repos/"+project.Repos[0].ID, protocol.PutRepoRequest{
		Name: "atlas", RemoteURL: "https://github.com/" + repo + ".git", DefaultBranch: "main", Forge: "github", ForgeRepo: repo}, nil)
	actions := []string{}
	if expected != "no_grant" {
		actions = append(actions, "publish_review")
	}
	e.c.must("PUT", "/v1/projects/"+project.ID+"/grants/"+e.engineerID("oren"), protocol.PutGrantRequest{Access: "read", Actions: actions}, nil)
	e.post("Security", "@Mira exercise the live playground review", []string{"mira"}, nil)
	var message protocol.Message
	e.waitFor("live publication outcome", 100*time.Second, func() bool {
		for _, m := range e.messages("Security") {
			if strings.HasPrefix(m.Body, "live publication:") {
				message = m
				return true
			}
		}
		return false
	})
	job, ok := e.job("Live playground review")
	if !ok {
		t.Fatal("missing root job")
	}
	detail := e.jobDetail(job.ID)
	if len(detail.Reviews) != 1 || detail.Reviews[0].State != protocol.ReviewApproved || len(detail.PullRequests) != 1 {
		t.Fatalf("internal review or linked PR missing: reviews=%+v prs=%+v", detail.Reviews, detail.PullRequests)
	}
	pr := detail.PullRequests[0]
	round := detail.Reviews[0].Rounds[0]
	connector := github.New(github.Options{Token: func(context.Context) (string, error) { return token, nil }})
	externalID, found, err := connector.FindReviewByMarker(e.ctx, ref, number, round.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch expected {
	case "published":
		if !found || externalID == "" || !strings.Contains(message.Body, externalID) {
			t.Fatalf("publication not confirmed: %q found=%v id=%s", message.Body, found, externalID)
		}
	case "no_grant":
		if found || !strings.Contains(message.Body, "isn't granted") {
			t.Fatalf("missing-grant publication: %s found=%v", message.Body, found)
		}
	case "ineligible":
		if found || !strings.Contains(message.Body, "eligible") {
			t.Fatalf("self-review publication: %s found=%v", message.Body, found)
		}
	}
	if pr.Merge.Merged {
		t.Fatal("review publication must not merge the PR")
	}
	t.Logf("expected=%s internal=%s head=%s remotePublication=%v remoteChecks=%s merge=%s", expected, round.State, round.Target.Head, found, pr.Checks.State, pr.Merge.Mergeable)
}
