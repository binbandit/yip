package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

// A team can ask several independent questions in one discussion. Answering
// the second question must never attach its answer to the first one.
func TestSimulationThreadedQuestionsKeepExplicitTargets(t *testing.T) {
	for _, answerVia := range []string{"question endpoint", "reply target"} {
		t.Run(answerVia, func(t *testing.T) {
			e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
				switch {
				case replyTo(m, "Engineering", "regional rollout"):
					return script(toolStep("work_create", map[string]any{"title": "Regional rollout decisions", "objective": "Identify region and maintenance window", "kind": "investigation", "project": "Beacon"}, ""))
				case m.Job.Title == "Regional rollout decisions" && m.Purpose == "start":
					return script(
						toolStep("human_ask", map[string]any{"question": "Which region goes first?", "missingFact": "initial region", "contextChecked": "Rollout notes"}, ""),
						toolStep("human_ask", map[string]any{"question": "What is the maintenance window?", "missingFact": "maintenance window", "contextChecked": "Release calendar"}, ""),
						toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
				case m.Job.Title == "Regional rollout decisions":
					return script(fake.Step{Final: "The rollout decisions are available."})
				}
				return nil
			}})
			thread := e.post("Engineering", "Coordinate the staged rollout here.", nil, nil)
			e.post("Engineering", "@Pip investigate regional rollout", []string{"pip"}, func(r *protocol.PostMessageRequest) { r.ThreadID = thread.Message.ID })
			job := e.waitJob("Regional rollout decisions", protocol.JobWaiting)
			var first, second protocol.Question
			e.waitFor("two questions awaiting answers", 15*time.Second, func() bool {
				for _, q := range e.jobDetail(job.ID).Questions {
					switch q.MissingFact {
					case "initial region":
						first = q
					case "maintenance window":
						second = q
					}
				}
				return first.ID != "" && second.ID != ""
			})
			ambiguous := e.post("Engineering", "I will check the release calendar.", nil, func(r *protocol.PostMessageRequest) { r.ThreadID = thread.Message.ID })
			if len(ambiguous.Resolved) != 0 {
				t.Fatalf("a reply in a discussion with two questions guessed an answer target: %v", ambiguous.Resolved)
			}
			var answer protocol.PostMessageResponse
			if answerVia == "question endpoint" {
				e.c.must("POST", "/v1/questions/"+second.ID+"/answer", protocol.AnswerQuestionRequest{Body: "Sunday 02:00 UTC", ClientKey: "maintenance-answer"}, &answer)
			} else {
				answer = e.post("Engineering", "Sunday 02:00 UTC", nil, func(r *protocol.PostMessageRequest) {
					r.ThreadID = thread.Message.ID
					r.ReplyToID = second.MessageID
				})
			}
			if len(answer.Resolved) != 1 || answer.Resolved[0] != second.ID {
				t.Fatalf("the maintenance-window answer resolved %v; wanted only %s (region question %s must remain open)", answer.Resolved, second.ID, first.ID)
			}
			for _, q := range e.jobDetail(job.ID).Questions {
				if q.ID == first.ID && q.Status != "open" {
					t.Fatalf("the region question consumed the maintenance answer: %+v", q)
				}
			}
			var duplicate protocol.PostMessageResponse
			e.c.must("POST", "/v1/questions/"+second.ID+"/answer", protocol.AnswerQuestionRequest{Body: "Confirming Sunday 02:00 UTC", ClientKey: "maintenance-answer-again"}, &duplicate)
			if len(duplicate.Resolved) != 0 {
				t.Fatalf("a second reply to an answered question consumed an unrelated open question: %v", duplicate.Resolved)
			}
		})
	}
}

// Revoking a reviewer's access must also revoke the runner's ability to
// download that review document through its authenticated artifact endpoint.
func TestSimulationReviewDownloadRechecksRevokedAccess(t *testing.T) {
	for _, revoked := range []string{"project", "conversation"} {
		t.Run(revoked, func(t *testing.T) {
			e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
				switch {
				case replyTo(m, "Restricted rollout", "confidential plan"):
					return script(toolStep("work_create", map[string]any{"title": "Confidential rollout plan", "objective": "Write an internal plan", "kind": "document", "project": "Beacon", "repo": "beacon-gateway"}, ""))
				case m.Review != nil:
					return script(fake.Step{Status: "Reviewing the confidential plan"}, fake.Step{Fault: "hang"})
				case m.Job.Title == "Confidential rollout plan":
					return script(fake.Step{Write: &fake.WriteFile{Path: "docs/internal.md", Content: "CONFIDENTIAL-SIMULATION-CANARY\n"}},
						toolStep("artifact_publish", map[string]any{"path": "docs/internal.md", "name": "Internal plan", "kind": "document"}, ""),
						toolStep("work_request_review", map[string]any{"reviewer": "mira", "message": "Please review the internal plan."}, ""),
						toolStep("work_wait", map[string]any{"reason": "review"}, ""))
				}
				return nil
			}})
			var room protocol.Room
			e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Restricted rollout", Private: true,
				EngineerIDs: []string{e.engineerID("pip"), e.engineerID("mira")}, ProjectIDs: []string{e.project("Beacon").ID}}, &room)
			e.post("Restricted rollout", "@Pip write the confidential plan", []string{"pip"}, nil)
			job := e.waitJob("Confidential rollout plan", protocol.JobReviewReady, protocol.JobWaiting)
			var artifact string
			e.waitFor("the reviewer to hold the document", 15*time.Second, func() bool {
				d := e.jobDetail(job.ID)
				if len(d.Reviews) == 0 || len(d.Reviews[0].Rounds) == 0 {
					return false
				}
				round := d.Reviews[0].Rounds[0]
				artifact = round.Target.ArtifactID
				for _, run := range e.jobDetail(round.ReviewJobID).Runs {
					if run.State == protocol.RunRunning {
						return artifact != ""
					}
				}
				return false
			})
			read := func(name string) []byte {
				data, err := os.ReadFile(filepath.Join(e.dir, "runner", name))
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			tlsConfig, err := auth.ClientTLS(read("hub-ca.pem"), read("node.pem"), read("node.key"), "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{TLSClientConfig: tlsConfig}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			fetch := func() (int, string) {
				resp, err := client.Get(e.runnerURL + "/v1/runner/artifacts/" + artifact)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				return resp.StatusCode, string(body)
			}
			if status, body := fetch(); status != http.StatusOK || !strings.Contains(body, "CONFIDENTIAL-SIMULATION-CANARY") {
				t.Fatalf("authorized review download failed: %d %s", status, body)
			}
			if revoked == "project" {
				e.c.must("PUT", "/v1/projects/"+e.project("Beacon").ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "none"}, nil)
			} else {
				e.c.must("DELETE", "/v1/rooms/"+room.ID+"/members/"+e.engineerID("mira"), nil, nil)
			}
			if status, body := fetch(); status == http.StatusOK || strings.Contains(body, "CONFIDENTIAL-SIMULATION-CANARY") {
				t.Fatalf("document remains downloadable after %s access was removed: HTTP %d %q", revoked, status, body)
			}
		})
	}
}

// A provider can crash after permission is delivered but before reporting
// whether a push happened. Recovery must keep that uncertainty out of the
// automatic retry path, including after the hub restarts.
func TestSimulationCrashAfterPushPermissionNeedsExplicitRetry(t *testing.T) {
	for _, decision := range []string{"grant", "approve", "reject", "routine check"} {
		t.Run(decision, func(t *testing.T) {
			e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
				switch {
				case replyTo(m, "Security", "release branch"):
					return script(toolStep("work_create", map[string]any{"title": "Publish release branch", "objective": "Push the approved release branch", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
				case m.Job.Title == "Publish release branch":
					command := "git push origin HEAD"
					if decision == "routine check" {
						command = "git status --short"
					}
					return script(fake.Step{Approval: &protocol.ApprovalAction{Kind: "exec", Command: command, Summary: "Check or push the release branch"}, Save: "push"},
						toolStep("room_post", map[string]any{"body": "Push permission: {{push.allowed}}"}, ""), fake.Step{Fault: "crash"})
				}
				return nil
			}})
			if decision == "grant" {
				e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "write", Actions: []string{"push"}}, nil)
			}
			e.post("Security", "@Mira publish the release branch", []string{"mira"}, nil)
			if decision == "approve" || decision == "reject" {
				var approval protocol.Approval
				e.waitFor("push approval", 15*time.Second, func() bool {
					job, ok := e.job("Publish release branch")
					if !ok {
						return false
					}
					for _, a := range e.jobDetail(job.ID).Approvals {
						if a.Status == "pending" {
							approval = a
							return true
						}
					}
					return false
				})
				e.c.must("POST", "/v1/approvals/"+approval.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: decision, Version: approval.Version}, nil)
			}
			permission := e.waitMessage("Security", "Push permission:")
			if strings.Contains(permission.Body, "true") != (decision != "reject") {
				t.Fatalf("unexpected push permission for %s: %s", decision, permission.Body)
			}
			var job protocol.Job
			e.waitFor("provider crash recorded", 15*time.Second, func() bool {
				job, _ = e.job("Publish release branch")
				d := e.jobDetail(job.ID)
				return len(d.Runs) == 1 && d.Runs[0].State == protocol.RunFailed
			})
			checkRecovery := func() {
				t.Helper()
				d := e.jobDetail(job.ID)
				if decision == "reject" || decision == "routine check" {
					if d.Job.State != protocol.JobWaiting || d.Job.WaitingReason != protocol.WaitRecovery {
						t.Fatalf("a crash with denied external access should remain safe to retry: %+v", d.Job)
					}
				} else if d.Job.State != protocol.JobFailed {
					t.Fatalf("a possibly executed push must require an explicit retry after %s: state=%s detail=%s", decision, d.Job.State, d.Job.StateDetail)
				}
				if len(d.Runs) != 1 {
					t.Fatalf("recovery replayed a potentially executed push: %d attempts", len(d.Runs))
				}
			}
			checkRecovery()
			e.restartHub()
			checkRecovery()
		})
	}
}
