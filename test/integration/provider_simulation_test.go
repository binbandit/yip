package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

func TestConversationWaitsForReadOnlyCapability(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "fake"} {
		t.Run(provider, func(t *testing.T) {
			e := newEnv(t, envOptions{noRunner: true})
			var engineer protocol.Engineer
			e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
				Engineer *protocol.Engineer `json:"engineer"`
			}{&engineer})
			preference := protocol.ProviderPreference{Provider: provider}
			e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{
				Version: engineer.Version, Provider: &preference,
			}, &engineer)
			installation := protocol.ProviderInstallation{Provider: provider, AuthState: protocol.AuthReady, ProfileID: provider + ":simulation"}
			node := e.fakeNodeWith("edit-only installation", installation)
			e.post("Security", "@Mira please inspect the session contract", []string{"mira"}, nil)
			e.waitFor("conversation waits instead of dispatching to an unsupported provider", 5*time.Second, func() bool {
				if _, offered := node.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun }); offered {
					t.Fatal("conversation offered despite unavailable read-only capability")
				}
				for _, job := range e.jobsWithReplies() {
					if job.Kind == protocol.JobKindReply && job.State == protocol.JobWaiting && strings.Contains(job.StateDetail, "conversation") {
						return true
					}
				}
				e.hub.Tick(e.ctx)
				return false
			})

			installation.Capabilities.ReadOnly = true
			node.send(e, protocol.EvCapabilities, "", 0, protocol.RunnerCapabilities{
				Slots: 2, Profiles: []protocol.ExecutionProfile{{Name: "native", Available: true}},
				Providers: []protocol.ProviderInstallation{installation},
			})
			node.waitFrame(e, "conversation resumes when its capability is available", func(f protocol.Frame) bool {
				return f.Type == protocol.CmdOfferRun
			})
		})
	}
}

// A review a colleague can never pick up would leave the author's work
// waiting silently. The request is refused with the reason and the
// colleagues who can review, and the author re-routes on their own.
func TestReviewRequestNamesReviewersWhoCanRun(t *testing.T) {
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "route"):
			return script(toolStep("work_create", map[string]any{"title": "Route review", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Review != nil:
			return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Fine.", "message": "Approved."}, ""))
		case m.Job.Title == "Route review" && m.Job.Head == "":
			return script(fake.Step{Write: &fake.WriteFile{Path: "NOTES.md", Content: "note\n"}},
				toolStep("work_publish_revision", map[string]any{"summary": "note"}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "pip", "message": "@pip please review"}, "pip"),
				toolStep("room_post", map[string]any{"body": "pip: {{pip.error.message}}"}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "@oren please review"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))
		}
		return nil
	}})
	pip := e.engineerID("pip")
	e.c.must("PUT", "/v1/rooms/"+e.roomID("Security")+"/members/"+pip, nil, nil)
	e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+pip, protocol.PutGrantRequest{Access: "read"}, nil)
	var engineer protocol.Engineer
	e.c.must("GET", "/v1/engineers/"+pip, nil, &struct {
		Engineer *protocol.Engineer `json:"engineer"`
	}{&engineer})
	e.c.must("PATCH", "/v1/engineers/"+pip, protocol.UpdateEngineerRequest{
		Version: engineer.Version, Provider: &protocol.ProviderPreference{Provider: "cursor"},
	}, &engineer)

	e.post("Security", "@Mira route this review", []string{"mira"}, nil)
	refused := e.waitMessage("Security", "pip:")
	if !strings.Contains(refused.Body, "Pip can't review right now") || !strings.Contains(refused.Body, "Cursor isn't installed") ||
		!strings.Contains(refused.Body, "@oren") {
		t.Fatalf("the refusal should explain why and who can review instead: %s", refused.Body)
	}
	j, _ := e.job("Route review")
	e.waitFor("Oren's approval", 60*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		for _, r := range d.Reviews {
			if r.ReviewerID == pip {
				t.Fatalf("a review round was opened for a reviewer who can't run it")
			}
		}
		return len(d.Reviews) == 1 && d.Reviews[0].State == protocol.ReviewApproved
	})
}

// An author withdraws a review that no longer applies; it then neither
// approves nor blocks, and another colleague's approval completes the work.
func TestAuthorWithdrawsReview(t *testing.T) {
	// One active run at a time: Pip's review waits for a slot while Mira works.
	e := newEnv(t, envOptions{limits: func(l *domain.Limits) { l.ActiveRunsPerOrg = 1 }, director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "withdraw"):
			return script(toolStep("work_create", map[string]any{"title": "Withdraw review", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Review != nil:
			return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Fine.", "message": "Approved."}, ""))
		case m.Job.Title == "Withdraw review" && m.Job.Head == "":
			return script(fake.Step{Replace: &fake.ReplaceText{Path: "session/validate.go",
				Old: "\treturn nil\n}", New: "\tif !now.Before(t.ExpiresAt) {\n\t\treturn ErrExpired\n\t}\n\treturn nil\n}"}},
				toolStep("work_publish_revision", map[string]any{"summary": "Reject expired sessions"}, ""),
				toolStep("work_run_check", map[string]any{"command": "go test ./..."}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "pip", "message": "@pip please review"}, ""),
				toolStep("work_withdraw_review", map[string]any{"reviewer": "pip", "reason": "Oren is reviewing instead"}, "w"),
				toolStep("room_post", map[string]any{"body": "withdrawn: {{w.state}}"}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "@oren please review"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))
		case m.Job.Title == "Withdraw review":
			return script(toolStep("work_update", map[string]any{"state": "completed", "summary": "done"}, ""))
		}
		return nil
	}})
	pip := e.engineerID("pip")
	e.c.must("PUT", "/v1/rooms/"+e.roomID("Security")+"/members/"+pip, nil, nil)
	e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+pip, protocol.PutGrantRequest{Access: "read"}, nil)

	e.post("Security", "@Mira withdraw a review", []string{"mira"}, nil)
	if m := e.waitMessage("Security", "withdrawn:"); m.Body != "withdrawn: cancelled" {
		t.Fatalf("withdrawal: %s", m.Body)
	}
	e.waitJob("Withdraw review", protocol.JobCompleted)
	j, _ := e.job("Withdraw review")
	for _, r := range e.jobDetail(j.ID).Reviews {
		if r.ReviewerID == pip && r.State != protocol.ReviewCancelled {
			t.Fatalf("Pip's withdrawn review is %s", r.State)
		}
	}
}

// A provider whose own always-allow rules are all that stops a read-only run
// waits with that reason, and runs once the owner allows the rules on that
// machine, carrying the permission to the adapter.
func TestOwnerAllowsProviderRulesForReadOnlyWork(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	var engineer protocol.Engineer
	e.c.must("GET", "/v1/engineers/"+e.engineerID("mira"), nil, &struct {
		Engineer *protocol.Engineer `json:"engineer"`
	}{&engineer})
	e.c.must("PATCH", "/v1/engineers/"+engineer.ID, protocol.UpdateEngineerRequest{
		Version: engineer.Version, Provider: &protocol.ProviderPreference{Provider: "codex"},
	}, &engineer)
	installation := protocol.ProviderInstallation{Provider: "codex", AuthState: protocol.AuthReady, ProfileID: "codex:simulation",
		Capabilities: protocol.ProviderCapabilities{ExecPolicyRules: []string{"/home/me/.codex/rules/default.rules"}}}
	node := e.fakeNodeWith("rules installation", installation)
	e.post("Security", "@Mira please inspect the session contract", []string{"mira"}, nil)
	e.waitFor("the conversation to wait on the rules", 5*time.Second, func() bool {
		for _, job := range e.jobsWithReplies() {
			if job.Kind == protocol.JobKindReply && job.State == protocol.JobWaiting && strings.Contains(job.StateDetail, "always-allow rules") {
				return true
			}
		}
		e.hub.Tick(e.ctx)
		return false
	})
	if _, offered := node.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun }); offered {
		t.Fatal("offered before the owner allowed the rules")
	}

	var n protocol.Node
	e.c.must("POST", "/v1/nodes/"+node.id+"/trust-rules", protocol.TrustRulesRequest{Provider: "codex", Trust: true}, &n)
	if len(n.TrustedRules) != 1 || n.TrustedRules[0] != "codex" {
		t.Fatalf("trusted rules: %+v", n.TrustedRules)
	}
	offer := node.waitFrame(e, "the conversation to be offered", func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun })
	var o protocol.OfferRun
	if err := json.Unmarshal(offer.Payload, &o); err != nil || !o.Manifest.TrustProviderRules || o.Manifest.Mode != protocol.ModeConversation {
		t.Fatalf("offer must carry the owner's permission: %v %+v", err, o.Manifest)
	}
}
