package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/binbandit/yip/protocol"
)

const messageCols = `id, org_id, room_id, COALESCE(thread_id, ''), seq, author_kind, author_id, body, kind, refs, project_ids,
	COALESCE(reply_to_id, ''), COALESCE(run_id, ''), COALESCE(job_id, ''), COALESCE(client_key, ''), revision, edited_at, deleted_at, created_at`

func scanMessage(s scanner) (protocol.Message, error) {
	var m protocol.Message
	var refs, projects, created string
	var edited, deleted sql.NullString
	err := s.Scan(&m.ID, &m.OrgID, &m.RoomID, &m.ThreadID, &m.Seq, &m.Author.Kind, &m.Author.ID, &m.Body, &m.Kind,
		&refs, &projects, &m.ReplyToID, &m.RunID, &m.JobID, &m.ClientKey, &m.Revision, &edited, &deleted, &created)
	unjs(refs, &m.Refs)
	unjs(projects, &m.ProjectIDs)
	if m.Refs == nil {
		m.Refs = []protocol.Ref{}
	}
	m.ProjectIDs = strs(m.ProjectIDs)
	m.EditedAt, m.DeletedAt = parseTSP(edited), parseTSP(deleted)
	m.CreatedAt = parseTS(created)
	m.Mentions, m.Reactions = []protocol.Mention{}, []protocol.ReactionSummary{}
	return m, err
}

// InsertMessage persists a message, its structured mentions, and its search
// entry. The caller allocates Seq via NextRoomSeq in the same transaction.
func InsertMessage(ctx context.Context, q Q, m protocol.Message) error {
	if m.Refs == nil {
		m.Refs = []protocol.Ref{}
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO messages(id, org_id, room_id, thread_id, seq, author_kind, author_id, body, kind, refs, project_ids,
		reply_to_id, run_id, job_id, client_key, revision, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		m.ID, m.OrgID, m.RoomID, nullStr(m.ThreadID), m.Seq, m.Author.Kind, m.Author.ID, m.Body, m.Kind, js(m.Refs), js(strs(m.ProjectIDs)),
		nullStr(m.ReplyToID), nullStr(m.RunID), nullStr(m.JobID), nullStr(m.ClientKey), ts(m.CreatedAt)); err != nil {
		return err
	}
	for _, mn := range m.Mentions {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO mentions(message_id, member_kind, member_id) VALUES (?, ?, ?)`, m.ID, mn.Kind, mn.ID); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO messages_fts(body, message_id, room_id) VALUES (?, ?, ?)`, m.Body, m.ID, m.RoomID); err != nil {
		return err
	}
	if m.ThreadID != "" {
		if _, err := q.ExecContext(ctx, `INSERT INTO threads(id, room_id, reply_count, last_reply_at, created_at) VALUES (?, ?, 1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET reply_count = reply_count + 1, last_reply_at = excluded.last_reply_at`,
			m.ThreadID, m.RoomID, ts(m.CreatedAt), ts(m.CreatedAt)); err != nil {
			return err
		}
	}
	return nil
}

// AppendMessageRefs adds references to an existing message (e.g. a job
// created from it) without changing its body.
func AppendMessageRefs(ctx context.Context, q Q, id string, refs ...protocol.Ref) error {
	m, err := GetMessage(ctx, q, id)
	if err != nil {
		return err
	}
	for _, r := range refs {
		dup := false
		for _, e := range m.Refs {
			if e == r {
				dup = true
			}
		}
		if !dup {
			m.Refs = append(m.Refs, r)
		}
	}
	_, err = q.ExecContext(ctx, `UPDATE messages SET refs = ? WHERE id = ?`, js(m.Refs), id)
	return err
}

func GetMessage(ctx context.Context, q Q, id string) (protocol.Message, error) {
	m, err := scanMessage(q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE id = ?`, id))
	if err != nil {
		return m, notFound(err)
	}
	return m, fillMessages(ctx, q, []*protocol.Message{&m}, "")
}

// GetMessageByClientKey finds an earlier send with the same idempotency key.
func GetMessageByClientKey(ctx context.Context, q Q, orgID, key string) (protocol.Message, error) {
	m, err := scanMessage(q.QueryRowContext(ctx, `SELECT `+messageCols+` FROM messages WHERE org_id = ? AND client_key = ?`, orgID, key))
	if err != nil {
		return m, notFound(err)
	}
	return m, fillMessages(ctx, q, []*protocol.Message{&m}, "")
}

// ListRoomMessages returns top-level messages before a sequence (0 = latest),
// oldest first.
func ListRoomMessages(ctx context.Context, q Q, roomID string, beforeSeq int64, limit int, viewerID string) ([]protocol.Message, bool, error) {
	if beforeSeq <= 0 {
		beforeSeq = 1 << 62
	}
	msgs, err := list(ctx, q, scanMessage, `SELECT `+messageCols+` FROM messages
		WHERE room_id = ? AND thread_id IS NULL AND seq < ? ORDER BY seq DESC LIMIT ?`, roomID, beforeSeq, limit+1)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	ptrs := make([]*protocol.Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	return msgs, hasMore, fillMessages(ctx, q, ptrs, viewerID)
}

// ListThread returns the root message followed by its replies.
func ListThread(ctx context.Context, q Q, threadID, viewerID string) ([]protocol.Message, error) {
	msgs, err := list(ctx, q, scanMessage, `SELECT `+messageCols+` FROM messages WHERE id = ? OR thread_id = ? ORDER BY seq`, threadID, threadID)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*protocol.Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	return msgs, fillMessages(ctx, q, ptrs, viewerID)
}

// RecentMessages returns the latest messages in a room (optionally a thread),
// oldest first, for building context manifests.
func RecentMessages(ctx context.Context, q Q, roomID, threadID string, limit int) ([]protocol.Message, error) {
	var msgs []protocol.Message
	var err error
	if threadID != "" {
		msgs, err = list(ctx, q, scanMessage, `SELECT `+messageCols+` FROM messages WHERE room_id = ? AND (id = ? OR thread_id = ?)
			AND deleted_at IS NULL ORDER BY seq DESC LIMIT ?`, roomID, threadID, threadID, limit)
	} else {
		msgs, err = list(ctx, q, scanMessage, `SELECT `+messageCols+` FROM messages WHERE room_id = ? AND thread_id IS NULL
			AND deleted_at IS NULL ORDER BY seq DESC LIMIT ?`, roomID, limit)
	}
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	ptrs := make([]*protocol.Message, len(msgs))
	for i := range msgs {
		ptrs[i] = &msgs[i]
	}
	return msgs, fillMessages(ctx, q, ptrs, "")
}

func fillMessages(ctx context.Context, q Q, msgs []*protocol.Message, viewerID string) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]any, len(msgs))
	idx := map[string]*protocol.Message{}
	for i, m := range msgs {
		ids[i] = m.ID
		idx[m.ID] = m
	}
	in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	rows, err := q.QueryContext(ctx, `SELECT message_id, member_kind, member_id FROM mentions WHERE message_id IN `+in, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var mid string
		var mn protocol.Mention
		if err := rows.Scan(&mid, &mn.Kind, &mn.ID); err != nil {
			rows.Close()
			return err
		}
		idx[mid].Mentions = append(idx[mid].Mentions, mn)
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT message_id, emoji, COUNT(*), SUM(CASE WHEN actor_kind = 'user' AND actor_id = ? THEN 1 ELSE 0 END)
		FROM reactions WHERE message_id IN `+in+` GROUP BY message_id, emoji ORDER BY MIN(created_at)`, append([]any{viewerID}, ids...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var mid string
		var r protocol.ReactionSummary
		var mine int
		if err := rows.Scan(&mid, &r.Emoji, &r.Count, &mine); err != nil {
			rows.Close()
			return err
		}
		r.Mine = mine > 0
		idx[mid].Reactions = append(idx[mid].Reactions, r)
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT id, reply_count, COALESCE(last_reply_at, '') FROM threads WHERE id IN `+in, ids...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, last string
		var n int
		if err := rows.Scan(&id, &n, &last); err != nil {
			rows.Close()
			return err
		}
		if n > 0 {
			idx[id].Thread = &protocol.ThreadSummary{ReplyCount: n, LastReplyAt: parseTS(last), Participants: []protocol.Actor{}}
		}
	}
	rows.Close()
	for _, m := range msgs {
		if m.Thread != nil {
			parts, err := list(ctx, q, func(s scanner) (protocol.Actor, error) {
				var a protocol.Actor
				err := s.Scan(&a.Kind, &a.ID)
				return a, err
			}, `SELECT author_kind, author_id FROM messages WHERE thread_id = ? GROUP BY author_kind, author_id ORDER BY MAX(seq) DESC LIMIT 5`, m.ID)
			if err != nil {
				return err
			}
			m.Thread.Participants = parts
		}
		if m.DeletedAt != nil {
			m.Body = ""
		}
	}
	return nil
}

// SetThreadOwner records the engineer who owns work started in a thread, so
// unaddressed follow-ups route to them.
func SetThreadOwner(ctx context.Context, q Q, threadID, roomID, engineerID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO threads(id, room_id, reply_count, owner_id, created_at) VALUES (?, ?, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET owner_id = COALESCE(threads.owner_id, excluded.owner_id)`, threadID, roomID, engineerID, ts(nowUTC()))
	return err
}

func ThreadOwner(ctx context.Context, q Q, threadID string) (string, error) {
	var owner sql.NullString
	err := q.QueryRowContext(ctx, `SELECT owner_id FROM threads WHERE id = ?`, threadID).Scan(&owner)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return owner.String, err
}

func SetReaction(ctx context.Context, q Q, messageID string, actor protocol.Actor, emoji string, remove bool) error {
	if remove {
		_, err := q.ExecContext(ctx, `DELETE FROM reactions WHERE message_id = ? AND actor_kind = ? AND actor_id = ? AND emoji = ?`,
			messageID, actor.Kind, actor.ID, emoji)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO reactions(message_id, actor_kind, actor_id, emoji, created_at) VALUES (?, ?, ?, ?, ?)`,
		messageID, actor.Kind, actor.ID, emoji, ts(nowUTC()))
	return err
}

// EditMessage records a revision and replaces the body.
func EditMessage(ctx context.Context, q Q, id, revID, body string) error {
	var old string
	var rev int
	if err := q.QueryRowContext(ctx, `SELECT body, revision FROM messages WHERE id = ?`, id).Scan(&old, &rev); err != nil {
		return notFound(err)
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO message_revisions(id, message_id, revision, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		revID, id, rev, old, ts(nowUTC())); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE messages SET body = ?, revision = revision + 1, edited_at = ? WHERE id = ?`, body, ts(nowUTC()), id); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM messages_fts WHERE message_id = ?`, id); err != nil {
		return err
	}
	var room string
	_ = q.QueryRowContext(ctx, `SELECT room_id FROM messages WHERE id = ?`, id).Scan(&room)
	_, err := q.ExecContext(ctx, `INSERT INTO messages_fts(body, message_id, room_id) VALUES (?, ?, ?)`, body, id, room)
	return err
}

// RedactMessage removes content from the message, its revisions, search,
// retained event payloads, derived reply jobs, and recorded run manifests.
func RedactMessage(ctx context.Context, q Q, id string) error {
	var body string
	_ = q.QueryRowContext(ctx, `SELECT body FROM messages WHERE id = ?`, id).Scan(&body)
	if _, err := q.ExecContext(ctx, `UPDATE messages SET body = '', deleted_at = ? WHERE id = ?`, ts(nowUTC()), id); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE events SET payload = json_set(payload, '$.body', '')
		WHERE type IN ('message.created', 'message.updated') AND json_extract(payload, '$.id') = ?`, id); err != nil {
		return err
	}
	// A reply job is titled by its request: clear it, and its events.
	if _, err := q.ExecContext(ctx, `UPDATE events SET payload = json_set(payload, '$.title', '[deleted message]', '$.objective', '')
		WHERE type IN ('job.created', 'job.updated') AND job_id IN (SELECT id FROM jobs WHERE source_message_id = ? AND kind = 'reply')`, id); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE jobs SET title = '[deleted message]', objective = '' WHERE source_message_id = ? AND kind = 'reply'`, id); err != nil {
		return err
	}
	if len(body) >= 8 {
		if _, err := q.ExecContext(ctx, `UPDATE jobs SET objective = '' WHERE objective = ?`, body); err != nil {
			return err
		}
		// Anywhere else the exact text was copied (run context, other event
		// payloads). The JSON-escaped form can only match inside a string.
		escaped := strings.TrimSuffix(strings.TrimPrefix(js(body), `"`), `"`)
		if _, err := q.ExecContext(ctx, `UPDATE runs SET manifest = replace(manifest, ?, '[deleted]') WHERE instr(manifest, ?) > 0`, escaped, escaped); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE events SET payload = replace(payload, ?, '[deleted]') WHERE instr(payload, ?) > 0`, escaped, escaped); err != nil {
			return err
		}
		// Steering input taken from the message, the run's activity log, and
		// runner commands not yet delivered (their JSON-escaped copies).
		if _, err := q.ExecContext(ctx, `UPDATE job_inputs SET body = '[deleted message]' WHERE message_id = ? OR body = ?`, id, body); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE run_events SET text = replace(text, ?, '[deleted]') WHERE instr(text, ?) > 0`, body, body); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE outbox SET frame = replace(frame, ?, '[deleted]') WHERE instr(frame, ?) > 0`, escaped, escaped); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM jobs_fts WHERE objective = ?`, body); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `UPDATE message_revisions SET body = '' WHERE message_id = ?`, id); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM messages_fts WHERE message_id = ?`, id)
	return err
}

// SearchMessages runs a full-text query restricted to the given rooms. The
// room filter is applied inside the query, before ranking.
func SearchMessages(ctx context.Context, q Q, match string, roomIDs []string, limit int) ([]protocol.SearchResult, error) {
	if len(roomIDs) == 0 {
		return []protocol.SearchResult{}, nil
	}
	args := []any{match}
	for _, r := range roomIDs {
		args = append(args, r)
	}
	args = append(args, limit)
	in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(roomIDs)), ",") + ")"
	return list(ctx, q, func(s scanner) (protocol.SearchResult, error) {
		var r protocol.SearchResult
		var thread, created string
		err := s.Scan(&r.ID, &r.RoomID, &thread, &r.Snippet, &created)
		r.Kind, r.ThreadID = "message", thread
		t := parseTS(created)
		r.At = &t
		return r, err
	}, `SELECT m.id, m.room_id, COALESCE(m.thread_id, ''), snippet(messages_fts, 0, '[', ']', '…', 12), m.created_at
		FROM messages_fts f JOIN messages m ON m.id = f.message_id
		WHERE messages_fts MATCH ? AND f.room_id IN `+in+` AND m.deleted_at IS NULL
		ORDER BY rank LIMIT ?`, args...)
}
