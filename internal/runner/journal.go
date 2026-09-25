package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	_ "modernc.org/sqlite"

	"github.com/binbandit/yip/protocol"
)

// Journal is the runner's durable record of accepted commands, run state,
// and produced events. A command is acknowledged only after it is journaled;
// events stay until the hub acknowledges their commit.
type Journal struct{ db *sql.DB }

const journalSchema = `
CREATE TABLE IF NOT EXISTS commands (
  command_id TEXT PRIMARY KEY,
  type       TEXT NOT NULL,
  run_id     TEXT,
  ack        TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  run_id       TEXT PRIMARY KEY,
  epoch        INTEGER NOT NULL,
  state        TEXT NOT NULL,
  manifest     TEXT NOT NULL,
  workspace    TEXT NOT NULL DEFAULT '',
  last_seq     INTEGER NOT NULL DEFAULT 0,
  acked_seq    INTEGER NOT NULL DEFAULT 0,
  terminal     TEXT,
  terminal_acked INTEGER NOT NULL DEFAULT 0,
  last_activity TEXT NOT NULL DEFAULT '',
  updated_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  run_id TEXT NOT NULL,
  seq    INTEGER NOT NULL,
  frame  TEXT NOT NULL,
  acked  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, seq)
);
CREATE TABLE IF NOT EXISTS inputs (
  input_id TEXT PRIMARY KEY,
  run_id   TEXT NOT NULL,
  text     TEXT NOT NULL,
  delivered INTEGER NOT NULL DEFAULT 0
);`

func OpenJournal(path string) (*Journal, error) {
	v := url.Values{}
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "busy_timeout(5000)")
	v.Add("_pragma", "synchronous(FULL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+v.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(journalSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &Journal{db: db}, nil
}

func (j *Journal) Close() error { return j.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// CommandAck returns the recorded acknowledgement for a command, if any.
func (j *Journal) CommandAck(id string) (json.RawMessage, bool) {
	var ack string
	if err := j.db.QueryRow(`SELECT ack FROM commands WHERE command_id = ?`, id).Scan(&ack); err != nil {
		return nil, false
	}
	return json.RawMessage(ack), true
}

func (j *Journal) RecordCommand(id, typ, runID string, ack any) error {
	b, _ := json.Marshal(ack)
	_, err := j.db.Exec(`INSERT OR IGNORE INTO commands(command_id, type, run_id, ack, created_at) VALUES (?, ?, ?, ?, ?)`, id, typ, runID, string(b), now())
	return err
}

// AcceptRun journals an accepted offer (and its command) atomically.
func (j *Journal) AcceptRun(commandID string, m protocol.ExecutionManifest, epoch int64, ack protocol.RunAck) error {
	tx, err := j.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	mb, _ := json.Marshal(m)
	if _, err := tx.Exec(`INSERT INTO runs(run_id, epoch, state, manifest, updated_at) VALUES (?, ?, 'accepted', ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET epoch = excluded.epoch, state = 'accepted', manifest = excluded.manifest, updated_at = excluded.updated_at,
			terminal = NULL, terminal_acked = 0`,
		m.RunID, epoch, string(mb), now()); err != nil {
		return err
	}
	ab, _ := json.Marshal(ack)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO commands(command_id, type, run_id, ack, created_at) VALUES (?, 'offer_run', ?, ?, ?)`,
		commandID, m.RunID, string(ab), now()); err != nil {
		return err
	}
	return tx.Commit()
}

type journalRun struct {
	RunID     string
	Epoch     int64
	State     string
	Manifest  protocol.ExecutionManifest
	Workspace string
	LastSeq   int64
	AckedSeq  int64
	Terminal  *protocol.RunTerminal
	TermAcked bool
	Activity  string
}

func (j *Journal) Run(runID string) (journalRun, bool) {
	var r journalRun
	var manifest string
	var term sql.NullString
	var tacked int
	err := j.db.QueryRow(`SELECT run_id, epoch, state, manifest, workspace, last_seq, acked_seq, terminal, terminal_acked, last_activity
		FROM runs WHERE run_id = ?`, runID).Scan(&r.RunID, &r.Epoch, &r.State, &manifest, &r.Workspace, &r.LastSeq, &r.AckedSeq, &term, &tacked, &r.Activity)
	if err != nil {
		return r, false
	}
	_ = json.Unmarshal([]byte(manifest), &r.Manifest)
	if term.Valid {
		r.Terminal = &protocol.RunTerminal{}
		_ = json.Unmarshal([]byte(term.String), r.Terminal)
	}
	r.TermAcked = tacked == 1
	return r, true
}

// Runs lists journaled runs that the hub may still need to hear about.
func (j *Journal) Runs() ([]journalRun, error) {
	rows, err := j.db.Query(`SELECT run_id FROM runs WHERE terminal_acked = 0 OR acked_seq < last_seq`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []journalRun
	for _, id := range ids {
		if r, ok := j.Run(id); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (j *Journal) SetState(runID, state, workspace string) error {
	_, err := j.db.Exec(`UPDATE runs SET state = ?, workspace = CASE WHEN ? = '' THEN workspace ELSE ? END, updated_at = ? WHERE run_id = ?`,
		state, workspace, workspace, now(), runID)
	return err
}

func (j *Journal) SetActivity(runID, text string) {
	_, _ = j.db.Exec(`UPDATE runs SET last_activity = ? WHERE run_id = ?`, text, runID)
}

// AppendEvent assigns the next producer sequence and stores the frame.
func (j *Journal) AppendEvent(runID string, build func(seq int64) protocol.Frame) (protocol.Frame, error) {
	tx, err := j.db.Begin()
	if err != nil {
		return protocol.Frame{}, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRow(`UPDATE runs SET last_seq = last_seq + 1 WHERE run_id = ? RETURNING last_seq`, runID).Scan(&seq); err != nil {
		return protocol.Frame{}, err
	}
	f := build(seq)
	b, _ := json.Marshal(f)
	if _, err := tx.Exec(`INSERT INTO events(run_id, seq, frame) VALUES (?, ?, ?)`, runID, seq, string(b)); err != nil {
		return protocol.Frame{}, err
	}
	return f, tx.Commit()
}

// AckEvents marks events committed by the hub.
func (j *Journal) AckEvents(runID string, upTo int64) error {
	if _, err := j.db.Exec(`UPDATE events SET acked = 1 WHERE run_id = ? AND seq <= ?`, runID, upTo); err != nil {
		return err
	}
	_, err := j.db.Exec(`UPDATE runs SET acked_seq = MAX(acked_seq, ?) WHERE run_id = ?`, upTo, runID)
	return err
}

// Unacked returns frames the hub has not committed, in order.
func (j *Journal) Unacked(runID string) ([]protocol.Frame, error) {
	rows, err := j.db.Query(`SELECT frame FROM events WHERE run_id = ? AND acked = 0 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Frame
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		var f protocol.Frame
		if json.Unmarshal([]byte(s), &f) == nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

func (j *Journal) SetTerminal(runID string, t protocol.RunTerminal) error {
	b, _ := json.Marshal(t)
	_, err := j.db.Exec(`UPDATE runs SET terminal = ?, state = 'terminal', updated_at = ? WHERE run_id = ?`, string(b), now(), runID)
	return err
}

func (j *Journal) AckTerminal(runID string) error {
	_, err := j.db.Exec(`UPDATE runs SET terminal_acked = 1 WHERE run_id = ?`, runID)
	return err
}

func (j *Journal) LastSeq(runID string) int64 {
	var n int64
	_ = j.db.QueryRow(`SELECT last_seq FROM runs WHERE run_id = ?`, runID).Scan(&n)
	return n
}

func (j *Journal) AddInput(inputID, runID, text string) (bool, error) {
	res, err := j.db.Exec(`INSERT OR IGNORE INTO inputs(input_id, run_id, text) VALUES (?, ?, ?)`, inputID, runID, text)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (j *Journal) PendingInputs(runID string) ([][2]string, error) {
	rows, err := j.db.Query(`SELECT input_id, text FROM inputs WHERE run_id = ? AND delivered = 0 ORDER BY rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var id, text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		out = append(out, [2]string{id, text})
	}
	return out, rows.Err()
}

func (j *Journal) MarkInputDelivered(inputID string) {
	_, _ = j.db.Exec(`UPDATE inputs SET delivered = 1 WHERE input_id = ?`, inputID)
}

var errNotFound = errors.New("not found")

var _ = context.Background
