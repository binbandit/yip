package runner

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func reopen(t *testing.T, j *Journal, path string) *Journal {
	t.Helper()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

// A command the hub re-sends after either side restarts is answered from the
// journal with the original acknowledgement, so the attempt runs once.
func TestJournalAnswersRepeatedCommandAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	m := protocol.ExecutionManifest{RunID: "run-1", Provider: "codex", Mode: protocol.ModeEdit}
	ack := protocol.RunAck{CommandID: "offer:run-1", Accepted: true, State: "accepted"}
	if err := j.AcceptRun("offer:run-1", m, 2, ack); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordCommand("start:run-1", protocol.CmdStartRun, "run-1", protocol.CommandAck{CommandID: "start:run-1", OK: true}); err != nil {
		t.Fatal(err)
	}
	j = reopen(t, j, path)

	raw, ok := j.CommandAck("offer:run-1")
	if !ok {
		t.Fatal("the accepted offer was not journaled")
	}
	var got protocol.RunAck
	if err := json.Unmarshal(raw, &got); err != nil || got != ack {
		t.Fatalf("the repeated offer should get the original acknowledgement: %s (%v)", raw, err)
	}
	if err := j.RecordCommand("start:run-1", protocol.CmdStartRun, "run-1", protocol.CommandAck{CommandID: "start:run-1", OK: false, Error: "later"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = j.CommandAck("start:run-1")
	var start protocol.CommandAck
	if err := json.Unmarshal(raw, &start); err != nil || !start.OK {
		t.Fatalf("a repeated command must not overwrite its first answer: %s", raw)
	}
	r, ok := j.Run("run-1")
	if !ok || r.Epoch != 2 || r.State != "accepted" || r.Manifest.Provider != "codex" {
		t.Fatalf("the accepted attempt did not survive the restart: %+v (found %v)", r, ok)
	}
}

// Events and the terminal report stay journaled until the hub acknowledges
// them, across a restart.
func TestJournalKeepsUnacknowledgedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.AcceptRun("offer:run-1", protocol.ExecutionManifest{RunID: "run-1"}, 1, protocol.RunAck{Accepted: true}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		if _, err := j.AppendEvent("run-1", func(seq int64) protocol.Frame {
			b, _ := json.Marshal(protocol.RunEvent{Seq: seq, Kind: protocol.RunEvStatus, Text: text})
			return protocol.Frame{Type: protocol.EvRunEvent, RunID: "run-1", LeaseEpoch: 1, Payload: b}
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.AckEvents("run-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := j.SetTerminal("run-1", protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, LastSeq: 3, ExitConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	j = reopen(t, j, path)

	frames, err := j.Unacked("run-1")
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, f := range frames {
		var ev protocol.RunEvent
		_ = json.Unmarshal(f.Payload, &ev)
		seqs = append(seqs, ev.Seq)
	}
	if len(seqs) != 2 || seqs[0] != 2 || seqs[1] != 3 {
		t.Fatalf("only events after the acknowledged one should be re-sent: %v", seqs)
	}
	if last := j.LastSeq("run-1"); last != 3 {
		t.Fatalf("the next event would reuse a sequence number: last %d", last)
	}
	r, _ := j.Run("run-1")
	if r.Terminal == nil || r.Terminal.Outcome != protocol.OutcomeSucceeded || r.TermAcked {
		t.Fatalf("the unacknowledged terminal report was lost: %+v", r)
	}
	if err := j.AckTerminal("run-1"); err != nil {
		t.Fatal(err)
	}
	if r, _ := j.Run("run-1"); !r.TermAcked {
		t.Fatalf("the terminal acknowledgement was not recorded")
	}
}
