package integration

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// Release-gate checks that play the runner's side by hand: each attempt is
// accepted, reports that it started, makes the same hub tool calls the
// bridge would, and reports its outcome.

// start waits for an offer this machine hasn't taken yet whose run matches,
// accepts it and reports that the attempt started. A nil match takes the
// next offer.
func (n *testNode) start(e *env, what string, match func(store.RunRow) bool) protocol.Frame {
	e.t.Helper()
	f := n.waitFrame(e, what, func(f protocol.Frame) bool {
		if f.Type != protocol.CmdOfferRun || n.taken[f.RunID] {
			return false
		}
		if match == nil {
			return true
		}
		run, err := store.GetRun(e.ctx, e.hub.Store().R(), f.RunID)
		return err == nil && match(run)
	})
	n.mu.Lock()
	n.taken[f.RunID] = true
	n.mu.Unlock()
	n.send(e, protocol.EvRunAck, f.RunID, f.LeaseEpoch, protocol.RunAck{CommandID: f.ID, Accepted: true, State: "accepted"})
	n.event(e, f, protocol.RunEvent{Kind: protocol.RunEvStarted})
	return f
}

// startJob takes the next attempt of the job with that title.
func (n *testNode) startJob(e *env, title string) protocol.Frame {
	e.t.Helper()
	var id string
	e.waitFor("the job "+title, 15*time.Second, func() bool {
		j, ok := e.job(title)
		id = j.ID
		return ok
	})
	return n.start(e, "an attempt of "+title, func(r store.RunRow) bool { return r.JobID == id })
}

func (n *testNode) event(e *env, offer protocol.Frame, ev protocol.RunEvent) {
	n.mu.Lock()
	n.seq[offer.RunID]++
	ev.Seq = n.seq[offer.RunID]
	n.mu.Unlock()
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	n.send(e, protocol.EvRunEvent, offer.RunID, offer.LeaseEpoch, ev)
}

func (n *testNode) call(e *env, offer protocol.Frame, tool string, args map[string]any) protocol.ToolResult {
	b, _ := json.Marshal(args)
	return e.hub.HandleToolCall(e.ctx, n.id, offer.RunID, offer.LeaseEpoch, protocol.ToolCall{CallID: domain.NewID(), Tool: tool, Args: b})
}

func (n *testNode) mustCall(e *env, offer protocol.Frame, tool string, args map[string]any) json.RawMessage {
	e.t.Helper()
	res := n.call(e, offer, tool, args)
	if !res.OK {
		e.t.Fatalf("%s: %+v", tool, res.Error)
	}
	return res.Result
}

func (n *testNode) finish(e *env, offer protocol.Frame, outcome, final string) {
	n.mu.Lock()
	last := n.seq[offer.RunID]
	n.mu.Unlock()
	n.send(e, protocol.EvRunTerminal, offer.RunID, offer.LeaseEpoch, protocol.RunTerminal{Outcome: outcome, FinalText: final, ExitConfirmed: true, LastSeq: last})
}

// ask sends the provider's permission request for an action and returns its
// request ID.
func (n *testNode) ask(e *env, offer protocol.Frame, a protocol.ApprovalAction) string {
	id := domain.NewID()
	n.send(e, protocol.EvApprovalRequest, offer.RunID, offer.LeaseEpoch, protocol.ApprovalRequest{RequestID: id, Action: a})
	return id
}

// resolution waits for the hub's nth (from 1) answer to a permission request
// and acknowledges it as delivered, as the runner does.
func (n *testNode) resolution(e *env, requestID string, nth int) protocol.ResolveApproval {
	e.t.Helper()
	var got protocol.ResolveApproval
	f := n.waitFrame(e, "answer to permission request", func(f protocol.Frame) bool {
		if f.Type != protocol.CmdResolveApproval {
			return false
		}
		var r protocol.ResolveApproval
		if json.Unmarshal(f.Payload, &r) != nil || r.RequestID != requestID {
			return false
		}
		if nth--; nth > 0 {
			return false
		}
		got = r
		return true
	})
	n.send(e, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
	return got
}

func (e *env) manifestOf(runID string) manifest.Manifest {
	e.t.Helper()
	run, err := store.GetRun(e.ctx, e.hub.Store().R(), runID)
	if err != nil {
		e.t.Fatal(err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(run.Manifest, &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

// createWork has the engineer's reply to the message create a job, and
// returns once the reply attempt has finished.
func (n *testNode) createWork(e *env, room, engineer, request string, work map[string]any) {
	e.t.Helper()
	e.post(room, "@"+engineer+" "+request, []string{strings.ToLower(engineer)}, nil)
	reply := n.start(e, "the reply to "+request, nil)
	n.mustCall(e, reply, "work_create", work)
	n.finish(e, reply, protocol.OutcomeSucceeded, "")
}

func codeWork(title string) map[string]any {
	return map[string]any{"title": title, "objective": title, "kind": "code", "project": "Atlas", "repo": "atlas"}
}

// A23: work for a provider no connected machine has waits with the reason,
// and nothing is launched on the ineligible machine.
func TestIncompatibleMachineExplains(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNodeWith("Test mini", protocol.ProviderInstallation{Provider: "claude", AuthState: protocol.AuthReady, ProfileID: "claude:local",
		Capabilities: protocol.ProviderCapabilities{ReadOnly: true}})
	oren := e.engineerID("oren")
	e.post("Security", "@Oren hello", []string{"oren"}, nil)
	e.waitFor("the explained wait", 20*time.Second, func() bool {
		for _, j := range e.jobsWithReplies() {
			if j.OwnerID == oren && j.State == protocol.JobWaiting && j.WaitingReason == protocol.WaitMachine &&
				strings.Contains(j.StateDetail, "Codex isn't installed on Test mini") {
				return true
			}
		}
		return false
	})
	if _, ok := n.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdOfferRun }); ok {
		t.Fatalf("launched against an ineligible machine")
	}
}

// A17: a decision recorded in a private room is found by searches inside it
// and by nothing outside it: not a broader room's tools, run context, or the
// owner's room-scoped search.
func TestPrivateCanaryIsolation(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	var incident protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Incident", Private: true, EngineerIDs: []string{e.engineerID("mira")},
		ProjectIDs: []string{e.project("Atlas").ID}}, &incident)
	sent := e.post("Incident", "@Mira remember: the signing key rotates via vault slot 3", []string{"mira"}, nil)
	r := n.start(e, "the Incident reply", nil)
	n.mustCall(e, r, "decision_propose", map[string]any{"title": "Signing key rotation", "project": "Atlas",
		"body": "The signing key rotates via vault slot 3.", "sources": []string{sent.Message.ID}})
	n.finish(e, r, protocol.OutcomeSucceeded, "Noted.")
	var ds []protocol.Decision
	e.c.must("GET", "/v1/decisions", nil, &ds)
	if len(ds) != 1 || len(ds[0].VisibleRoomIDs) != 1 || ds[0].VisibleRoomIDs[0] != incident.ID {
		t.Fatalf("a decision derived from a private room must stay in it: %+v", ds)
	}
	e.c.must("POST", "/v1/decisions/"+ds[0].ID, protocol.DecisionActionRequest{Action: "accept", Version: ds[0].Version}, nil)

	ask := func(room string) string {
		t.Helper()
		e.post(room, "@Mira what do you know about the signing key?", []string{"mira"}, nil)
		r := n.start(e, "the "+room+" reply", nil)
		seen := string(n.mustCall(e, r, "knowledge_search", map[string]any{"query": "signing key vault slot"})) +
			string(n.mustCall(e, r, "work_status", map[string]any{"includeCompleted": true}))
		n.finish(e, r, protocol.OutcomeSucceeded, "Answered.")
		return seen
	}
	if inside := ask("Incident"); !strings.Contains(inside, "vault slot 3") {
		t.Fatalf("control: the private room's own search should find the decision: %s", inside)
	}
	if outside := ask("Engineering"); strings.Contains(outside, "vault slot") || strings.Contains(outside, "Signing key rotation") {
		t.Fatalf("private canary leaked into Engineering: %s", outside)
	}
	rows, err := e.hub.Store().R().QueryContext(e.ctx, `SELECT manifest FROM runs WHERE dest_room_id = ?`, e.roomID("Engineering"))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		if strings.Contains(s, "vault slot") {
			t.Fatalf("a broader-room run manifest contains the canary")
		}
	}
	rows.Close()
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

// A17: an engineer removed from a room is neither woken by replies in their
// old thread nor given the room's later messages.
func TestRegressionRemovedMemberNotRoutedOrLeaked(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	mira, oren := e.engineerID("mira"), e.engineerID("oren")
	var incident protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Incident", Private: true, EngineerIDs: []string{mira, oren}}, &incident)
	first := e.post("Incident", "@Mira start here", []string{"mira"}, nil)
	r := n.start(e, "Mira's reply", nil)
	n.finish(e, r, protocol.OutcomeSucceeded, "Replying from Mira")
	e.waitMessage("Incident", "Replying from Mira")
	e.c.must("DELETE", "/v1/rooms/"+incident.ID+"/members/"+mira, nil, nil)
	resp := e.post("Incident", "SECRET-CANARY: the breach came from host db-7", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = first.Message.ID })
	for _, id := range resp.Dispatched {
		if id == mira {
			t.Fatalf("a removed engineer was woken by a reply in their old thread")
		}
	}
	e.post("Incident", "@Oren can you take this?", []string{"oren"}, func(r *protocol.PostMessageRequest) { r.ThreadID = first.Message.ID })
	r = n.start(e, "Oren's reply", nil)
	if run, _ := store.GetRun(e.ctx, e.hub.Store().R(), r.RunID); run.EngineerID != oren {
		t.Fatalf("the next attempt went to %s, not Oren", run.EngineerID)
	}
	if m := e.manifestOf(r.RunID); !strings.Contains(mustJSON(m), "SECRET-CANARY") {
		t.Fatalf("control: a current member should see the thread")
	}
	rows, err := e.hub.Store().R().QueryContext(e.ctx, `SELECT manifest FROM runs WHERE engineer_id = ?`, mira)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		count++
		if strings.Contains(s, "SECRET-CANARY") {
			t.Fatalf("the removed engineer received the room's new message")
		}
	}
	rows.Close()
	if count != 1 {
		t.Fatalf("the removed engineer was dispatched again: %d attempts", count)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A17: deleting a message leaves no copy in events, jobs, run context or
// search.
func TestRegressionRedactionIsComplete(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	sent := e.post("Engineering", "@Mira REDACT-CANARY is the staging password", []string{"mira"}, nil)
	r := n.start(e, "the reply", nil)
	if !strings.Contains(mustJSON(e.manifestOf(r.RunID)), "REDACT-CANARY") {
		t.Fatalf("control: the attempt's context should carry the message before it is deleted")
	}
	n.finish(e, r, protocol.OutcomeSucceeded, "Noted.")
	e.waitMessage("Engineering", "Noted.")
	e.c.must("DELETE", "/v1/messages/"+sent.Message.ID, nil, nil)
	q := e.hub.Store().R()
	for what, query := range map[string]string{
		"events":   `SELECT COUNT(*) FROM events WHERE payload LIKE '%REDACT-CANARY%'`,
		"jobs":     `SELECT COUNT(*) FROM jobs WHERE title LIKE '%REDACT-CANARY%' OR objective LIKE '%REDACT-CANARY%'`,
		"runs":     `SELECT COUNT(*) FROM runs WHERE manifest LIKE '%REDACT-CANARY%'`,
		"messages": `SELECT COUNT(*) FROM messages WHERE body LIKE '%REDACT-CANARY%'`,
	} {
		var c int
		if err := q.QueryRowContext(e.ctx, query).Scan(&c); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if c != 0 {
			t.Errorf("the deleted text survives in %s (%d rows)", what, c)
		}
	}
	var results []protocol.SearchResult
	e.c.must("GET", "/v1/search?q=REDACT-CANARY", nil, &results)
	if len(results) != 0 {
		t.Errorf("search still finds the deleted text: %+v", results)
	}
}

// A18: removing an engineer's write access mid-job refuses further tool
// calls and permission requests in that attempt.
func TestAccessRevokedMidJob(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	n.createWork(e, "Security", "Mira", "revoke test", codeWork("Revoke test"))
	r := n.startJob(e, "Revoke test")
	n.mustCall(e, r, "room_post", map[string]any{"body": "Starting."})
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "read"}, nil)
	res := n.call(e, r, "room_post", map[string]any{"body": "late change"})
	if res.OK || res.Error == nil || !strings.Contains(res.Error.Message, "write access to this project was removed") {
		t.Fatalf("a tool call after write access was removed should be refused: %+v", res)
	}
	id := n.ask(e, r, protocol.ApprovalAction{Kind: "exec", Command: "git push origin HEAD", Summary: "Push the branch"})
	if d := n.resolution(e, id, 1); d.Decision != "deny" || !strings.Contains(d.Reason, "removed") {
		t.Fatalf("a permission request after write access was removed should be denied: %+v", d)
	}
	for _, m := range e.messages("Security") {
		if m.Body == "late change" {
			t.Fatalf("the refused post reached the room")
		}
	}
}

// A20: "done" without the expected evidence leaves the work incomplete with
// the missing evidence named, and creates no human task.
func TestDoneWithoutEvidence(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	n.createWork(e, "Security", "Mira", "claim it's done", codeWork("Claim test"))
	r := n.startJob(e, "Claim test")
	res := n.call(e, r, "work_update", map[string]any{"state": "completed", "summary": "All done!"})
	if res.OK || res.Error == nil || !strings.Contains(res.Error.Message, "published result revision") {
		t.Fatalf("missing evidence should be named: %+v", res)
	}
	n.finish(e, r, protocol.OutcomeSucceeded, "")
	for range e.hub.Limits().MaxAutoRetries {
		n.finish(e, n.startJob(e, "Claim test"), protocol.OutcomeSucceeded, "Nothing more to do")
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

// pendingApproval waits for the job's undecided permission request.
func (e *env) pendingApproval(title string) protocol.Approval {
	e.t.Helper()
	var ap protocol.Approval
	e.waitFor("a pending approval on "+title, 15*time.Second, func() bool {
		j, ok := e.job(title)
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
	return ap
}

// A16, A35: exceptional actions need an exact, single-use decision; stale
// clicks conflict; undecided requests expire and deny; existing grants
// proceed without asking.
func TestExactActionApprovals(t *testing.T) {
	e := newEnv(t, envOptions{limits: func(l *domain.Limits) { l.ApprovalTTL = 4 * time.Second }})
	n := e.connectNode("test-runner")
	n.createWork(e, "Security", "Mira", "push it", codeWork("Push test"))
	r := n.startJob(e, "Push test")
	first := n.ask(e, r, protocol.ApprovalAction{Kind: "exec", Command: "git push origin HEAD", Summary: "Push the branch to origin"})
	ap := e.pendingApproval("Push test")
	inline := false
	for _, m := range e.messages("Security") {
		for _, ref := range m.Refs {
			if ref.Kind == "approval" && ref.ID == ap.ID && m.Kind == protocol.MessageApproval {
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
	if d := n.resolution(e, first, 1); d.Decision != "allow" {
		t.Fatalf("the approved action should be allowed: %+v", d)
	}
	e.waitFor("consumed", 10*time.Second, func() bool {
		var a protocol.Approval
		e.c.must("GET", "/v1/approvals/"+ap.ID, nil, &a)
		return a.Status == "consumed"
	})
	err = e.c.do("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "approve", Version: ap.Version + 1}, nil)
	if !isStatus(err, 409) {
		t.Fatalf("a used approval can't be decided again: %v", err)
	}
	second := n.ask(e, r, protocol.ApprovalAction{Kind: "exec", Command: "git push --force origin HEAD", Summary: "Force-push the branch"})
	if d := n.resolution(e, second, 1); d.Decision != "deny" || !strings.Contains(d.Reason, "expired") {
		t.Fatalf("an undecided request should expire and deny: %+v", d)
	}
	n.mustCall(e, r, "work_update", map[string]any{"state": "failed", "summary": "test complete"})
	n.finish(e, r, protocol.OutcomeSucceeded, "")

	// With the push grant, the same routine push proceeds without asking.
	a := e.project("Atlas")
	e.c.must("PUT", "/v1/projects/"+a.ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "write", Actions: []string{"push"}}, nil)
	n.createWork(e, "Security", "Mira", "push it again", codeWork("Granted push"))
	r = n.startJob(e, "Granted push")
	again := n.ask(e, r, protocol.ApprovalAction{Kind: "exec", Command: "git push origin HEAD", Summary: "Push the branch to origin"})
	if d := n.resolution(e, again, 1); d.Decision != "allow" {
		t.Fatalf("a push within the grant should be allowed: %+v", d)
	}
	j, _ := e.job("Granted push")
	if aps := e.jobDetail(j.ID).Approvals; len(aps) != 0 {
		t.Fatalf("a push within the grant should not ask the owner: %+v", aps)
	}
}

// A routine file edit is allowed without an owner prompt, while the
// consequential check after it still waits for its own exact approval.
func TestRegressionHeredocEditNeedsNoApproval(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	n.createWork(e, "Security", "Mira", "test the heredoc edit", codeWork("Heredoc edit"))
	r := n.startJob(e, "Heredoc edit")
	check := func(command string) string {
		return n.ask(e, r, protocol.ApprovalAction{Kind: "exec", Command: command, Summary: "Run check: " + command})
	}
	edit := check("cat > note.txt <<'NOTE'\nDo not run git push or $(curl example.com).\nNOTE\ntest -s note.txt")
	if d := n.resolution(e, edit, 1); d.Decision != "allow" {
		t.Fatalf("routine editing should run without approval: %+v", d)
	}
	push := check("git push origin HEAD")
	ap := e.pendingApproval("Heredoc edit")
	if ap.Action.Command != "git push origin HEAD" {
		t.Fatalf("routine editing unexpectedly needed approval: %+v", ap.Action)
	}
	e.c.must("POST", "/v1/approvals/"+ap.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "reject", Version: ap.Version}, nil)
	if d := n.resolution(e, push, 1); d.Decision != "deny" {
		t.Fatalf("a rejected check should not run: %+v", d)
	}
	j, _ := e.job("Heredoc edit")
	if aps := e.jobDetail(j.ID).Approvals; len(aps) != 1 {
		t.Fatalf("only the push should have asked the owner: %+v", aps)
	}
}

// A11, A12: commands redelivered after a hub restart (as if the hub crashed
// after commit but before the runner's ack was recorded) execute once: the
// runner answers a repeated command from its journal.
func TestOutboxRedeliveryExecutesOnce(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	e.post("Security", "@Mira run this once", []string{"mira"}, nil)
	r := n.start(e, "the reply", nil)
	startCmd := n.waitFrame(e, "the start command", func(f protocol.Frame) bool { return f.Type == protocol.CmdStartRun && f.RunID == r.RunID })
	n.send(e, protocol.EvCommandAck, r.RunID, r.LeaseEpoch, protocol.CommandAck{CommandID: startCmd.ID, OK: true})
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(e.ctx, `UPDATE outbox SET status = 'pending', next_attempt_at = ? WHERE command_id LIKE 'offer:%' OR command_id LIKE 'start:%'`,
			store.TS(time.Now().Add(-time.Minute)))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.restartHub()
	n.connect(e, []protocol.JournalRunState{{RunID: r.RunID, LeaseEpoch: r.LeaseEpoch, State: "running", LastSeq: 1, UnackedFrom: 2}})
	count := func(id string) int {
		c := 0
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, f := range n.frames {
			if f.ID == id {
				c++
			}
		}
		return c
	}
	e.waitFor("the offer and start to be redelivered", 15*time.Second, func() bool {
		e.hub.Tick(e.ctx)
		return count(r.ID) == 2 && count(startCmd.ID) == 2
	})
	n.send(e, protocol.EvRunAck, r.RunID, r.LeaseEpoch, protocol.RunAck{CommandID: r.ID, Accepted: true, State: "accepted", Duplicate: true})
	n.send(e, protocol.EvCommandAck, r.RunID, r.LeaseEpoch, protocol.CommandAck{CommandID: startCmd.ID, OK: true})
	n.finish(e, r, protocol.OutcomeSucceeded, "Ran once.")
	e.waitMessage("Security", "Ran once.")
	replies := 0
	for _, m := range e.messages("Security") {
		if m.Body == "Ran once." {
			replies++
		}
	}
	var started, runs int
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM run_events WHERE kind = 'started'`).Scan(&started)
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM runs`).Scan(&runs)
	if replies != 1 || started != 1 || runs != 1 {
		t.Fatalf("duplicate execution: replies %d, started events %d, runs %d", replies, started, runs)
	}
	var pending int
	_ = e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM outbox WHERE status = 'pending' AND (command_id = ? OR command_id = ?)`, r.ID, startCmd.ID).Scan(&pending)
	if pending != 0 {
		t.Fatalf("the redelivered commands should be acknowledged")
	}
}

// A20: an engineer can't complete their own job by answering it.
func TestRegressionWorkRespondCannotSelfComplete(t *testing.T) {
	e := newEnv(t, envOptions{})
	n := e.connectNode("test-runner")
	n.createWork(e, "Engineering", "Mira", "fix Y", codeWork("Fix Y"))
	r := n.startJob(e, "Fix Y")
	j, _ := e.job("Fix Y")
	if res := n.call(e, r, "work_respond", map[string]any{"requestId": j.ID, "body": "Done, trust me."}); res.OK {
		t.Fatalf("work_respond on its own job must be refused: %s", res.Result)
	}
	n.mustCall(e, r, "work_update", map[string]any{"state": "failed", "summary": "test done"})
	n.finish(e, r, protocol.OutcomeSucceeded, "")
	if j := e.waitJob("Fix Y", protocol.JobFailed, protocol.JobCompleted); j.State == protocol.JobCompleted {
		t.Fatalf("the job completed itself without evidence")
	}
}
