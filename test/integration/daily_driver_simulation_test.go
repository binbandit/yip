package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// A push grant belongs to the assignment's repository. Ask the actual hub for
// permission without ever executing a push, including in the allowed control.
func TestSimulationPushGrantStaysInsideAssignedCheckout(t *testing.T) {
	for _, scenario := range []struct {
		name, command string
		autoAllow     bool
	}{
		{"assigned checkout", "git -C . push origin HEAD", true},
		{"assigned git directory", "git --git-dir=.git push origin HEAD", true},
		{"assigned github repository", "gh pr merge 42 --squash", true},
		{"another checkout", "git -C /outside push origin HEAD", false},
		{"another git directory", "git --git-dir=/tmp/other.git push origin HEAD", false},
		{"environment git directory", "GIT_DIR=/outside/.git git push origin HEAD", false},
		{"wrapped environment git directory", "env GIT_DIR=/outside/.git git push origin HEAD", false},
		{"environment working directory", "env -C /outside git push origin HEAD", false},
		{"compound working directory", "cd /outside && git push origin HEAD", false},
		{"nested repository", "cd nested-repo && git push origin HEAD", false},
		{"symlink-sensitive directory", "git -C link/.. push origin HEAD", false},
		{"restored working directory", "popd && git push origin HEAD", false},
		{"find working directory", "find /outside -execdir git push origin HEAD \\;", false},
		{"exported github repository", "export GH_REPO=another-owner/other-repo; gh pr merge 42 --squash", false},
		{"remote override", "git -c remote.origin.pushurl=https://github.com/another-owner/other-repo push origin HEAD", false},
		{"another remote", "git push https://github.com/another-owner/other-repo HEAD", false},
		{"another github repository", "gh -R another-owner/other-repo pr merge 42 --squash", false},
		{"environment github repository", "env GH_REPO=another-owner/other-repo gh pr create --fill", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
				switch {
				case replyTo(m, "Security", "repository permission"):
					return script(toolStep("work_create", map[string]any{"title": "Check repository permission", "objective": "Verify the assignment's push authority", "kind": "code", "project": "Atlas", "repo": "atlas"}, ""))
				case m.Job.Title == "Check repository permission":
					return script(fake.Step{Approval: &protocol.ApprovalAction{Kind: "exec", Command: scenario.command, Summary: "Push from the selected checkout"}, Save: "permission"},
						toolStep("room_post", map[string]any{"body": "Repository permission: {{permission.allowed}}"}, ""),
						toolStep("work_update", map[string]any{"state": "failed", "summary": "Permission simulation finished without executing the command"}, ""))
				}
				return nil
			}})
			e.c.must("PUT", "/v1/projects/"+e.project("Atlas").ID+"/grants/"+e.engineerID("mira"), protocol.PutGrantRequest{Access: "write", Actions: []string{"push", "merge", "open_pr"}}, nil)
			e.post("Security", "@Mira check repository permission", []string{"mira"}, nil)
			var pending protocol.Approval
			var allowed int
			e.waitFor("the repository permission decision", 15*time.Second, func() bool {
				job, ok := e.job("Check repository permission")
				if !ok {
					return false
				}
				for _, approval := range e.jobDetail(job.ID).Approvals {
					if approval.Status == "pending" {
						pending = approval
						return true
					}
				}
				var decisions int
				err := e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*), COALESCE(SUM(json_extract(payload, '$.decision') = 'allow'), 0)
					FROM events WHERE job_id = ? AND type = 'permission.auto'`, job.ID).Scan(&decisions, &allowed)
				return err == nil && decisions > 0
			})
			if (allowed > 0) != scenario.autoAllow {
				t.Fatalf("project push grant automatically allowed %q: %v; wanted %v", scenario.command, allowed > 0, scenario.autoAllow)
			}
			if pending.ID != "" {
				e.c.must("POST", "/v1/approvals/"+pending.ID+"/decision", protocol.ApprovalDecisionRequest{Decision: "reject", Version: pending.Version}, nil)
			}
			permission := e.waitMessage("Security", "Repository permission:")
			if strings.Contains(permission.Body, "true") != scenario.autoAllow {
				t.Fatalf("provider received the wrong repository permission: %s", permission.Body)
			}
		})
	}
}

// One workspace carries an entire working day into the next. Every chapter
// uses the same database, repositories, engineers, history and runner identity.
func TestSimulationDailyDriverPersistentWorkspace(t *testing.T) {
	type assignment struct {
		id, title, kind, room, author string
		question, cancel, rework      bool
	}
	specs := []assignment{
		{"01", "Fix Atlas session expiry", "code", "Security", "mira", false, false, false},
		{"02", "Daily gateway retry runbook", "document", "Daily Beacon", "pip", false, false, false},
		{"03", "Daily support label", "code", "Security", "mira", false, false, false},
		{"04", "Daily rollout window", "document", "Daily Beacon", "pip", true, false, false},
		{"05", "Daily cancelled experiment", "code", "Security", "mira", false, true, false},
		{"06", "Daily maintenance label", "code", "Security", "mira", false, false, false},
		{"07", "Daily incident handover", "document", "Daily Beacon", "pip", false, false, false},
		{"08", "Daily deployment label", "code", "Security", "mira", false, false, false},
		{"09", "Daily gateway ownership", "document", "Daily Beacon", "pip", false, false, false},
		{"10", "Daily rollback plan", "document", "Daily Beacon", "pip", false, false, true},
		{"11", "Daily recovery label", "code", "Security", "mira", false, false, false},
		{"12", "Daily release handover", "document", "Daily Beacon", "pip", false, false, false},
	}
	var recalled sync.Map
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.Job.Kind == "reply" && m.Request != nil && strings.HasPrefix(m.Request.Body, "Daily recall ") {
			recalled.Store(m.Request.Body, m.Prompt())
			return script(fake.Step{Final: "The earlier work records are available."})
		}
		for _, spec := range specs[1:] {
			if m.Job.Kind == "reply" && m.Request != nil && m.Request.Body == "Daily task "+spec.id {
				project, repo := "Atlas", "atlas"
				if spec.kind == "document" {
					project, repo = "Beacon", "beacon-gateway"
				}
				return script(toolStep("work_create", map[string]any{"title": spec.title, "objective": spec.title, "kind": spec.kind, "project": project, "repo": repo}, ""))
			}
			if m.Review != nil && strings.Contains(m.Job.Title, spec.title) {
				args := map[string]any{"verdict": "approved", "summary": "Verified the supplied result", "expectedHead": m.Review.Head, "expectedHash": m.Review.Hash}
				if spec.rework && m.Review.Round == 1 {
					args["verdict"], args["summary"] = "changes_requested", "The plan needs a rollback procedure"
					args["findings"] = []map[string]any{{"severity": "blocking", "body": "Add a rollback procedure", "evidence": "The published plan contains no rollback section"}}
				}
				var resolve []string
				for _, finding := range m.Review.Findings {
					resolve = append(resolve, finding.ID)
				}
				args["resolve"] = resolve
				if spec.kind == "code" {
					return script(toolStep("work_run_check", map[string]any{"command": "go test ./...", "name": "Independent revision tests"}, "check"),
						fake.Step{If: &fake.Cond{Var: "check.passed", Eq: true}, Then: []fake.Step{toolStep("work_review", args, "")},
							Else: []fake.Step{toolStep("work_review", map[string]any{"verdict": "changes_requested", "expectedHead": m.Review.Head, "summary": "Revision checks failed"}, "")}})
				}
				contains := spec.title
				if spec.rework && m.Review.Round > 1 {
					contains = "Rollback: revert the release."
				}
				return script(fake.Step{Shell: "cat review-artifact", Save: "document"},
					fake.Step{If: &fake.Cond{Var: "document.out", Contains: contains}, Then: []fake.Step{toolStep("work_review", args, "")},
						Else: []fake.Step{toolStep("work_review", map[string]any{"verdict": "unable_to_review", "expectedHash": m.Review.Hash, "summary": "The exact document content was not available"}, "")}})
			}
			if m.Job.Title != spec.title {
				continue
			}
			if spec.cancel {
				return script(fake.Step{Status: "Investigating the optional experiment"}, fake.Step{Fault: "hang"})
			}
			var steps []fake.Step
			if spec.kind == "code" {
				steps = append(steps,
					fake.Step{Write: &fake.WriteFile{Path: "daily/label" + spec.id + ".go", Content: fmt.Sprintf("package daily\n\nfunc Label%s() string { return %q }\n", spec.id, spec.title)}},
					fake.Step{Write: &fake.WriteFile{Path: "daily/label" + spec.id + "_test.go", Content: fmt.Sprintf("package daily\n\nimport \"testing\"\n\nfunc TestLabel%s(t *testing.T) { if got := Label%s(); got != %q { t.Fatalf(\"unexpected label: %%s\", got) } }\n", spec.id, spec.id, spec.title)}},
					toolStep("work_publish_revision", map[string]any{"summary": spec.title}, ""),
					toolStep("work_run_check", map[string]any{"command": "go test ./...", "name": "Published revision tests"}, ""))
			} else {
				content := "# " + spec.title + "\n\nThe gateway retries requests using X-Request-Id; the separately deployed worker requires its own release coordination.\n"
				if spec.question {
					answer, clarification := "", ""
					for _, input := range m.Inputs {
						if input.Kind == "answer" {
							answer = input.Body
						} else if input.Kind == "owner_input" {
							clarification += input.Body
						}
					}
					if answer == "" {
						return script(toolStep("human_ask", map[string]any{"question": "Which rollout window should the plan use?", "missingFact": "rollout window", "contextChecked": "Gateway documentation and prior release notes"}, ""),
							toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
					}
					content += "\nWindow: " + answer + "\nConstraint: " + clarification + "\n"
				}
				if spec.rework && m.OwnReview != nil {
					content += "\nRollback: revert the release.\n"
				}
				steps = append(steps, fake.Step{Write: &fake.WriteFile{Path: "docs/daily-" + spec.id + ".md", Content: content}},
					toolStep("artifact_publish", map[string]any{"path": "docs/daily-" + spec.id + ".md", "name": spec.title, "kind": "document"}, ""))
			}
			if spec.rework && m.OwnReview != nil {
				var responses []map[string]any
				for _, finding := range m.OwnReview.Findings {
					responses = append(responses, map[string]any{"findingId": finding.ID, "body": "Added the rollback procedure"})
				}
				steps = append(steps, toolStep("work_respond_to_review", map[string]any{"responses": responses, "requestRereview": true}, ""))
			} else {
				steps = append(steps, toolStep("work_request_review", map[string]any{"reviewer": "oren", "message": "Please verify this result."}, ""))
			}
			steps = append(steps, toolStep("work_update", map[string]any{"state": "completed", "summary": spec.title + " is ready with verified evidence."}, ""))
			return script(steps...)
		}
		return nil
	}})
	var room protocol.Room
	e.c.must("POST", "/v1/rooms", protocol.CreateRoomRequest{Name: "Daily Beacon", ReplyMode: protocol.ReplyModeQuiet,
		EngineerIDs: []string{e.engineerID("pip"), e.engineerID("oren")}, ProjectIDs: []string{e.project("Beacon").ID}}, &room)
	beacon := e.project("Beacon")
	policy := beacon.Policy
	policy.RequirePeerReview = true
	e.c.must("PATCH", "/v1/projects/"+beacon.ID, protocol.UpdateProjectRequest{Version: beacon.Version, Policy: &policy}, nil)
	e.c.must("PUT", "/v1/projects/"+beacon.ID+"/grants/"+e.engineerID("oren"), protocol.PutGrantRequest{Access: "read"}, nil)
	var profiles []protocol.ProviderProfile
	e.c.must("GET", "/v1/provider-profiles", nil, &profiles)
	for _, profile := range profiles {
		if profile.Provider == "fake" {
			e.c.must("PUT", "/v1/provider-profiles/"+profile.ID, protocol.ProviderProfileRequest{MaxConcurrency: 1}, nil)
		}
	}
	jobs := make(map[string]protocol.Job)
	post := func(id string) {
		for _, spec := range specs {
			if spec.id != id {
				continue
			}
			thread := e.post(spec.room, "Assignment discussion "+id, nil, nil)
			e.post(spec.room, "Daily task "+id, []string{spec.author}, func(r *protocol.PostMessageRequest) { r.ThreadID = thread.Message.ID })
			return
		}
		t.Fatalf("unknown daily assignment %s", id)
	}
	complete := func(ids ...string) {
		for _, id := range ids {
			for _, spec := range specs {
				if spec.id == id {
					jobs[id] = e.waitJob(spec.title, protocol.JobCompleted)
					detail := e.jobDetail(jobs[id].ID)
					if len(detail.Missing) != 0 || len(detail.Reviews) != 1 || detail.Reviews[0].State != protocol.ReviewApproved {
						e.t.Fatalf("daily assignment %s lacks its reviewed completion evidence: %+v", id, detail.Missing)
					}
				}
			}
		}
	}
	idle := func() {
		e.waitFor("all daily attempts to settle", 30*time.Second, func() bool {
			runs, err := store.RunsInStates(e.ctx, e.hub.Store().R(), protocol.RunCreated, protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning, protocol.RunAwaitingInput, protocol.RunStopping, protocol.RunUnknown)
			return err == nil && len(runs) == 0
		})
	}
	phase := func(name string, run func(*testing.T)) bool {
		ok := t.Run(name, func(pt *testing.T) { e.t, e.c.t = pt, pt; run(pt) })
		e.t, e.c.t = t, t
		return ok
	}
	if !phase("morning_build_review_and_follow_on_work", func(t *testing.T) {
		e.post("Security", "@Mira fix Atlas accepting expired sessions", []string{"mira"}, nil)
		complete("01")
		// The owner lands the reviewed patch into this disposable project's
		// real remote, so later work starts from the accumulated code history.
		remote := filepath.Join(e.dir, "fixtures", "atlas.git")
		landed := false
		for _, artifact := range e.jobDetail(jobs["01"].ID).Artifacts {
			if artifact.Kind == "bundle" && artifact.Revision == jobs["01"].Revision.Head {
				path, err := e.hub.Artifacts().Path(artifact.Hash)
				if err != nil {
					t.Fatal(err)
				}
				gitOut(t, remote, "fetch", "-q", path, jobs["01"].Revision.Head+":refs/heads/main")
				landed = true
				break
			}
		}
		if !landed || strings.TrimSpace(gitOut(t, remote, "rev-parse", "main")) != jobs["01"].Revision.Head {
			t.Fatal("the first completed revision was not landed into the shared fixture remote")
		}
		post("02")
		post("03")
		complete("02", "03")
		descendant := false
		for _, artifact := range e.jobDetail(jobs["03"].ID).Artifacts {
			if artifact.Kind == "bundle" && artifact.Revision == jobs["03"].Revision.Head {
				path, err := e.hub.Artifacts().Path(artifact.Hash)
				if err != nil {
					t.Fatal(err)
				}
				gitOut(t, remote, "fetch", "-q", path, jobs["03"].Revision.Head)
				gitOut(t, remote, "merge-base", "--is-ancestor", jobs["01"].Revision.Head, jobs["03"].Revision.Head)
				descendant = true
				break
			}
		}
		if !descendant {
			t.Fatal("the follow-on assignment did not publish a verifiable descendant of the landed revision")
		}
		idle()
	}) {
		return
	}
	var question protocol.Question
	if !phase("questions_and_cancellation_do_not_block_other_work", func(t *testing.T) {
		post("04")
		jobs["04"] = e.waitJob(specs[3].title, protocol.JobWaiting)
		questions := e.jobDetail(jobs["04"].ID).Questions
		if len(questions) != 1 {
			t.Fatalf("expected one rollout question, got %d", len(questions))
		}
		question = questions[0]
		post("05")
		jobs["05"] = e.waitJob(specs[4].title, protocol.JobRunning)
		post("06")
		e.c.must("POST", "/v1/jobs/"+jobs["05"].ID+"/cancel", protocol.CancelJobRequest{Reason: "Drop this experiment; continue the maintenance work"}, nil)
		complete("06")
		idle()
		if e.jobDetail(jobs["04"].ID).Questions[0].Status != "open" {
			t.Fatal("independent work consumed the rollout question")
		}
		if e.jobDetail(jobs["05"].ID).Job.State != protocol.JobCancelled {
			t.Fatal("the cancelled experiment restarted")
		}
	}) {
		return
	}
	if !phase("offline_inputs_and_answers_survive_full_restart", func(t *testing.T) {
		e.killRunner()
		var input protocol.JobInputResponse
		e.c.must("POST", "/v1/jobs/"+jobs["04"].ID+"/input", protocol.JobInputRequest{Body: "Keep the existing gateway retry contract", ClientKey: "daily-clarification"}, &input)
		answer := protocol.AnswerQuestionRequest{Body: "Sunday 02:00 UTC", ClientKey: "daily-answer"}
		var first, duplicate protocol.PostMessageResponse
		e.c.must("POST", "/v1/questions/"+question.ID+"/answer", answer, &first)
		e.c.must("POST", "/v1/questions/"+question.ID+"/answer", answer, &duplicate)
		if len(first.Resolved) != 1 || !duplicate.Duplicate || len(duplicate.Resolved) != 0 {
			t.Fatalf("the offline answer did not resolve once: %+v / %+v", first, duplicate)
		}
		post("07")
		post("08")
		post("09")
		e.restartHub()
		e.startRunner()
		complete("04", "07", "08", "09")
		idle()
		detail := e.jobDetail(jobs["04"].ID)
		if len(detail.Questions) != 1 || detail.Questions[0].AnswerMessageID != first.Message.ID || len(detail.Inputs) != 1 {
			t.Fatal("restart duplicated or lost the question or queued clarification")
		}
		found := false
		for _, artifact := range detail.Artifacts {
			if artifact.Kind != "document" {
				continue
			}
			file, err := e.hub.Artifacts().Open(artifact.Hash)
			if err != nil {
				t.Fatal(err)
			}
			var content strings.Builder
			_, err = io.Copy(&content, file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			found = strings.Contains(content.String(), answer.Body) && strings.Contains(content.String(), "Keep the existing gateway retry contract")
		}
		if !found {
			t.Fatal("the reviewed document omitted an input queued before restart")
		}
		attempts := len(detail.Runs)
		var replayedInput protocol.JobInputResponse
		e.c.must("POST", "/v1/jobs/"+jobs["04"].ID+"/input", protocol.JobInputRequest{Body: "Keep the existing gateway retry contract", ClientKey: "daily-clarification"}, &replayedInput)
		e.c.must("POST", "/v1/questions/"+question.ID+"/answer", answer, &duplicate)
		if replayedInput.Input.ID != input.Input.ID || !duplicate.Duplicate || duplicate.Message.ID != first.Message.ID || len(e.jobDetail(jobs["04"].ID).Runs) != attempts {
			t.Fatal("client retries after restart replayed completed work instead of its persisted receipts")
		}
	}) {
		return
	}
	phase("next_day_revision_memory_and_restorable_snapshot", func(t *testing.T) {
		post("10")
		post("11")
		post("12")
		complete("10", "11", "12")
		idle()
		rework := e.jobDetail(jobs["10"].ID).Reviews[0].Rounds
		if len(rework) != 2 || rework[0].Target.Hash == rework[1].Target.Hash || rework[0].Findings[0].Status != "resolved" {
			t.Fatal("the corrected daily plan was not reviewed as a new immutable result")
		}
		e.killRunner()
		e.restartHub()
		e.startRunner()
		var reviewRecords []protocol.EngineerNote
		e.c.must("GET", "/v1/engineers/"+e.engineerID("oren")+"/notes", nil, &reviewRecords)
		for _, spec := range specs {
			if spec.cancel {
				continue
			}
			detail := e.jobDetail(jobs[spec.id].ID)
			if detail.Job.State != protocol.JobCompleted || len(detail.Reviews) != 1 || len(detail.Missing) != 0 {
				t.Fatalf("restart lost completed assignment %s", spec.id)
			}
			var notes []protocol.EngineerNote
			e.c.must("GET", "/v1/engineers/"+e.engineerID(spec.author)+"/notes", nil, &notes)
			found := false
			for _, note := range notes {
				found = found || (note.Kind == "record" && note.Status == "accepted" && strings.Contains(note.Body, spec.title))
			}
			if !found {
				t.Fatalf("restart lost the author's work record for %s", spec.id)
			}
			found = false
			for _, note := range reviewRecords {
				found = found || (note.Kind == "record" && note.Status == "accepted" && strings.Contains(note.Body, spec.title))
			}
			if !found {
				t.Fatalf("restart lost the reviewer's work record for %s", spec.id)
			}
		}
		body := "Daily recall the earlier Atlas session work"
		e.post("Engineering", body, []string{"mira"}, nil)
		e.waitFor("cross-room recall after two restarts", 15*time.Second, func() bool { _, ok := recalled.Load(body); return ok })
		prompt, _ := recalled.Load(body)
		if !strings.Contains(prompt.(string), specs[0].title) || !strings.Contains(prompt.(string), jobs["01"].Revision.Head[:8]) {
			t.Fatal("persisted completion history did not reach the next day's context")
		}
		idle()
		roots := 0
		for _, job := range e.jobsWithReplies() {
			if job.Kind != protocol.JobKindReply && job.Kind != protocol.JobKindReview && job.ParentID == "" {
				roots++
			}
		}
		if roots != 12 {
			t.Fatalf("twelve assignments became %d root work items", roots)
		}
		backupDir := filepath.Join(e.dir, "daily-backup")
		backupPath := filepath.Join(backupDir, "hub.db")
		if err := e.hub.Store().Backup(e.ctx, backupPath); err != nil {
			t.Fatal(err)
		}
		restored, err := store.Open(e.ctx, backupPath)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		if err := store.IntegrityCheck(e.ctx, restored.R()); err != nil {
			t.Fatal(err)
		}
		artifacts, err := store.ListArtifacts(e.ctx, restored.R())
		if err != nil {
			t.Fatal(err)
		}
		restoredArtifacts, err := hub.NewArtifactStore(filepath.Join(backupDir, "artifacts"))
		if err != nil {
			t.Fatal(err)
		}
		for _, artifact := range artifacts {
			file, err := e.hub.Artifacts().Open(artifact.Hash)
			if err != nil {
				t.Fatal(err)
			}
			err = restoredArtifacts.Put(file, artifact.Hash, artifact.Size)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := restoredArtifacts.Verify(artifact.Hash); err != nil {
				t.Fatal(err)
			}
		}
		for id, original := range jobs {
			job, err := store.GetJob(e.ctx, restored.R(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := protocol.JobCompleted
			if id == "05" {
				want = protocol.JobCancelled
			}
			if job.State != want {
				t.Fatalf("snapshot restored assignment %s as %s instead of %s", id, job.State, want)
			}
		}
		t.Logf("Persistent workspace: 12 assignments, 11 completed, 1 cancelled, 2 hub+runner restarts, %d artifact records restored and hash verified", len(artifacts))
	})
}
