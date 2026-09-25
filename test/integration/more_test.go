package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

// sseEvents opens the event stream from cursor and collects committed events
// until n have arrived or the timeout passes.
func (e *env) sseEvents(cursor string, n int, timeout time.Duration) (ids []int64, types []string, control []string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.browser.URL+"/v1/events", nil)
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	resp, err := e.c.hc.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(resp.Body)
		var id int64
		var typ string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			case strings.HasPrefix(line, "event: "):
				typ = strings.TrimPrefix(line, "event: ")
			case line == "":
				if typ != "" {
					if id > 0 {
						ids, types = append(ids, id), append(types, typ)
					} else {
						control = append(control, typ)
					}
				}
				id, typ = 0, ""
				if len(ids) >= n {
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		resp.Body.Close()
		<-done
	}
	return
}

// A10: a client that disconnects resumes from its cursor without gaps; a
// cursor that no longer exists gets an explicit reset, never silent loss.
func TestEventReplayAfterDisconnect(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true})
	ids, _, control := e.sseEvents("", 3, 3*time.Second)
	if len(control) == 0 || control[0] != "ready" || len(ids) < 3 {
		t.Fatalf("initial stream: ids %v control %v", ids, control)
	}
	// The client's cursor is the snapshot position from bootstrap.
	var boot protocol.Bootstrap
	e.c.must("GET", "/v1/bootstrap", nil, &boot)
	cursor := boot.Cursor
	// While "disconnected", the owner posts from another tab.
	for i := 0; i < 3; i++ {
		e.post("Security", fmt.Sprintf("note %d", i), nil, nil)
	}
	ids2, types2, _ := e.sseEvents(strconv.FormatInt(cursor, 10), 3, 5*time.Second)
	if len(ids2) < 3 || ids2[0] != cursor+1 {
		t.Fatalf("replay should continue right after the cursor %d: %v", cursor, ids2)
	}
	for i := 1; i < len(ids2); i++ {
		if ids2[i] <= ids2[i-1] {
			t.Fatalf("replay out of order: %v", ids2)
		}
	}
	if types2[0] != "message.created" {
		t.Fatalf("first replayed event %q", types2[0])
	}
	_, _, control3 := e.sseEvents("999999999", 1, 2*time.Second)
	if len(control3) == 0 || control3[0] != "reset" {
		t.Fatalf("an unknown cursor must produce a reset: %v", control3)
	}
}

// A43: a document is reviewed without any forge: request, substantive
// feedback, revised artifact, approval of the new artifact.
func TestDocumentReviewWithoutForge(t *testing.T) {
	doc := func(v string) []fake.Step {
		return []fake.Step{{Write: &fake.WriteFile{Path: "docs/plan.md", Content: "# Plan\n\n" + v + "\n"}},
			toolStep("artifact_publish", map[string]any{"path": "docs/plan.md", "name": "Plan", "kind": "document"}, "")}
	}
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		switch {
		case replyTo(m, "Engineering", "plan"):
			return script(toolStep("work_create", map[string]any{"title": "Write the plan", "objective": "x", "kind": "document", "project": "Beacon", "repo": "beacon-gateway"}, ""))
		case m.Review != nil && m.Review.Round == 1:
			return script(toolStep("work_review", map[string]any{"verdict": "changes_requested", "expectedHead": "", "summary": "Missing rollback.",
				"findings": []map[string]any{{"severity": "blocking", "body": "The plan has no rollback step.", "evidence": "docs/plan.md has no rollback section"}},
				"message":  "The plan needs a rollback step."}, ""))
		case m.Review != nil:
			var resolve []string
			for _, f := range m.Review.Findings {
				resolve = append(resolve, f.ID)
			}
			return script(toolStep("work_review", map[string]any{"verdict": "approved", "expectedHead": "", "summary": "Rollback added.", "resolve": resolve,
				"message": "Approved."}, ""))
		case m.Job.Title == "Write the plan" && m.OwnReview != nil:
			var resp []map[string]any
			for _, f := range m.OwnReview.Findings {
				resp = append(resp, map[string]any{"findingId": f.ID, "body": "Added a rollback section."})
			}
			return script(append(doc("Deploy. Rollback: revert the release."),
				toolStep("work_respond_to_review", map[string]any{"responses": resp, "requestRereview": true, "message": "@mira added a rollback step."}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))...)
		case m.Job.Title == "Write the plan" && m.Purpose == "start":
			return script(append(doc("Deploy."),
				toolStep("work_request_review", map[string]any{"reviewer": "mira", "message": "@mira can you review the plan?"}, ""),
				toolStep("work_wait", map[string]any{"reason": "review"}, ""))...)
		}
		return nil
	}})
	e.post("Engineering", "@Pip write the plan", []string{"pip"}, nil)
	var d protocol.JobDetail
	e.waitFor("second round approved", 60*time.Second, func() bool {
		j, ok := e.job("Write the plan")
		if !ok {
			return false
		}
		d = e.jobDetail(j.ID)
		return len(d.Reviews) == 1 && len(d.Reviews[0].Rounds) == 2 && d.Reviews[0].Rounds[1].State == protocol.ReviewApproved
	})
	r1, r2 := d.Reviews[0].Rounds[0], d.Reviews[0].Rounds[1]
	if r1.Target.Kind != "artifact" || r1.Target.Hash == r2.Target.Hash || r1.State != protocol.ReviewChangesRequested {
		t.Fatalf("rounds must bind to distinct document versions: %+v / %+v", r1.Target, r2.Target)
	}
	if len(d.PullRequests) != 0 {
		t.Fatalf("no forge should be involved")
	}
}

// A02: a room linked to two projects never guesses the repository.
func TestScopeIsExplicitInMultiProjectRoom(t *testing.T) {
	e := newEnv(t, envOptions{})
	e.post("Engineering", "@Mira please fix the bug where things break", []string{"mira"}, nil)
	m := e.waitMessage("Engineering", "Which project should I change")
	if !strings.Contains(m.Body, "Atlas") || !strings.Contains(m.Body, "Beacon") {
		t.Fatalf("the question should name the candidate projects: %s", m.Body)
	}
	for _, j := range listAllJobs(e) {
		if j.Kind == protocol.JobKindCode {
			t.Fatalf("code work started without an explicit repository")
		}
	}
}

// §9 performance fixture: 10,000 messages across ten rooms and 100 jobs.
// Measures (and bounds) ordinary read latency; not an advertised capacity.
func TestLargeHistoryReadLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("large fixture")
	}
	if raceEnabled {
		t.Skip("latency targets don't apply under the race detector")
	}
	e := newEnv(t, envOptions{noRunner: true})
	var rooms []string
	for i := 0; i < 10; i++ {
		var r protocol.Room
		e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: fmt.Sprintf("Load %d", i), EngineerIDs: []string{e.engineerID("mira")},
			ProjectIDs: []string{e.project("Atlas").ID}}, &r)
		rooms = append(rooms, r.ID)
	}
	start := time.Now()
	words := []string{"session", "expiry", "gateway", "retry", "queue", "worker", "refresh", "token", "deploy", "review"}
	for i := 0; i < 10000; i++ {
		body := fmt.Sprintf("message %d about %s and %s", i, words[i%10], words[(i/10)%10])
		if _, err := e.hub.PostMessage(e.ctx, e.c.boot.User.ID, rooms[i%10], protocol.PostMessageRequest{Body: body, ClientKey: domain.NewID()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 100; i++ {
		e.post("Security", fmt.Sprintf("@Mira task %d", i), []string{"mira"}, nil)
	}
	t.Logf("seeded 10,100 messages and 100 jobs in %s", time.Since(start).Round(time.Millisecond))
	measure := func(name, path string) {
		var ds []time.Duration
		for i := 0; i < 40; i++ {
			t0 := time.Now()
			e.c.must("GET", path, nil, nil)
			ds = append(ds, time.Since(t0))
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		p95 := ds[len(ds)*95/100]
		t.Logf("%-22s p50 %6s  p95 %6s", name, ds[len(ds)/2].Round(time.Microsecond), p95.Round(time.Microsecond))
		if p95 > 200*time.Millisecond {
			t.Errorf("%s p95 %s exceeds the 200ms target", name, p95)
		}
	}
	measure("bootstrap", "/v1/bootstrap")
	measure("room page", "/v1/rooms/"+rooms[3]+"/messages?limit=60")
	measure("older page", "/v1/rooms/"+rooms[3]+"/messages?limit=60&before=500")
	measure("search", "/v1/search?q=gateway+retry")
	measure("jobs", "/v1/jobs?limit=200")
	measure("overview", "/v1/overview")
}
