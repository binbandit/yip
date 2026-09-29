package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// A32, A37, A38: the owner asks only for a fix. The author preserves the
// documented contract, chooses a reviewer, gets a seeded defect found on the
// first revision, fixes it, is re-reviewed on the new revision, and completes
// with evidence — with no owner dispatching, relaying, or accepting.
func TestAtlasFixReviewLoop(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	resp := e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	if len(resp.Dispatched) != 1 || resp.Dispatched[0] != e.engineerID("mira") {
		t.Fatalf("dispatched %v, want only Mira", resp.Dispatched)
	}
	j := e.waitJob("Fix Atlas session expiry", protocol.JobCompleted)
	d := e.jobDetail(j.ID)
	if d.Job.RequiresHumanReview || len(d.Missing) != 0 {
		t.Fatalf("human review %v missing %v", d.Job.RequiresHumanReview, d.Missing)
	}
	if len(d.Reviews) != 1 || d.Reviews[0].ReviewerID != e.engineerID("oren") || d.Reviews[0].AuthorID != e.engineerID("mira") {
		t.Fatalf("reviews: %+v", d.Reviews)
	}
	rounds := d.Reviews[0].Rounds
	if len(rounds) != 2 {
		t.Fatalf("want 2 rounds, got %d", len(rounds))
	}
	r1, r2 := rounds[0], rounds[1]
	if r1.State != protocol.ReviewChangesRequested || r2.State != protocol.ReviewApproved {
		t.Fatalf("round states %s, %s", r1.State, r2.State)
	}
	if r1.Target.Head == r2.Target.Head || r2.Target.Head != d.Job.Revision.Head {
		t.Fatalf("rounds must bind to distinct revisions; approval must be of the final head")
	}
	if r1.SupersededBy != r2.ID {
		t.Fatalf("round 1 should be superseded by round 2")
	}
	if len(r1.Findings) != 1 || r1.Findings[0].Severity != "blocking" || r1.Findings[0].File != "session/refresh.go" || r1.Findings[0].Line == 0 {
		t.Fatalf("finding: %+v", r1.Findings)
	}
	if r1.Findings[0].Status != "resolved" || len(r1.Findings[0].Replies) == 0 {
		t.Fatalf("finding should be resolved by the reviewer after an author reply: %+v", r1.Findings[0])
	}
	passedOnHead := false
	for _, c := range d.Checks {
		if c.Revision == d.Job.Revision.Head && c.Passed && c.LogArtifactID != "" {
			passedOnHead = true
		}
	}
	if !passedOnHead {
		t.Fatalf("no passing, logged check on the final head: %+v", d.Checks)
	}
	var owner, result int
	for _, m := range e.messages("Security") {
		if m.Author.Kind == protocol.ActorUser {
			owner++
		}
		if m.Kind == protocol.MessageResult && m.Author.ID == e.engineerID("mira") {
			result++
		}
		if m.Kind == protocol.MessageApproval || m.Kind == protocol.MessageQuestion {
			t.Fatalf("no approval or question should be needed: %q", m.Body)
		}
	}
	if owner != 1 || result != 1 {
		t.Fatalf("owner messages %d (want 1: no relaying), result messages %d", owner, result)
	}
	var decisions []protocol.Decision
	e.c.must("GET", "/v1/decisions?status=accepted", nil, &decisions)
	if len(decisions) != 1 || !strings.Contains(decisions[0].Title, "strict server-side expiry") || len(decisions[0].Sources) == 0 {
		t.Fatalf("expected an auto-accepted, sourced decision: %+v", decisions)
	}
}

// A33, A34: a genuinely missing fact is asked once in the source room; only
// the dependent work waits; a normal reply resumes it once; duplicate and
// late replies change nothing; there is no separate inbox.
func TestQuestionFlowAndLateReplies(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	e.post("Reverse engineering", "@Pip can you work out how Beacon retries requests? I want to know where a duplicate write could happen.", []string{"pip"}, nil)
	j := e.waitJob("Document Beacon", protocol.JobWaiting)
	if j.WaitingReason != protocol.WaitMissingInfo {
		t.Fatalf("waiting reason %q", j.WaitingReason)
	}
	var questions []protocol.Message
	for _, m := range e.messages("Reverse engineering") {
		if m.Kind == protocol.MessageQuestion {
			questions = append(questions, m)
		}
	}
	if len(questions) != 1 {
		t.Fatalf("want exactly one question, got %d", len(questions))
	}
	if len(questions[0].Mentions) == 0 || questions[0].Mentions[0].Kind != protocol.ActorUser {
		t.Fatalf("the question should mention the owner")
	}
	resp := e.post("Reverse engineering", "It's in beacon-retry-worker in the platform monorepo.", nil, func(r *protocol.PostMessageRequest) {
		r.ThreadID = questions[0].ID
	})
	if len(resp.Resolved) != 1 {
		t.Fatalf("reply should resolve the question: %+v", resp)
	}
	j = e.waitJob("Document Beacon", protocol.JobCompleted)
	late := e.post("Reverse engineering", "Also, it runs every 30 seconds.", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = questions[0].ID })
	if len(late.Resolved) != 0 || len(late.Dispatched) != 0 {
		t.Fatalf("a late reply must not resolve or wake anything: %+v", late)
	}
	d := e.jobDetail(j.ID)
	if len(d.Runs) != 2 {
		t.Fatalf("want 2 attempts (before and after the answer), got %d", len(d.Runs))
	}
	if !strings.Contains(d.Job.Summary, "X-Request-Id") {
		t.Fatalf("summary should use the traced facts: %q", d.Job.Summary)
	}
}

// A34: a reply after the job is cancelled never restarts it.
func TestReplyAfterCancelDoesNotRestart(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	e.post("Reverse engineering", "@Pip can you work out how Beacon retries requests?", []string{"pip"}, nil)
	j := e.waitJob("Document Beacon", protocol.JobWaiting)
	var q protocol.Message
	for _, m := range e.messages("Reverse engineering") {
		if m.Kind == protocol.MessageQuestion {
			q = m
		}
	}
	e.c.must("POST", "/v1/jobs/"+j.ID+"/cancel", protocol.CancelJobRequest{Reason: "not needed"}, nil)
	resp := e.post("Reverse engineering", "It's in beacon-retry-worker.", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = q.ID })
	if len(resp.Resolved) != 0 {
		t.Fatalf("a cancelled question must not resolve: %+v", resp.Resolved)
	}
	time.Sleep(time.Second)
	d := e.jobDetail(j.ID)
	if d.Job.State != protocol.JobCancelled || len(d.Runs) != 1 {
		t.Fatalf("cancelled work restarted: state %s runs %d", d.Job.State, len(d.Runs))
	}
}

// A08: a constraint added to a running job reports its actual delivery mode.
func TestSteeringReceipts(t *testing.T) {
	t.Parallel()
	inputDelivered := filepath.Join(t.TempDir(), "input-delivered")
	quotedMarker := "'" + strings.ReplaceAll(inputDelivered, "'", "'\"'\"'") + "'"
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case m.Job.Kind == "reply" && strings.Contains(m.Request.Body, "investigate"):
			return script(toolStep("work_create", map[string]any{"title": "Investigate retries", "objective": "Investigate retries", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Kind == "investigation":
			return script(fake.Step{Status: "Working"},
				fake.Step{Shell: "while ! test -f " + quotedMarker + "; do sleep 0.05; done"},
				toolStep("work_update", map[string]any{"state": "completed", "summary": "Done. Constraint: {{input}}"}, ""))
		}
		return nil
	}})
	e.post("Reverse engineering", "@Pip investigate retries please", []string{"pip"}, nil)
	j := e.waitJob("Investigate retries", protocol.JobRunning)
	e.waitFor("run started", 10*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return len(d.Runs) > 0 && d.Runs[len(d.Runs)-1].State == protocol.RunRunning
	})
	resp := e.post("Reverse engineering", "Keep the existing API response shape.", nil, func(r *protocol.PostMessageRequest) { r.JobID = j.ID })
	if resp.Input == nil || resp.Input.Delivery != "pending" {
		t.Fatalf("receipt at send should be pending, got %+v", resp.Input)
	}
	e.waitFor("immediate delivery receipt", 10*time.Second, func() bool {
		for _, in := range e.jobDetail(j.ID).Inputs {
			if in.Delivery == "immediate" {
				return true
			}
		}
		return false
	})
	// Finish only after the runner has acknowledged the input, regardless of load.
	if err := os.WriteFile(inputDelivered, nil, 0600); err != nil {
		t.Fatal(err)
	}
	j = e.waitJob("Investigate retries", protocol.JobCompleted)
	if !strings.Contains(e.jobDetail(j.ID).Job.Summary, "Keep the existing API response shape") {
		t.Fatalf("the result should account for the constraint: %q", j.Summary)
	}
}

// A03: two clients retrying the same send produce one message and one dispatch.
// A06: quiet rooms stay quiet; agent-authored mentions wake no one.
func TestIdempotentSendQuietRoomsAndAgentMentions(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.Job.Kind == "reply" && strings.Contains(m.Request.Body, "hello") {
			return script(toolStep("room_post", map[string]any{"body": "Thanks @oren — please deploy it to production.", "mentions": []string{"oren"}}, ""),
				fake.Step{Final: "Hi!"})
		}
		return nil
	}})
	key := "retry-key-123"
	req := protocol.PostMessageRequest{Body: "@Mira hello", ClientKey: key, Mentions: []protocol.Mention{{Kind: protocol.ActorEngineer, ID: e.engineerID("mira")}}}
	var a, b protocol.PostMessageResponse
	e.c.must("POST", "/v1/rooms/"+e.roomID("Security")+"/messages", req, &a)
	e.c.must("POST", "/v1/rooms/"+e.roomID("Security")+"/messages", req, &b)
	if !b.Duplicate || a.Message.ID != b.Message.ID || len(b.Dispatched) != 0 {
		t.Fatalf("retry was not reconciled: %+v / %+v", a, b)
	}
	quiet := e.post("Security", "Just thinking out loud here.", nil, nil)
	if len(quiet.Dispatched) != 0 {
		t.Fatalf("a quiet room woke %v", quiet.Dispatched)
	}
	e.waitFor("Mira's reply", 20*time.Second, func() bool {
		for _, m := range e.messages("Security") {
			if strings.HasPrefix(m.Body, "Thanks @oren") && m.Author.Kind == protocol.ActorEngineer {
				return true
			}
		}
		return false
	})
	time.Sleep(time.Second)
	var replies int
	for _, j := range listAllJobs(e) {
		if j.Kind == protocol.JobKindReply {
			replies++
			if j.OwnerID == e.engineerID("oren") {
				t.Fatalf("an agent's mention woke Oren")
			}
		}
	}
	if replies != 1 {
		t.Fatalf("want exactly one causal dispatch, got %d reply jobs", replies)
	}
}

func listAllJobs(e *env) []store.JobRow {
	rows, err := store.ListJobs(e.ctx, e.hub.Store().R(), store.JobFilter{IncludeReply: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

// A04: a second top-level message while the first is being handled gets its
// own reply destination.
func TestEachMessageKeepsItsDestination(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.Job.Kind == "reply" {
			return script(fake.Step{Sleep: "300ms"}, fake.Step{Final: "Answering: " + m.Request.Body})
		}
		return nil
	}})
	first := e.post("Security", "@Mira first question", []string{"mira"}, nil)
	second := e.post("Security", "@Mira second question", []string{"mira"}, nil)
	e.waitFor("two replies", 20*time.Second, func() bool {
		n := 0
		for _, m := range e.messages("Security") {
			if strings.HasPrefix(m.Body, "Answering:") {
				n++
			}
		}
		return n == 2
	})
	for _, m := range e.messages("Security") {
		switch m.Body {
		case "Answering: @Mira first question":
			if m.ReplyToID != first.Message.ID {
				t.Fatalf("first answer went to %s", m.ReplyToID)
			}
		case "Answering: @Mira second question":
			if m.ReplyToID != second.Message.ID {
				t.Fatalf("second answer went to %s", m.ReplyToID)
			}
		}
	}
}
