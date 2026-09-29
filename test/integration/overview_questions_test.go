package integration

import (
	"encoding/json"
	"testing"
	"time"

	manifest "github.com/binbandit/yip/internal/context"
	"github.com/binbandit/yip/internal/providers/fake"
	"github.com/binbandit/yip/protocol"
)

func TestOverviewReviewerQuestionStaysWithAssignment(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{director: func(m *manifest.Manifest) json.RawMessage {
		if m.Review == nil || m.Review.Round != 1 {
			return nil
		}
		if m.Purpose == "review" {
			return script(
				toolStep("human_ask", map[string]any{"question": "Which client release is this review for?", "missingFact": "client release", "contextChecked": "Repository contract and change request"}, ""),
				toolStep("work_wait", map[string]any{"reason": "missing_information"}, ""))
		}
		var resumed fake.Script
		if err := json.Unmarshal(fake.Direct(m), &resumed); err != nil {
			panic(err)
		}
		resumed.Steps = append([]fake.Step{{Sleep: "3s"}}, resumed.Steps...)
		return script(resumed.Steps...)
	}})
	e.post("Security", "@Mira fix Atlas accepting expired sessions", []string{"mira"}, nil)
	var question protocol.Question
	var root protocol.Job
	e.waitFor("reviewer's question", 60*time.Second, func() bool {
		for _, job := range e.jobsWithReplies() {
			if job.Title == "Fix Atlas session expiry" {
				root = job
			}
			if job.Kind == protocol.JobKindReview {
				for _, q := range e.jobDetail(job.ID).Questions {
					if q.Status == "open" {
						question = q
					}
				}
			}
		}
		return root.ID != "" && question.ID != ""
	})
	if question.JobID == root.ID || question.AskerID != e.engineerID("oren") {
		t.Fatalf("expected a reviewer's own question: %+v", question)
	}
	var overview protocol.Overview
	e.c.must("GET", "/v1/overview", nil, &overview)
	var rows []protocol.WorkRow
	for _, row := range overview.Work {
		if row.Job.ID == root.ID {
			rows = append(rows, row)
		}
		if row.Job.ID == question.JobID {
			t.Fatal("review child became a duplicate assignment row")
		}
	}
	if len(rows) != 1 || len(rows[0].Questions) != 1 || rows[0].Questions[0].ID != question.ID || rows[0].Questions[0].MessageID != question.MessageID {
		t.Fatalf("reviewer's actionable question disappeared from its assignment: %+v", rows)
	}
	var asked int
	for _, message := range e.messages("Security") {
		if message.Kind == protocol.MessageQuestion {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("reviewer asked %d times", asked)
	}
	e.c.must("POST", "/v1/questions/"+question.ID+"/answer", protocol.AnswerQuestionRequest{Body: "The September release", ClientKey: "overview-review-answer"}, nil)
	overview = protocol.Overview{}
	e.c.must("GET", "/v1/overview", nil, &overview)
	for _, row := range overview.Work {
		if row.Job.ID == root.ID && len(row.Questions) != 0 {
			t.Fatalf("answered question still needs attention: %+v", row.Questions)
		}
	}
}
