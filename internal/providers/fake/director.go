package fake

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	manifest "github.com/binbandit/yip/internal/context"
)

// Direct selects a deterministic script for a run from its context manifest.
// It encodes the demonstration workflows; anything else gets an honest
// limitation instead of pretended work.
func Direct(m *manifest.Manifest) json.RawMessage {
	var s Script
	switch {
	case m.Job.Kind == "reply":
		s = replyScript(m)
	case m.Review != nil:
		s = reviewScript(m)
	case m.Job.HelpFrom != "":
		s = Script{Steps: []Step{tool("work_respond", map[string]any{"requestId": m.Job.ID,
			"body": "I'm running on the deterministic fake provider and have no scripted answer for this; a real provider would investigate it."}, "")}}
	case m.Job.Kind == "code":
		s = codeScript(m)
	case m.Job.Kind == "investigation" || m.Job.Kind == "document":
		s = investigationScript(m)
	default:
		s = limitation(m)
	}
	s.Steps = append(steeringNotes(m), s.Steps...)
	b, _ := json.Marshal(s)
	return b
}

// ---- step builders ----

func say(t string) Step    { return Step{Say: t} }
func status(t string) Step { return Step{Status: t} }
func final(t string) Step  { return Step{Final: t} }
func pause() Step          { return Step{Pause: true} }
func shell(cmd, save string) Step {
	return Step{Shell: cmd, Save: save}
}
func tool(name string, args map[string]any, save string) Step {
	b, _ := json.Marshal(args)
	return Step{Tool: name, Args: b, Save: save}
}
func ifEq(v string, eq any, then, els []Step) Step {
	return Step{If: &Cond{Var: v, Eq: eq}, Then: then, Else: els}
}

func limitation(m *manifest.Manifest) Script {
	msg := "I'm running on yip's deterministic fake provider, which only follows the scripted demo workflows (the Atlas expiry fix and review, the Beacon request-flow investigation, status, and decision recall). Configure Codex, Claude Code, or Cursor for open-ended work."
	if m.Job.Kind == "reply" {
		return Script{Steps: []Step{final(msg)}}
	}
	return Script{Steps: []Step{tool("work_update", map[string]any{"state": "failed", "summary": msg}, "")}}
}

func steeringNotes(m *manifest.Manifest) []Step {
	var out []Step
	for _, in := range m.Inputs {
		if in.Kind == "owner_input" {
			out = append(out, status("Taking your update into account: "+trunc(in.Body, 120)))
		}
	}
	return out
}

// ---- project helpers ----

type projectRef struct {
	name  string
	repos []string
}

var projRe = regexp.MustCompile(`^(.*?) \(repos: (.*)\)$`)

func projects(m *manifest.Manifest) []projectRef {
	var out []projectRef
	for _, p := range m.Projects {
		if mm := projRe.FindStringSubmatch(p); mm != nil {
			out = append(out, projectRef{name: mm[1], repos: strings.Split(mm[2], ", ")})
		} else {
			out = append(out, projectRef{name: p})
		}
	}
	return out
}

func pickProject(m *manifest.Manifest, text string) (projectRef, bool) {
	ps := projects(m)
	for _, p := range ps {
		if strings.Contains(text, strings.ToLower(p.name)) {
			return p, true
		}
		for _, r := range p.repos {
			if r != "" && strings.Contains(text, strings.ToLower(r)) {
				return p, true
			}
		}
	}
	for _, p := range ps {
		if len(p.repos) > 0 {
			return p, len(ps) == 1
		}
	}
	return projectRef{}, false
}

func requestText(m *manifest.Manifest) string {
	if m.Request != nil {
		return m.Request.Body
	}
	return m.Job.Objective
}

var (
	fixRe    = regexp.MustCompile(`\b(fix|fixes|patch|implement|change|repair|bug|broken|accept(s|ing)? expired)\b`)
	studyRe  = regexp.MustCompile(`\b(document|map|trace|investigate|understand|explain|figure out|reverse|how does|request flow|retries|retry)\b`)
	statusRe = regexp.MustCompile(`\b(status|where are we|how are|update me|what'?s (happening|going on)|progress)\b`)
	recallRe = regexp.MustCompile(`\b(decid(ed|e|ing)|decision|policy|contract|agreed|remember|settled)\b`)
	expiryRe = regexp.MustCompile(`expir|session`)
	// A clarification to work already under way ("also keep …").
	clarifyRe = regexp.MustCompile(`\b(also|keep|make sure|actually|instead|don'?t|do not|as well)\b`)
)

// ---- conversational replies ----

func replyScript(m *manifest.Manifest) Script {
	text := strings.ToLower(requestText(m))
	switch {
	case len(m.OpenWork) > 0 && clarifyRe.MatchString(text):
		if len(m.OpenWork) == 1 {
			// Goes to the assignment; the owner sees a receipt, not another message.
			return Script{Steps: []Step{tool("work_add_input", map[string]any{"job": m.OpenWork[0].ID}, "")}}
		}
		var titles []string
		for _, w := range m.OpenWork {
			titles = append(titles, w.Title)
		}
		return Script{Steps: []Step{final("Which one is that for: " + strings.Join(titles, " or ") + "?")}}
	case strings.Contains(text, "where did we land"):
		for _, n := range m.Notes {
			if n.Kind == "record" && expiryRe.MatchString(strings.ToLower(n.Body)) {
				return Script{Steps: []Step{final(n.Body)}}
			}
		}
		return Script{Steps: []Step{final("I don't have a finished work record for that here.")}}
	case recallRe.MatchString(text):
		if len(m.Decisions) == 0 {
			return Script{Steps: []Step{final("I can't find a recorded decision about that from this conversation. If it was decided in a private room, it stays there — ask me in that room, or record it here.")}}
		}
		d := m.Decisions[0]
		for _, x := range m.Decisions {
			if strings.Contains(text, strings.ToLower(x.Scope)) || (expiryRe.MatchString(text) && expiryRe.MatchString(strings.ToLower(x.Title+x.Body))) {
				d = x
				break
			}
		}
		return Script{Steps: []Step{final(fmt.Sprintf("We settled on **%s**. %s", d.Title, d.Body))}}
	case statusRe.MatchString(text):
		return Script{Steps: []Step{tool("work_status", map[string]any{"includeCompleted": true}, "s"), {Summarize: "s"}}}
	case fixRe.MatchString(text):
		p, ok := pickProject(m, text)
		if p.name == "" || len(p.repos) == 0 {
			return Script{Steps: []Step{final("Which project is this in? None of this room's linked projects has a repository I can change.")}}
		}
		if !ok && len(projects(m)) > 1 {
			return Script{Steps: []Step{final(fmt.Sprintf("Which project should I change — %s? This room links more than one.", names(projects(m))))}}
		}
		title := "Fix: " + trunc(firstLine(requestText(m)), 70)
		acceptance := []string{"A regression test covers the reported behaviour", "The project's checks pass", "An independent colleague approves the final revision"}
		if expiryRe.MatchString(text) {
			title = "Fix " + p.name + " session expiry"
			acceptance = []string{"Expired sessions are rejected at and after ExpiresAt", "Regression tests cover the exact boundary", "go test ./... passes", "Security review approves the final revision"}
		}
		return Script{Steps: []Step{
			tool("work_create", map[string]any{"title": title, "objective": requestText(m), "kind": "code", "project": p.name, "repo": p.repos[0],
				"acceptance": acceptance}, "w"),
			ifEq("w.ok", true,
				[]Step{final(fmt.Sprintf("On it. I'll check the %s change against the existing contract.", p.name))},
				[]Step{final("I couldn't start that: {{w.error.message}}")}),
		}}
	case studyRe.MatchString(text):
		p, _ := pickProject(m, text)
		if p.name == "" {
			return Script{Steps: []Step{final("Which project should I look at? None is linked to this room.")}}
		}
		title := "Investigate: " + trunc(firstLine(requestText(m)), 70)
		if strings.Contains(text, "request") || strings.Contains(text, "retr") {
			title = "Document " + p.name + "'s request flow"
		}
		args := map[string]any{"title": title, "objective": requestText(m), "kind": "investigation", "project": p.name,
			"acceptance": []string{"A written map of the flow with source locations", "Places where duplicate writes could happen are identified"}}
		if len(p.repos) > 0 {
			args["repo"] = p.repos[0]
		}
		return Script{Steps: []Step{
			tool("work_create", args, "w"),
			ifEq("w.ok", true,
				[]Step{final("I'll trace the flow and write up what the code confirms.")},
				[]Step{final("I couldn't start that: {{w.error.message}}")}),
		}}
	}
	return limitation(m)
}

func names(ps []projectRef) string {
	var n []string
	for _, p := range ps {
		n = append(n, p.name)
	}
	return strings.Join(n, " or ")
}

// ---- code work: the Atlas session-expiry scenario ----

func pickReviewer(m *manifest.Manifest) (manifest.Colleague, bool) {
	var fallback *manifest.Colleague
	for i, c := range m.Colleagues {
		if !c.InRoom || c.Access == "none" {
			continue
		}
		for _, t := range c.Tags {
			if t == "security" || t == "review" || t == "auth" {
				return c, true
			}
		}
		if fallback == nil {
			fallback = &m.Colleagues[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return manifest.Colleague{}, false
}

func codeScript(m *manifest.Manifest) Script {
	isExpiry := m.Job.Repo == "atlas" && expiryRe.MatchString(strings.ToLower(m.Job.Title+" "+m.Job.Objective))
	if !isExpiry {
		return limitation(m)
	}
	if m.OwnReview != nil && m.OwnReview.Verdict == "changes_requested" {
		return addressReview(m)
	}
	if strings.Contains(strings.ToLower(m.Job.Objective), "client rollout") && m.Job.Head != "" && m.OwnReview == nil && (m.Purpose == "answer" || m.Purpose == "input") {
		answer := ""
		for _, in := range m.Inputs {
			if in.Kind == "answer" {
				answer = in.Body
			}
		}
		if answer == "" {
			return Script{Steps: []Step{tool("work_wait", map[string]any{"reason": "missing_information"}, "")}}
		}
		rv, ok := pickReviewer(m)
		if !ok {
			return missingReviewer(m)
		}
		return Script{Steps: []Step{
			{Write: &WriteFile{Path: "docs/client-rollout.md", Content: "# Client rollout\n\nOwner's release scope: " + answer + "\n\nThe existing session contract and error codes are unchanged. Expired sessions must be rejected at and after ExpiresAt on both validation and refresh.\n"}},
			tool("work_publish_revision", map[string]any{"summary": "Record the clarified client rollout scope"}, ""),
			tool("work_run_check", map[string]any{"name": "go test", "command": "go test ./..."}, ""),
			tool("work_request_review", map[string]any{"reviewer": rv.Handle, "criteria": "The exact expiry boundary and the refresh path", "message": "@" + rv.Handle + " can you check expiry and refresh? The rollout note records the owner's release scope; the session contract stays unchanged."}, ""),
			tool("work_update", map[string]any{"state": "completed", "summary": "Validation rejects expired sessions; the client rollout keeps the existing session contract."}, ""),
		}}
	}
	if m.Job.Head != "" {
		return Script{Steps: []Step{tool("work_update", map[string]any{"state": "completed",
			"summary": "Expired sessions are rejected at and after ExpiresAt on both validation and refresh, preserving the strict-expiry contract."}, "")}}
	}
	rv, ok := pickReviewer(m)
	steps := []Step{
		status("Reading the session contract"),
		shell("cat docs/sessions.md", "contract"),
		shell("grep -n 'func Validate' -A6 session/validate.go", "validate"),
		pause(),
		status("Adding the expiry check to Validate"),
		{Replace: &ReplaceText{Path: "session/validate.go",
			Old: "\tif !validSignature(t) {\n\t\treturn ErrInvalid\n\t}\n\treturn nil\n}",
			New: "\tif !validSignature(t) {\n\t\treturn ErrInvalid\n\t}\n\t// Strict server-side expiry (docs/sessions.md): invalid at and after ExpiresAt.\n\tif !now.Before(t.ExpiresAt) {\n\t\treturn ErrExpired\n\t}\n\treturn nil\n}"}},
		pause(),
		tool("work_publish_revision", map[string]any{"summary": "Reject expired sessions in Validate"}, "rev"),
		tool("work_run_check", map[string]any{"name": "go test", "command": "go test ./..."}, "check"),
	}
	var src []string
	if m.Request != nil {
		src = []string{m.Request.ID}
	}
	if len(src) > 0 {
		steps = append(steps, tool("decision_propose", map[string]any{"title": "Atlas uses strict server-side expiry",
			"body":    "A session token is valid only while now is strictly before ExpiresAt. At the exact expiry instant and after it, the server rejects the token — including on the refresh path. There is no grace period.",
			"project": "Atlas", "sources": src}, "decision"))
	}
	if strings.Contains(strings.ToLower(m.Job.Objective), "client rollout") {
		return Script{Steps: append(steps,
			tool("human_ask", map[string]any{"question": "Validation checks pass. Which client release should the compatibility note cover?", "missingFact": "Client release for the rollout note", "contextChecked": "The repository documents the session contract but not the client rollout", "dependentStep": "The rollout note", "continuingWith": "The implementation and checks are ready; the rollout note needs the release scope"}, ""),
			tool("work_wait", map[string]any{"reason": "missing_information"}, ""))}
	}
	if !m.Job.PeerReview {
		// Nobody else here to review (the owner reviews it): finish with evidence.
		return Script{Steps: append(steps, tool("work_update", map[string]any{"state": "completed",
			"summary": "Expired sessions are rejected: Validate now refuses a token at and after ExpiresAt, preserving the strict-expiry contract in docs/sessions.md. go test ./... passes on {{rev.head}}. Ready for your review."}, "done"))}
	}
	if !ok {
		return Script{Steps: append(steps, missingReviewer(m).Steps...)}
	}

	steps = append(steps,
		pause(),
		tool("work_request_review", map[string]any{"reviewer": rv.Handle, "criteria": "The exact expiry boundary and the refresh path",
			"message": "@" + rv.Handle + " can you review the expiry fix? The exact expiry boundary is the bit I want a second pair of eyes on."}, "rr"),
		tool("work_update", map[string]any{"state": "completed",
			"summary": "Expired sessions are rejected: Validate now refuses a token at and after ExpiresAt, preserving the strict-expiry contract in docs/sessions.md (no grace period). go test ./... passes on {{rev.head}}."}, "done"),
	)
	return Script{Steps: steps}
}

func missingReviewer(m *manifest.Manifest) Script {
	missing := "A colleague needs room membership and read access to " + m.Job.Project + " to review this work."
	for _, c := range m.Colleagues {
		if c.InRoom && c.Access == "none" {
			missing = c.Name + " needs read access to " + m.Job.Project + " to review here."
			break
		}
		if !c.InRoom && c.Access != "none" {
			missing = c.Name + " needs membership in #" + m.RoomName + " to review here."
		}
	}
	return Script{Steps: []Step{
		tool("human_ask", map[string]any{"question": "@" + m.OwnerHandle + " " + missing + " Could you grant that access or choose another reviewer?", "missingFact": missing, "contextChecked": "Room members and their project access", "dependentStep": "Peer review", "continuingWith": "The implementation and checks are ready"}, ""),
		tool("work_wait", map[string]any{"reason": "missing_information"}, ""),
	}}
}

func addressReview(m *manifest.Manifest) Script {
	r := m.OwnReview
	var responses []map[string]any
	for _, f := range r.Findings {
		if f.Severity == "blocking" && f.Status != "resolved" {
			responses = append(responses, map[string]any{"findingId": f.ID,
				"body":     "Refresh now calls the shared validator, so a token at the exact expiry instant is rejected. Added TestRefreshRejectsTokenAtExactExpiry.",
				"evidence": "go test ./... passes on {{rev.head}}"})
		}
	}
	reviewerHandle := strings.ToLower(r.Reviewer)
	for _, c := range m.Colleagues {
		if c.Name == r.Reviewer {
			reviewerHandle = c.Handle
		}
	}
	return Script{Steps: []Step{
		status("Addressing " + r.Reviewer + "'s finding on the refresh path"),
		shell("grep -n 'now.After' session/refresh.go", "g"),
		{Replace: &ReplaceText{Path: "session/refresh.go",
			Old: "\tif !validSignature(t) {\n\t\treturn Token{}, ErrInvalid\n\t}\n\tif now.After(t.ExpiresAt) {\n\t\treturn Token{}, ErrExpired\n\t}\n",
			New: "\t// Use the shared validator so refresh honours the same strict expiry.\n\tif err := Validate(t, now); err != nil {\n\t\treturn Token{}, err\n\t}\n"}},
		{Write: &WriteFile{Path: "session/refresh_test.go", Content: refreshTest}},
		pause(),
		tool("work_publish_revision", map[string]any{"summary": "Use the shared validator in Refresh and cover the exact expiry boundary"}, "rev"),
		tool("work_run_check", map[string]any{"name": "go test", "command": "go test ./..."}, "check"),
		tool("work_respond_to_review", map[string]any{"responses": responses, "requestRereview": true,
			"message": "Good catch. Refresh now uses the shared validator, and the new regression passes. @" + reviewerHandle + " ready for another look."}, "resp"),
		tool("work_update", map[string]any{"state": "completed",
			"summary": "Expired sessions are rejected at and after ExpiresAt on both Validate and Refresh. Refresh now uses the shared validator, with regression coverage for the exact boundary."}, ""),
	}}
}

const refreshTest = `package session

import (
	"testing"
	"time"
)

// Regression: refresh must reject a token at the exact expiry instant.
func TestRefreshRejectsTokenAtExactExpiry(t *testing.T) {
	tok := Sign("mira", base)
	if _, err := Refresh(tok, base); err != ErrExpired {
		t.Fatalf("refresh at expiry: got %v, want ErrExpired", err)
	}
}

func TestRefreshRejectsExpiredToken(t *testing.T) {
	tok := Sign("mira", base.Add(-time.Second))
	if _, err := Refresh(tok, base); err != ErrExpired {
		t.Fatalf("refresh after expiry: got %v, want ErrExpired", err)
	}
}
`

// ---- review ----

func reviewScript(m *manifest.Manifest) Script {
	r := m.Review
	if !(strings.EqualFold(m.Job.Repo, "atlas") || strings.Contains(strings.ToLower(m.Job.Objective), "expir")) || r.TargetKind != "patch" {
		return Script{Steps: []Step{tool("work_review", map[string]any{"verdict": "unable_to_review", "expectedHead": r.Head,
			"summary": "I'm running on the deterministic fake provider and have no scripted review for this change, so I can't honestly approve or reject it.",
			"message": "I can't review this one: the fake provider has no scripted review for it. A real provider would read the change."}, "")}}
	}
	var resolve []string
	for _, f := range r.Findings {
		if f.Status != "resolved" && f.Severity == "blocking" {
			resolve = append(resolve, f.ID)
		}
	}
	author := r.Author
	return Script{Steps: []Step{
		status(fmt.Sprintf("Reading %s's change %s..%s", author, short(r.Base), short(r.Head))),
		shell(fmt.Sprintf("git diff --stat %s %s", r.Base, r.Head), "stat"),
		shell("grep -n 'now.After(t.ExpiresAt)' session/refresh.go", "g"),
		pause(),
		status("Running the tests against this exact revision"),
		tool("work_run_check", map[string]any{"name": "go test (review)", "command": "go test ./..."}, "check"),
		ifEq("g.exit", 0,
			[]Step{tool("work_review", map[string]any{"verdict": "changes_requested", "expectedHead": r.Head,
				"summary": "Validate now rejects expired tokens, but the refresh path still accepts a token at the exact expiry instant.",
				"findings": []map[string]any{{"severity": "blocking", "file": "session/refresh.go", "line": "{{g.line}}",
					"body":     "Refresh still accepts a token at the exact expiry instant: now.After(t.ExpiresAt) is false when now == ExpiresAt. Use the shared validator here and add a regression test for the boundary.",
					"evidence": "session/refresh.go:{{g.line}}: {{g.out}}"}},
				"message": "One change before I can approve: the refresh handler still accepts a token at the exact expiry instant. Please use the shared validator there and add a regression test."}, "")},
			[]Step{tool("work_review", map[string]any{"verdict": "approved", "expectedHead": r.Head, "resolve": resolve,
				"summary": "Checked the updated diff and the boundary tests: refresh now rejects the token at expiry through the shared validator, and normal refresh still works. go test ./... passes on this revision.",
				"message": "Checked the updated diff and the boundary test. That fixes it. Approved."}, "")}),
	}}
}

// ---- investigation: the Beacon request-flow scenario ----

func investigationScript(m *manifest.Manifest) Script {
	if m.Job.Repo != "beacon-gateway" {
		return limitation(m)
	}
	var answer string
	for _, in := range m.Inputs {
		if in.Kind == "answer" {
			answer = in.Body
		}
	}
	// New notes from the owner while the question is still open: take them
	// in quietly and keep waiting (no repeated update or second question).
	noted := false
	for _, in := range m.Inputs {
		noted = noted || in.Kind == "owner_input"
	}
	if answer == "" && noted && m.Purpose == "input" {
		return Script{Steps: []Step{
			status("Noted your update; still waiting on where the retry worker lives"),
			tool("work_wait", map[string]any{"reason": "missing_information", "detail": "Tracing the retry worker needs its repository location"}, ""),
		}}
	}
	if answer != "" {
		ans := answer
		if i := strings.Index(ans, "\nA: "); i >= 0 {
			ans = ans[i+4:]
		}
		return Script{Steps: []Step{
			status("Picking up the retry-worker trace with your answer"),
			shell("grep -rn 'RequestIDHeader\\|RequestID' --include=*.go gateway queue", "g"),
			{Write: &WriteFile{Path: "docs/request-flow.md", Content: flowDoc + "\n## Retry worker\n\nLocation (from the owner): " + strings.ReplaceAll(ans, "{", "(") +
				"\n\nThe worker re-sends failed deliveries. Because the gateway only reuses a request ID when the client supplies `X-Request-Id`, a retried request without that header gets a fresh ID and can be written twice. Idempotency depends on clients sending the header, or on the worker deduplicating by payload.\n"}},
			tool("artifact_publish", map[string]any{"path": "docs/request-flow.md", "name": "Beacon request flow", "kind": "document"}, "art"),
			tool("work_update", map[string]any{"state": "completed",
				"summary": "Mapped Beacon's request flow: the gateway reuses a client's X-Request-Id or mints a new one (gateway/handler.go), the queue producer publishes it (queue/producer.go), and the retry worker re-sends failures. A duplicate write can happen when a client retries without X-Request-Id, because the retry gets a fresh ID. Details and source locations are in the Beacon request flow document."}, ""),
		}}
	}
	return Script{Steps: []Step{
		status("Tracing the gateway"),
		shell("grep -rn 'RequestIDHeader\\|RequestID' --include=*.go gateway queue", "g"),
		shell("cat README.md docs/setup.md", "docs"),
		pause(),
		{Write: &WriteFile{Path: "docs/request-flow.md", Content: flowDoc + "\n## Retry worker\n\nNot yet located: it lives outside the linked repositories.\n"}},
		tool("artifact_publish", map[string]any{"path": "docs/request-flow.md", "name": "Beacon request flow (draft)", "kind": "document"}, "art"),
		tool("room_post", map[string]any{"body": "The gateway reuses a client's `X-Request-Id`, or mints one, before enqueueing (gateway/handler.go). I'm checking whether repeat requests keep that ID all the way through the queue."}, ""),
		tool("human_ask", map[string]any{
			"question":       "@" + m.OwnerHandle + " which repository contains Beacon's retry worker? I checked the linked repos and setup docs, but only found the gateway and queue producer. I'm continuing the gateway deduplication map meanwhile.",
			"missingFact":    "Where the Beacon retry-worker repository lives",
			"contextChecked": "The linked beacon-gateway repository, README.md, and docs/setup.md",
			"dependentStep":  "Trace how the retry worker re-sends requests",
			"continuingWith": "The gateway's duplicate-request analysis"}, "q"),
		pause(),
		status("Mapping duplicate-request handling in the gateway"),
		shell("grep -n 'newID\\|RequestIDHeader' gateway/handler.go", "dedupe"),
		tool("work_wait", map[string]any{"reason": "missing_information", "detail": "Tracing the retry worker needs its repository location"}, ""),
	}}
}

const flowDoc = `# Beacon request flow

Traced from the beacon-gateway source.

1. **Gateway** (` + "`gateway/handler.go`" + `): reads ` + "`X-Request-Id`" + ` (` + "`RequestIDHeader`" + `). If the client sent one it is reused; otherwise ` + "`newID()`" + ` mints a random ID.
2. **Queue producer** (` + "`queue/producer.go`" + `): ` + "`Publish`" + ` enqueues ` + "`Message{RequestID, Path}`" + ` on the ` + "`beacon.requests`" + ` topic.
3. **Retry worker**: re-sends failed deliveries (separate repository).

## Where a duplicate write can happen

A client that retries without ` + "`X-Request-Id`" + ` gets a new ID on each attempt, so downstream consumers can't tell the attempts apart.
`

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func short(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

var _ = say
