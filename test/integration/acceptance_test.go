package integration

import (
	"database/sql"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// partition blocks reconnects and drops the live connection while reserving the port.
func (e *env) partition() {
	e.runnerPartitioned.Store(true)
	nodes, _ := e.hub.ListNodes(e.ctx)
	for _, n := range nodes {
		e.hub.DropRunnerConnection(n.ID)
	}
}

// heal allows the runner to reconnect to the existing listener.
func (e *env) heal() {
	e.runnerPartitioned.Store(false)
}

func (e *env) roomMessage(room, prefix string) (protocol.Message, bool) {
	for _, m := range e.messages(room) {
		if strings.HasPrefix(m.Body, prefix) {
			return m, true
		}
	}
	return protocol.Message{}, false
}

func (e *env) waitMessage(room, prefix string) protocol.Message {
	e.t.Helper()
	var got protocol.Message
	e.waitFor("message "+prefix, 60*time.Second, func() bool {
		m, ok := e.roomMessage(room, prefix)
		got = m
		return ok
	})
	return got
}

func (e *env) project(name string) protocol.Project {
	var ps []protocol.Project
	e.c.must("GET", "/v1/projects", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	e.t.Fatalf("no project %s", name)
	return protocol.Project{}
}

func replyTo(m *manifest.Manifest, room, contains string) bool {
	return m.Job.Kind == "reply" && m.RoomName == room && m.Request != nil && strings.Contains(m.Request.Body, contains)
}

// A17: a canary in a private room never reaches a broader room's context,
// search, knowledge, decisions, or replies.
func TestPrivateCanaryIsolation(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Incident", "remember"):
			return script(toolStep("decision_propose", map[string]any{"title": "Signing key rotation", "project": "Atlas",
				"body": "The signing key rotates via vault slot 3.", "sources": []string{m.Request.ID}}, ""), fake.Step{Final: "Noted."})
		case (m.RoomName == "Engineering" || m.RoomName == "Incident") && m.Job.Kind == "reply" && strings.Contains(m.Request.Body, "what do you know"):
			return script(toolStep("knowledge_search", map[string]any{"query": "signing key vault slot"}, "k"),
				toolStep("work_status", map[string]any{"includeCompleted": true}, "s"),
				toolStep("room_post", map[string]any{"body": "What I can see here: {{k}} {{s}}"}, ""))
		}
		return nil
	}})
	var incident protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Incident", Private: true, EngineerIDs: []string{e.engineerID("mira")},
		ProjectIDs: []string{e.project("Atlas").ID}}, &incident)
	e.post("Incident", "@Mira remember: the signing key rotates via vault slot 3", []string{"mira"}, nil)
	e.waitMessage("Incident", "Noted.")
	var ds []protocol.Decision
	e.c.must("GET", "/v1/decisions", nil, &ds)
	if len(ds) != 1 || len(ds[0].VisibleRoomIDs) != 1 || ds[0].VisibleRoomIDs[0] != incident.ID {
		t.Fatalf("a decision derived from a private room must stay in it: %+v", ds)
	}
	e.c.must("POST", "/v1/decisions/"+ds[0].ID, protocol.DecisionActionRequest{Action: "accept", Version: ds[0].Version}, nil)

	// Positive control: the same search inside the private room finds it.
	e.post("Incident", "@Mira what do you know about the signing key?", []string{"mira"}, nil)
	inside := e.waitMessage("Incident", "What I can see here:")
	if !strings.Contains(inside.Body, "vault slot 3") {
		t.Fatalf("control: the private room's own search should find the decision: %s", inside.Body)
	}
	e.post("Engineering", "@Mira what do you know about the signing key?", []string{"mira"}, nil)
	reply := e.waitMessage("Engineering", "What I can see here:")
	if strings.Contains(reply.Body, "vault slot") || strings.Contains(reply.Body, "Signing key rotation") {
		t.Fatalf("private canary leaked into Engineering: %s", reply.Body)
	}
	var manifests []string
	rows, _ := e.hub.Store().R().QueryContext(e.ctx, `SELECT manifest FROM runs WHERE dest_room_id = ?`, e.roomID("Engineering"))
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		manifests = append(manifests, s)
	}
	rows.Close()
	for _, m := range manifests {
		if strings.Contains(m, "vault slot") {
			t.Fatalf("a broader-room run manifest contains the canary")
		}
	}
	var scoped []protocol.SearchResult
	e.c.must("GET", "/v1/search?q=vault&room="+e.roomID("Engineering"), nil, &scoped)
	for _, r := range scoped {
		if strings.Contains(r.Snippet, "vault") {
			t.Fatalf("room-scoped search leaked: %+v", r)
		}
	}
	var global []protocol.SearchResult
	e.c.must("GET", "/v1/search?q=vault", nil, &global)
	for _, r := range global {
		if r.RoomID != "" && r.RoomID != incident.ID {
			t.Fatalf("canary result attributed outside its room: %+v", r)
		}
	}
}

// A36: an explicit human-review policy keeps the job review_ready until the
// owner accepts the exact result revision; stale acceptances are refused.
func TestHumanReviewPolicy(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	p := e.project("Atlas")
	pol := p.Policy
	pol.RequireHumanReview = true
	e.c.must("PATCH", "/v1/projects/"+p.ID, protocol.UpdateProjectRequest{Version: p.Version, Policy: &pol}, nil)
	e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	j := e.waitJob("Fix Atlas session expiry", protocol.JobReviewReady, protocol.JobCompleted)
	e.waitFor("owner acceptance required", 60*time.Second, func() bool {
		j, _ = e.job("Fix Atlas session expiry")
		return j.State == protocol.JobReviewReady && strings.Contains(j.StateDetail, "accept")
	})
	time.Sleep(500 * time.Millisecond)
	j, _ = e.job("Fix Atlas session expiry")
	if j.State != protocol.JobReviewReady {
		t.Fatalf("autonomous defaults must not erase the human-review requirement: %s", j.State)
	}
	err := e.c.do("POST", "/v1/jobs/"+j.ID+"/accept", protocol.AcceptJobRequest{Revision: "0000000", Version: j.Version}, nil)
	if !isStatus(err, 409) {
		t.Fatalf("accepting the wrong revision must be a conflict: %v", err)
	}
	err = e.c.do("POST", "/v1/jobs/"+j.ID+"/accept", protocol.AcceptJobRequest{Revision: j.Revision.Head, Version: j.Version - 1}, nil)
	if !isStatus(err, 409) {
		t.Fatalf("a stale version must be a conflict: %v", err)
	}
	var done protocol.Job
	e.c.must("POST", "/v1/jobs/"+j.ID+"/accept", protocol.AcceptJobRequest{Revision: j.Revision.Head, Version: j.Version}, &done)
	if done.State != protocol.JobCompleted {
		t.Fatalf("accept should complete: %s", done.State)
	}
}

// A09: cancelling a job tree stops every active process and child.
func TestCancelJobTree(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "investigate"):
			return script(toolStep("work_create", map[string]any{"title": "Map the queue", "objective": "Map the queue", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Map the queue":
			return script(toolStep("work_request_help", map[string]any{"to": "mira", "question": "Where is the queue configured?"}, ""),
				fake.Step{Fault: "hang"})
		case m.Job.HelpFrom != "":
			return script(fake.Step{Fault: "hang"})
		}
		return nil
	}})
	e.post("Engineering", "@Pip investigate the queue", []string{"pip"}, nil)
	j := e.waitJob("Map the queue", protocol.JobRunning)
	e.waitFor("helper running", 20*time.Second, func() bool {
		h, ok := e.job("Help Pip")
		return ok && h.State == protocol.JobRunning
	})
	e.c.must("POST", "/v1/jobs/"+j.ID+"/cancel", protocol.CancelJobRequest{Reason: "Stop everything"}, nil)
	e.waitFor("all runs confirmed stopped", 30*time.Second, func() bool {
		for _, row := range listAllJobs(e) {
			if row.Kind == protocol.JobKindReply {
				continue
			}
			if row.State != protocol.JobCancelled {
				return false
			}
			for _, r := range e.jobDetail(row.ID).Runs {
				if r.State != protocol.RunCancelled {
					return false
				}
			}
		}
		return true
	})
}

// A13 and recovery: a network partition makes the attempt's outcome unknown;
// no second writer starts; the runner stops at its lease deadline and the
// journal reconciles the true outcome after the partition heals. Retrying is
// an explicit new attempt.
func TestPartitionProducesUnknownThenReconciles(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{
		limits: func(l *domain.Limits) {
			l.LeaseDuration = 3 * time.Second
			l.StopMargin = time.Second
			l.OfferAckTimeout = 3 * time.Second
		},
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Reverse engineering", "trace"):
				return script(toolStep("work_create", map[string]any{"title": "Long trace", "objective": "Trace", "kind": "investigation", "project": "Beacon"}, ""))
			case m.Job.Title == "Long trace":
				return script(fake.Step{Status: "Tracing"}, fake.Step{Fault: "hang"})
			}
			return nil
		}})
	e.post("Reverse engineering", "@Pip trace everything", []string{"pip"}, nil)
	j := e.waitJob("Long trace", protocol.JobRunning)
	e.waitFor("run running", 10*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return len(d.Runs) == 1 && d.Runs[0].State == protocol.RunRunning
	})
	e.partition()
	// A parallel environment must not be able to take this runner's address.
	if ln, err := net.Listen("tcp", strings.TrimPrefix(e.runnerURL, "https://")); err == nil {
		ln.Close()
		t.Fatal("partition released the runner port to other tests")
	}
	j = e.waitJob("Long trace", protocol.JobWaiting)
	if j.WaitingReason != protocol.WaitRecovery || !strings.Contains(j.StateDetail, "not yet confirmed") {
		t.Fatalf("lost runner must be shown as unconfirmed: %s / %s", j.WaitingReason, j.StateDetail)
	}
	d := e.jobDetail(j.ID)
	if len(d.Runs) != 1 || d.Runs[0].State != protocol.RunUnknown {
		t.Fatalf("expected exactly one attempt in unknown state, got %+v", d.Runs)
	}
	time.Sleep(2 * time.Second)
	if n := len(e.jobDetail(j.ID).Runs); n != 1 {
		t.Fatalf("a second writer was launched during the partition (%d runs)", n)
	}
	e.heal()
	e.waitFor("journal reconciliation", 40*time.Second, func() bool {
		d := e.jobDetail(j.ID)
		return d.Runs[0].State == protocol.RunFailed && strings.Contains(d.Runs[0].TerminalReason, "lease")
	})
	var retried protocol.Job
	e.c.must("POST", "/v1/jobs/"+j.ID+"/retry", protocol.RetryJobRequest{Reason: "network is back"}, &retried)
	e.waitFor("second attempt", 20*time.Second, func() bool { return len(e.jobDetail(j.ID).Runs) == 2 })
}

// A11, A12: an offer redelivered after a hub restart (as if the hub crashed
// after commit but before the runner's ack was recorded) executes once.
func TestOutboxRedeliveryExecutesOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if replyTo(m, "Security", "once") {
			return script(fake.Step{Sleep: "3s"}, fake.Step{Final: "Ran once."})
		}
		return nil
	}})
	e.post("Security", "@Mira run this once", []string{"mira"}, nil)
	e.waitFor("reply running", 20*time.Second, func() bool {
		for _, r := range listAllJobs(e) {
			if r.Kind == protocol.JobKindReply && r.State == protocol.JobRunning {
				return true
			}
		}
		return false
	})
	if _, err := e.hub.Store().R().ExecContext(e.ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	_ = e.hub.Store().Tx(e.ctx, func(tx *sqlTx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE outbox SET status = 'pending', next_attempt_at = ? WHERE command_id LIKE 'offer:%' OR command_id LIKE 'start:%'`,
			store.TS(time.Now().Add(-time.Minute)))
		return err
	})
	e.restartHub()
	e.waitMessage("Security", "Ran once.")
	time.Sleep(time.Second)
	n := 0
	for _, m := range e.messages("Security") {
		if m.Body == "Ran once." {
			n++
		}
	}
	var started, runs int
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM run_events WHERE kind = 'started'`).Scan(&started)
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM runs`).Scan(&runs)
	if n != 1 || started != 1 || runs != 1 {
		t.Fatalf("duplicate execution: replies %d, started events %d, runs %d", n, started, runs)
	}
}

// A15: an exhausted allowance waits without busy-looping or paid fallback.
// The allowance belongs to the account, so other work on that account is
// held (with the reason) until it resets instead of hitting the same limit,
// and then continues by itself.
func TestProviderAllowanceWaits(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Reverse engineering", "limit"):
			return script(toolStep("work_create", map[string]any{"title": "Limited work", "objective": "x", "kind": "investigation", "project": "Beacon"}, ""))
		case m.Job.Title == "Limited work":
			return script(fake.Step{Fault: "rate_limit", FaultArg: "1h"})
		case replyTo(m, "Security", "still"):
			return script(fake.Step{Final: "Still working fine."})
		}
		return nil
	}})
	e.post("Reverse engineering", "@Pip hit the limit", []string{"pip"}, nil)
	j := e.waitJob("Limited work", protocol.JobWaiting)
	if j.WaitingReason != protocol.WaitProviderLimit || !strings.Contains(j.StateDetail, "resets at") {
		t.Fatalf("expected an allowance wait with the vendor's reset time: %s / %s", j.WaitingReason, j.StateDetail)
	}
	var profiles []protocol.ProviderProfile
	e.c.must("GET", "/v1/provider-profiles", nil, &profiles)
	if len(profiles) == 0 || profiles[0].PausedUntil == nil {
		t.Fatalf("the account should be paused until it resets: %+v", profiles)
	}
	e.post("Security", "@Mira are you still working?", []string{"mira"}, nil)
	e.waitFor("the reply is held with the account's pause", 15*time.Second, func() bool {
		for _, x := range e.jobsWithReplies() {
			if x.Kind == protocol.JobKindReply && x.OwnerID == e.engineerID("mira") && strings.Contains(x.StateDetail, "paused until") {
				return true
			}
		}
		e.hub.Tick(e.ctx)
		return false
	})
	// The allowance resets: held work carries on without anyone retrying it.
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE provider_profiles SET paused_until = NULL`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.hub.Tick(e.ctx)
	e.waitMessage("Security", "Still working fine.")
	if n := len(e.jobDetail(j.ID).Runs); n != 1 {
		t.Fatalf("busy retry loop: %d attempts", n)
	}
}

// A16, A35: exceptional actions need an exact, single-use decision; stale
// clicks conflict; undecided requests expire and deny; existing grants
// proceed without asking.
func TestExactActionApprovals(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{
		limits: func(l *domain.Limits) { l.ApprovalTTL = 4 * time.Second },
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Security", "push"):
				return script(toolStep("work_create", map[string]any{"title": "Push test", "objective": "push", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
			case m.Job.Title == "Push test":
				return script(
					fake.Step{Approval: &protocol.ApprovalAction{Kind: "exec", Command: "git push origin HEAD", Summary: "Push the branch to origin"}, Save: "a1"},
					toolStep("room_post", map[string]any{"body": "first: {{a1.allowed}}"}, ""),
					fake.Step{Approval: &protocol.ApprovalAction{Kind: "exec", Command: "git push --force origin HEAD", Summary: "Force-push the branch"}, Save: "a2"},
					toolStep("room_post", map[string]any{"body": "second: {{a2.allowed}} {{a2.reason}}"}, ""),
					toolStep("work_update", map[string]any{"state": "failed", "summary": "test complete"}, ""))
			}
			return nil
		}})
	e.post("Security", "@Mira push it", []string{"mira"}, nil)
	var ap protocol.Approval
	e.waitFor("pending approval", 30*time.Second, func() bool {
		j, ok := e.job("Push test")
		if !ok {
			return false
		}
		for _, a := range e.jobDetail(j.ID).Approvals {
			if a.Status == "pending" {
				ap = a
				return true
			}
		}
		return false
	})
	inline := false
	for _, m := range e.messages("Security") {
		for _, r := range m.Refs {
			if r.Kind == "approval" && r.ID == ap.ID && m.Kind == protocol.MessageApproval {
				inline = true
			}
		}
	}
	if !inline {
		t.Fatalf("the exceptional request must appear inline in the source conversation")
	}
	err := e.c.do("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "approve", Version: ap.Version + 1}, nil)
	if !isStatus(err, 409) {
		t.Fatalf("a stale click must conflict: %v", err)
	}
	e.c.must("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "approve", Version: ap.Version}, nil)
	e.waitMessage("Security", "first: true")
	e.waitFor("consumed", 10*time.Second, func() bool {
		var a protocol.Approval
		e.c.must("GET", "/v1/approvals/"+ap.ID, nil, &a)
		return a.Status == "consumed"
	})
	err = e.c.do("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "approve", Version: ap.Version + 1}, nil)
	if !isStatus(err, 409) {
		t.Fatalf("a used approval can't be decided again: %v", err)
	}
	m := e.waitMessage("Security", "second: false")
	if !strings.Contains(m.Body, "expired") {
		t.Fatalf("an undecided request should expire and deny: %s", m.Body)
	}
	// The expiry message precedes work_update. Finish that assignment before
	// sending a new one, or intake can correctly steer the still-open job.
	e.waitJob("Push test", protocol.JobFailed)

	// With the push grant, the same routine push proceeds without asking.
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "write", Actions: []string{"push"}}, nil)
	e.post("Security", "@Mira push it again", []string{"mira"}, nil)
	e.waitFor("second push job done", 30*time.Second, func() bool {
		n := 0
		for _, m := range e.messages("Security") {
			if strings.HasPrefix(m.Body, "first: true") {
				n++
			}
		}
		return n == 2
	})
}

// A07: delegation cycles are rejected and the automatic wakeup budget stops
// further delegation with an explanation.
func TestWakeupBudgetAndCycles(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{
		limits: func(l *domain.Limits) { l.MaxWakeupsPerRoot = 3 },
		director: func(m *manifest.Manifest) json.RawMessage {
			switch {
			case replyTo(m, "Engineering", "coordinate"):
				return script(toolStep("work_create", map[string]any{"title": "Coordinate", "objective": "x", "kind": "investigation", "project": "Beacon"}, ""))
			case m.Job.Title == "Coordinate" && m.Purpose == "start":
				return script(
					toolStep("work_request_help", map[string]any{"to": "pip", "question": "q1"}, "h1"),
					toolStep("work_request_help", map[string]any{"to": "pip", "question": "q2"}, "h2"),
					toolStep("work_request_help", map[string]any{"to": "pip", "question": "q3"}, "h3"),
					toolStep("room_post", map[string]any{"body": "h3: {{h3.error.message}}"}, ""),
					toolStep("work_update", map[string]any{"state": "failed", "summary": "done"}, ""))
			case m.Job.HelpFrom != "":
				return script(toolStep("work_request_help", map[string]any{"to": "mira", "question": "back to you"}, "c"),
					toolStep("room_post", map[string]any{"body": "cycle: {{c.error.message}}"}, ""),
					toolStep("work_respond", map[string]any{"requestId": m.Job.ID, "body": "answer"}, ""))
			}
			return nil
		}})
	e.post("Engineering", "@Mira coordinate this", []string{"mira"}, nil)
	h3 := e.waitMessage("Engineering", "h3:")
	if !strings.Contains(h3.Body, "automatic wakeups") {
		t.Fatalf("the budget should stop further delegation with an explanation: %s", h3.Body)
	}
	cyc := e.waitMessage("Engineering", "cycle:")
	if !strings.Contains(cyc.Body, "cycle") {
		t.Fatalf("a delegation cycle should be rejected: %s", cyc.Body)
	}
}

// A23: a machine without the required provider is never used; the job
// explains the incompatibility.
func TestIncompatibleMachineExplains(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	oren := e.engineerID("oren")
	var eng struct {
		Engineer protocol.Engineer `json:"engineer"`
	}
	e.c.must("GET", "/v1/engineers/"+oren, nil, &eng)
	e.c.must("PATCH", "/v1/engineers/"+oren, protocol.UpdateEngineerRequest{Version: eng.Engineer.Version,
		Provider: &protocol.ProviderPreference{Provider: "codex"}}, nil)
	e.post("Security", "@Oren hello", []string{"oren"}, nil)
	e.waitFor("explained wait", 20*time.Second, func() bool {
		for _, r := range listAllJobs(e) {
			if r.OwnerID == oren && r.State == protocol.JobWaiting && r.WaitingReason == protocol.WaitMachine &&
				strings.Contains(r.StateDetail, "Codex isn't installed on Test mini") {
				return true
			}
		}
		return false
	})
	var offered int
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM runs WHERE engineer_id = ? AND state <> 'created'`, oren).Scan(&offered)
	if offered != 0 {
		t.Fatalf("launched against an ineligible machine")
	}
}

// A41, A39: a duplicated review request is one logical request; an approval
// of an older revision doesn't satisfy completion of a newer one.
func TestReviewDedupeAndRevisionBinding(t *testing.T) {
	t.Parallel()
	change := func(line string) fake.Step {
		return fake.Step{Write: &fake.WriteFile{Path: "NOTES.md", Content: line + "\n"}}
	}
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Security", "bind"):
			return script(toolStep("work_create", map[string]any{"title": "Binding test", "objective": "x", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
		case m.Review != nil:
			return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": m.Review.Head, "summary": "Looks right.", "message": "Approved."}, ""))
		case m.Job.Title == "Binding test" && m.Job.Head == "":
			return script(change("first"),
				toolStep("work_publish_revision", map[string]any{"summary": "first"}, "rev"),
				toolStep("work_run_check", map[string]any{"command": "go test ./..."}, ""),
				toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "@oren please review"}, "r1"),
				toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "@oren please review (again)"}, "r2"),
				toolStep("room_post", map[string]any{"body": "dup: {{r2.duplicate}}"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))
		case m.Job.Title == "Binding test":
			return script(change("second"),
				toolStep("work_publish_revision", map[string]any{"summary": "second"}, "rev"),
				toolStep("work_run_check", map[string]any{"command": "go test ./..."}, ""),
				toolStep("work_update", map[string]any{"state": "completed", "summary": "done"}, "u"),
				toolStep("room_post", map[string]any{"body": "complete: {{u.error.message}}"}, ""))
		}
		return nil
	}})
	e.post("Security", "@Mira bind", []string{"mira"}, nil)
	dup := e.waitMessage("Security", "dup:")
	if dup.Body != "dup: true" {
		t.Fatalf("second identical request should be a duplicate: %s", dup.Body)
	}
	done := e.waitMessage("Security", "complete:")
	if !strings.Contains(done.Body, "older revision") {
		t.Fatalf("completion must require approval of the new head: %s", done.Body)
	}
	j, _ := e.job("Binding test")
	d := e.jobDetail(j.ID)
	if len(d.Reviews) != 1 || len(d.Reviews[0].Rounds) != 1 {
		t.Fatalf("one logical review request expected: %+v", d.Reviews)
	}
	if d.Job.State == protocol.JobCompleted || d.Reviews[0].Rounds[0].Target.Head == d.Job.Revision.Head {
		t.Fatalf("an approval bound to the old revision was applied to the new one")
	}
}

// A05: when builder and reviewer share an account limited to one active run,
// the builder yields and the review still happens.
func TestSharedAccountSingleSlot(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	_ = e.hub.Store().Tx(e.ctx, func(tx *sqlTx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE provider_profiles SET max_concurrency = 1`)
		return err
	})
	e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	e.waitJob("Fix Atlas session expiry", protocol.JobCompleted)
}

// A22: a corrected decision supersedes the old one for later runs.
func TestDecisionCorrection(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	e.waitJob("Fix Atlas session expiry", protocol.JobCompleted)
	var ds []protocol.Decision
	e.c.must("GET", "/v1/decisions?status=accepted", nil, &ds)
	old := ds[0]
	var corrected protocol.Decision
	e.c.must("POST", "/v1/decisions", protocol.DecisionRequest{Scope: old.Scope, Title: "Atlas refresh allows a 30-second clock-skew window",
		Body: "Corrected: refresh tolerates 30 seconds of client clock skew; validation stays strict.", SupersedesID: old.ID, Accept: true}, &corrected)
	e.post("Engineering", "@Mira what did we decide about Atlas session expiry?", []string{"mira"}, nil)
	m := e.waitMessage("Engineering", "We settled on")
	if !strings.Contains(m.Body, "30-second") || strings.Contains(m.Body, old.Title) {
		t.Fatalf("recall should use the corrected decision: %s", m.Body)
	}
	e.c.must("GET", "/v1/decisions", nil, &ds)
	for _, d := range ds {
		if d.ID == old.ID && d.Status != "superseded" {
			t.Fatalf("old decision should be superseded, is %s", d.Status)
		}
	}
}

// A42: without a permitted reviewer the author asks in the room instead of
// expanding access or rubber-stamping.
func TestNoPermittedReviewer(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("oren"), protocol.PutGrantRequest{Access: "none"}, nil)
	e.post("Security", "@Mira can you fix Atlas accepting expired sessions?", []string{"mira"}, nil)
	j := e.waitJob("Fix Atlas session expiry", protocol.JobWaiting)
	if j.WaitingReason != protocol.WaitMissingInfo || len(j.ReviewerIDs) != 0 {
		t.Fatalf("expected a question, not a review without access: %+v", j)
	}
	if _, ok := e.roomMessage("Security", "@brayden Oren needs read access to Atlas"); !ok {
		t.Fatalf("expected the author to ask in the room")
	}
}
