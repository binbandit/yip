package hub

import (
	"context"
	"errors"
	"strings"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// toolHumanAsk posts one genuine question in the source conversation. There
// is no separate inbox: the question is an ordinary message with a mention,
// correlated behind the scenes so a natural reply resolves it.
func (h *Hub) toolHumanAsk(ctx context.Context, t *txn, env toolEnv, a bridge.HumanAskArgs) (any, error) {
	if strings.TrimSpace(a.Question) == "" || strings.TrimSpace(a.MissingFact) == "" || strings.TrimSpace(a.ContextChecked) == "" {
		return nil, domain.Invalid("A question needs the message, the missing fact, and what you already checked.")
	}
	ownerID, err := h.ownerID(ctx, t.tx)
	if err != nil {
		return nil, err
	}
	owner, _ := store.GetUser(ctx, t.tx, ownerID)
	// Ask once: an open question about the same fact is not repeated.
	open, err := store.ListQuestions(ctx, t.tx, "job_id = ? AND status = 'open'", env.job.ID)
	if err != nil {
		return nil, err
	}
	for _, q := range open {
		if strings.EqualFold(strings.TrimSpace(q.MissingFact), strings.TrimSpace(a.MissingFact)) {
			return map[string]any{"questionId": q.ID, "duplicate": true, "note": "You already asked this; it is still open. Continue independent work."}, nil
		}
	}
	body := strings.TrimSpace(a.Question)
	if !strings.Contains(strings.ToLower(body), "@"+owner.Handle) {
		body = "@" + owner.Handle + " " + body
	}
	mentions := h.resolveTextMentions(ctx, t.tx, env.room.ID, body)
	msg, err := t.postMessage(newMessage{Room: env.run.Destination.RoomID, Thread: env.run.Destination.ThreadID, Author: env.me,
		Kind: protocol.MessageQuestion, Body: body, Mentions: mentions, RunID: env.run.ID, JobID: env.job.ID, Refs: jobRefFor(env.job),
		Root: env.run.RootRequestID})
	if err != nil {
		return nil, err
	}
	q := protocol.Question{ID: domain.NewID(), JobID: env.job.ID, AskerID: env.eng.ID, Recipient: protocol.Actor{Kind: protocol.ActorUser, ID: ownerID},
		MissingFact: strings.TrimSpace(a.MissingFact), ContextChecked: strings.TrimSpace(a.ContextChecked), DependentStep: strings.TrimSpace(a.DependentStep),
		ContinuingWith: strings.TrimSpace(a.ContinuingWith), Status: "open", Source: env.run.Destination, MessageID: msg.ID, CreatedAt: h.now()}
	if err := store.InsertQuestion(ctx, t.tx, h.Org().ID, env.run.ID, q); err != nil {
		return nil, err
	}
	if err := store.AppendMessageRefs(ctx, t.tx, msg.ID, protocol.Ref{Kind: "question", ID: q.ID}); err != nil {
		return nil, err
	}
	if m, err := store.GetMessage(ctx, t.tx, msg.ID); err == nil {
		_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
	}
	if err := t.emit(ev{Type: "question.created", Actor: env.me, Room: env.room.ID, Job: env.job.ID, Payload: q}); err != nil {
		return nil, err
	}
	return map[string]any{"questionId": q.ID, "note": "Asked in the conversation. Keep working on anything that doesn't depend on the answer. When nothing independent is left, call work_wait {reason:'missing_information'} and end your turn; you'll be resumed with the answer."}, nil
}

// resolveQuestionsFromMessage lets an ordinary reply answer a question:
// replying in its thread, or replying to it (the web composer does this by
// default when one question in the room is waiting on you). Merely
// mentioning the asker is not an answer: "@Pip also note X" is a
// clarification, and the owner can say "Not an answer".
func (h *Hub) resolveQuestionsFromMessage(ctx context.Context, t *txn, userID string, room protocol.Room, msg protocol.Message) ([]string, error) {
	open, err := store.ListQuestions(ctx, t.tx, "source_room_id = ? AND status = 'open' AND recipient_id = ?", room.ID, userID)
	if err != nil || len(open) == 0 {
		return nil, err
	}
	mentioned := map[string]bool{}
	for _, m := range msg.Mentions {
		if m.Kind == protocol.ActorEngineer {
			mentioned[m.ID] = true
		}
	}
	var candidates []protocol.Question
	for _, q := range open {
		switch {
		case msg.ReplyToID != "":
			if msg.ReplyToID == q.MessageID {
				candidates = []protocol.Question{q}
			}
		case msg.ThreadID != "" && msg.ThreadID == q.MessageID:
			candidates = []protocol.Question{q}
			// A thread rooted in the question is an explicit answer target.
			return h.resolveQuestionCandidate(ctx, t, candidates, msg)
		case msg.ThreadID != "" && q.Source.ThreadID == msg.ThreadID && (mentioned[q.AskerID] || len(msg.Mentions) == 0):
			candidates = append(candidates, q)
		}
	}
	return h.resolveQuestionCandidate(ctx, t, candidates, msg)
}

func (h *Hub) resolveQuestionCandidate(ctx context.Context, t *txn, candidates []protocol.Question, msg protocol.Message) ([]string, error) {
	// Several questions in one discussion need an explicit reply target.
	if len(candidates) != 1 {
		return nil, nil
	}
	ok, err := h.answerQuestion(ctx, t, candidates[0].ID, msg)
	if err != nil || !ok {
		return nil, err
	}
	return []string{candidates[0].ID}, nil
}

// answerQuestion resolves a question exactly once and resumes only its live
// dependent work. Duplicate and late replies change nothing; a cancelled job
// never restarts.
func (h *Hub) answerQuestion(ctx context.Context, t *txn, questionID string, msg protocol.Message) (bool, error) {
	q, err := store.GetQuestion(ctx, t.tx, questionID)
	if err != nil {
		return false, domain.NotFound("That question doesn't exist.")
	}
	ok, err := store.AnswerQuestion(ctx, t.tx, questionID, msg.ID, h.now())
	if err != nil || !ok {
		return false, err
	}
	q, _ = store.GetQuestion(ctx, t.tx, questionID)
	if err := store.AppendMessageRefs(ctx, t.tx, msg.ID, protocol.Ref{Kind: "question", ID: q.ID}); err != nil {
		return false, err
	}
	if m, err := store.GetMessage(ctx, t.tx, msg.ID); err == nil {
		_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
	}
	if err := t.emit(ev{Type: "question.updated", Actor: msg.Author, Room: q.Source.RoomID, Job: q.JobID, Payload: q}); err != nil {
		return false, err
	}
	job, err := store.GetJob(ctx, t.tx, q.JobID)
	if err != nil || !domain.JobLive(job.State) {
		return true, err
	}
	// If the asker is still running, deliver the answer into the live attempt.
	if run, err := store.ActiveRunForJob(ctx, t.tx, job.ID); err == nil && domain.RunHoldsLease(run.State) {
		in := protocol.JobInput{ID: domain.NewID(), JobID: job.ID, RunID: run.ID, Body: "Answer to your question (" + q.MissingFact + "): " + msg.Body,
			Delivery: "pending", MessageID: msg.ID, CreatedAt: h.now()}
		if err := store.InsertJobInput(ctx, t.tx, in, ""); err != nil {
			return true, err
		}
		if err := h.queueCommand(ctx, t, run.NodeID, run.ID, run.LeaseEpoch, protocol.CmdDeliverInput, "input:"+in.ID,
			protocol.DeliverInput{InputID: in.ID, Text: in.Body}); err != nil {
			return true, err
		}
		return true, t.emit(ev{Type: "input.updated", Room: job.Source.RoomID, Job: job.ID, Payload: in})
	}
	// Once nothing else they asked is open, the answer resumes the asker,
	// whatever the work moved on to wait for meanwhile (a review, a check).
	// An attempt that is already queued reads the answer when it starts.
	if n, _ := h.openQuestionsFor(ctx, t.tx, job.ID); n == 0 {
		if _, err := store.ActiveRunForJob(ctx, t.tx, job.ID); errors.Is(err, store.ErrNotFound) {
			if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "answer", Cause: msg.ID}); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// AnswerQuestion is POST /v1/questions/{id}/answer: a normal attributed reply
// in the question's own conversation, linked to it.
func (h *Hub) AnswerQuestion(ctx context.Context, userID, questionID string, req protocol.AnswerQuestionRequest) (protocol.PostMessageResponse, error) {
	q, err := store.GetQuestion(ctx, h.st.R(), questionID)
	if err != nil {
		return protocol.PostMessageResponse{}, domain.NotFound("That question doesn't exist.")
	}
	thread := q.Source.ThreadID
	if thread == "" {
		thread = q.MessageID
	}
	return h.PostMessage(ctx, userID, q.Source.RoomID, protocol.PostMessageRequest{Body: req.Body, ThreadID: thread, ReplyToID: q.MessageID, ClientKey: req.ClientKey})
}
