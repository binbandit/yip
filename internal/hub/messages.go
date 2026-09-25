package hub

import (
	"context"
	"errors"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const maxMessageBytes = 20000

// ListMessages returns a page of top-level room history.
func (h *Hub) ListMessages(ctx context.Context, userID, roomID string, beforeSeq int64, limit int) (protocol.MessagePage, error) {
	if _, err := h.requireRoom(ctx, h.st.R(), userID, roomID); err != nil {
		return protocol.MessagePage{}, err
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	msgs, more, err := store.ListRoomMessages(ctx, h.st.R(), roomID, beforeSeq, limit, userID)
	if err != nil {
		return protocol.MessagePage{}, err
	}
	return protocol.MessagePage{Messages: msgs, HasMore: more}, nil
}

// ListThread returns a thread's root and replies.
func (h *Hub) ListThread(ctx context.Context, userID, threadID string) ([]protocol.Message, error) {
	root, err := store.GetMessage(ctx, h.st.R(), threadID)
	if err != nil {
		return nil, domain.NotFound("That thread doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, root.RoomID); err != nil {
		return nil, err
	}
	return store.ListThread(ctx, h.st.R(), threadID, userID)
}

// PostMessage persists a human message and performs routing: question
// resolution, steering input to a selected job, and wakeups for explicitly
// addressed engineers. Text that merely looks like a mention never wakes
// anyone; only structured mentions do.
func (h *Hub) PostMessage(ctx context.Context, userID, roomID string, req protocol.PostMessageRequest) (protocol.PostMessageResponse, error) {
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" {
		return protocol.PostMessageResponse{}, domain.Invalid("Write a message first.")
	}
	if len(req.Body) > maxMessageBytes {
		return protocol.PostMessageResponse{}, domain.Invalid("That message is too long (limit %d characters).", maxMessageBytes)
	}
	var resp protocol.PostMessageResponse
	resp.Dispatched, resp.Resolved = []string{}, []string{}
	err := h.do(ctx, func(t *txn) error {
		room, err := h.requireRoom(ctx, t.tx, userID, roomID)
		if err != nil {
			return err
		}
		if room.Archived {
			return domain.Invalid("This room is archived.")
		}
		// Idempotent send: a retried request reconciles to the original.
		if req.ClientKey != "" {
			if prior, err := store.GetMessageByClientKey(ctx, t.tx, h.Org().ID, req.ClientKey); err == nil {
				if prior.RoomID != roomID || prior.Author.ID != userID {
					return domain.Conflict("That idempotency key was already used for a different message.")
				}
				resp.Message, resp.Duplicate = prior, true
				return nil
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		// Validate thread and structured references.
		if req.ThreadID != "" {
			root, err := store.GetMessage(ctx, t.tx, req.ThreadID)
			if err != nil || root.RoomID != roomID || root.ThreadID != "" {
				return domain.Invalid("That thread isn't in this room.")
			}
		}
		if req.ReplyToID != "" {
			ref, err := store.GetMessage(ctx, t.tx, req.ReplyToID)
			if err != nil || ref.RoomID != roomID {
				return domain.Invalid("You can only reply to a message in this room.")
			}
		}
		mentions, err := h.validMentions(ctx, t.tx, room, req.Mentions)
		if err != nil {
			return err
		}
		for _, p := range req.ProjectIDs {
			if !contains(room.ProjectIDs, p) {
				return domain.Invalid("That project isn't linked to this room.")
			}
		}
		msg, err := t.postMessage(newMessage{Room: roomID, Thread: req.ThreadID, Author: userActor(userID), Body: req.Body,
			Mentions: mentions, ProjectIDs: req.ProjectIDs, ReplyTo: req.ReplyToID, ClientKey: nilIfEmpty(req.ClientKey)})
		if err != nil {
			return err
		}
		resp.Message = msg

		// The owner's Overview answers status from the ledger, without waking anyone.
		if room.Kind == protocol.RoomKindOverview && len(engineerMentions(mentions)) == 0 {
			return h.answerStatus(ctx, t, userID, room, msg)
		}

		// 1. A natural reply resolves a correlated question.
		resolved, err := h.resolveQuestionsFromMessage(ctx, t, userID, room, msg)
		if err != nil {
			return err
		}
		resp.Resolved = resolved

		// 2. Input to an explicitly selected running job.
		if req.JobID != "" {
			in, err := h.addJobInput(ctx, t, userID, req.JobID, msg, "")
			if err != nil {
				return err
			}
			resp.Input = &in
			return nil
		}

		// 3. Routing to engineers.
		recipients := engineerMentions(mentions)
		if len(recipients) == 0 && len(resolved) > 0 {
			return nil // the reply answered a question; the dependent work resumes
		}
		if len(recipients) == 0 && req.ThreadID != "" {
			if owner, _ := store.ThreadOwner(ctx, t.tx, req.ThreadID); owner != "" {
				// Follow-up in a thread with live work goes to that work as input.
				if job, ok := h.liveThreadJob(ctx, t.tx, req.ThreadID, owner); ok {
					in, err := h.addJobInput(ctx, t, userID, job.ID, msg, "")
					if err != nil {
						return err
					}
					resp.Input = &in
					return nil
				}
				recipients = []string{owner}
			}
		}
		if len(recipients) == 0 && req.ThreadID == "" && room.ReplyMode == protocol.ReplyModeSteward && room.StewardID != "" {
			recipients = []string{room.StewardID}
		}
		if len(recipients) == 0 {
			return nil // a quiet room stays quiet
		}
		owner, contributors := recipients[0], recipients[1:]
		job, err := h.createJob(ctx, t, jobSpec{
			Kind: protocol.JobKindReply, Title: truncate(firstLine(req.Body), 90), Objective: req.Body, Owner: owner,
			Contributors: contributors, Root: msg.ID,
			Source:     protocol.Destination{RoomID: roomID, ThreadID: req.ThreadID, MessageID: msg.ID},
			ProjectIDs: req.ProjectIDs, Actor: userActor(userID),
		})
		if err != nil {
			return err
		}
		if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "message", Cause: msg.ID, Automatic: false}); err != nil {
			return err
		}
		resp.Dispatched = []string{owner}
		return nil
	})
	return resp, err
}

func nilIfEmpty(s string) string { return s }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// validMentions keeps only structured mentions of this room's members (and
// its human members). A mention of a non-member is rejected rather than
// silently dropped, because the sender expected someone to see it.
func (h *Hub) validMentions(ctx context.Context, q store.Q, room protocol.Room, in []protocol.Mention) ([]protocol.Mention, error) {
	var out []protocol.Mention
	seen := map[string]bool{}
	for _, m := range in {
		if seen[m.Kind+m.ID] {
			continue
		}
		seen[m.Kind+m.ID] = true
		switch m.Kind {
		case protocol.ActorEngineer, protocol.ActorUser:
		default:
			return nil, domain.Invalid("Unknown mention type %q.", m.Kind)
		}
		ok, err := store.IsMember(ctx, q, room.ID, m.Kind, m.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			if m.Kind == protocol.ActorEngineer {
				name := h.engineerName(ctx, q, m.ID)
				return nil, domain.Invalid("%s isn't in this room. Add them to the room first.", name)
			}
			return nil, domain.Invalid("That person isn't in this room.")
		}
		if m.Kind == protocol.ActorEngineer {
			e, err := store.GetEngineer(ctx, q, m.ID)
			if err != nil || e.Archived {
				return nil, domain.Invalid("That engineer is archived.")
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func engineerMentions(ms []protocol.Mention) []string {
	var out []string
	for _, m := range ms {
		if m.Kind == protocol.ActorEngineer {
			out = append(out, m.ID)
		}
	}
	return out
}

// liveThreadJob finds non-reply live work owned by the thread owner whose
// source is this thread.
func (h *Hub) liveThreadJob(ctx context.Context, q store.Q, threadID, owner string) (store.JobRow, bool) {
	jobs, err := store.ListJobs(ctx, q, store.JobFilter{OwnerID: owner, States: []protocol.JobState{
		protocol.JobQueued, protocol.JobRunning, protocol.JobWaiting, protocol.JobReviewReady}})
	if err != nil {
		return store.JobRow{}, false
	}
	for _, j := range jobs {
		if j.Source.ThreadID == threadID || j.Source.MessageID == threadID {
			return j, true
		}
	}
	return store.JobRow{}, false
}

// React adds or removes an emoji reaction. Reactions are social only; they
// never authorize anything.
func (h *Hub) React(ctx context.Context, userID, messageID string, req protocol.ReactRequest) (protocol.Message, error) {
	emoji := strings.TrimSpace(req.Emoji)
	if emoji == "" || len([]rune(emoji)) > 8 {
		return protocol.Message{}, domain.Invalid("Choose a reaction.")
	}
	var msg protocol.Message
	err := h.do(ctx, func(t *txn) error {
		m, err := store.GetMessage(ctx, t.tx, messageID)
		if err != nil {
			return domain.NotFound("That message doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, m.RoomID); err != nil {
			return err
		}
		if err := store.SetReaction(ctx, t.tx, messageID, userActor(userID), emoji, req.Remove); err != nil {
			return err
		}
		if msg, err = store.GetMessage(ctx, t.tx, messageID); err != nil {
			return err
		}
		return t.emit(ev{Type: "message.updated", Actor: userActor(userID), Room: m.RoomID, Payload: msg})
	})
	if err == nil {
		// Reactions are per-viewer; recompute "mine" for the caller.
		msgs, _ := store.ListThread(ctx, h.st.R(), msg.ID, userID)
		for _, m := range msgs {
			if m.ID == msg.ID {
				msg = m
			}
		}
	}
	return msg, err
}

// EditMessage lets the owner correct their own message; revisions are kept.
func (h *Hub) EditMessage(ctx context.Context, userID, messageID, body string) (protocol.Message, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return protocol.Message{}, domain.Invalid("A message can't be empty. Delete it instead.")
	}
	var msg protocol.Message
	err := h.do(ctx, func(t *txn) error {
		m, err := store.GetMessage(ctx, t.tx, messageID)
		if err != nil || m.Author.Kind != protocol.ActorUser || m.Author.ID != userID {
			return domain.NotFound("You can only edit your own messages.")
		}
		if err := store.EditMessage(ctx, t.tx, messageID, domain.NewID(), body); err != nil {
			return err
		}
		if msg, err = store.GetMessage(ctx, t.tx, messageID); err != nil {
			return err
		}
		return t.emit(ev{Type: "message.updated", Actor: userActor(userID), Room: m.RoomID, Payload: msg})
	})
	return msg, err
}

// DeleteMessage redacts the owner's own message from history and search. An
// audit tombstone is kept without the deleted text.
func (h *Hub) DeleteMessage(ctx context.Context, userID, messageID string) error {
	return h.do(ctx, func(t *txn) error {
		m, err := store.GetMessage(ctx, t.tx, messageID)
		if err != nil || m.Author.Kind != protocol.ActorUser || m.Author.ID != userID {
			return domain.NotFound("You can only delete your own messages.")
		}
		if err := store.RedactMessage(ctx, t.tx, messageID); err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "message.redact", messageID, "ok", ""); err != nil {
			return err
		}
		m, _ = store.GetMessage(ctx, t.tx, messageID)
		return t.emit(ev{Type: "message.updated", Actor: userActor(userID), Room: m.RoomID, Payload: m})
	})
}
