package hub

import (
	"context"
	"errors"
	"testing"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestNoteReplacementAtCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, previousStatus, previousKind string
		ownerWritten, untrusted, additive  bool
		wantAccepted                       bool
	}{
		{name: "accepted replacement", previousStatus: "accepted", wantAccepted: true},
		{name: "proposed replacement", previousStatus: "proposed", wantAccepted: true},
		{name: "additive note", additive: true},
		{name: "pending replacement", previousStatus: "accepted", untrusted: true},
		{name: "owner correction", previousStatus: "accepted", ownerWritten: true},
		{name: "retired replacement", previousStatus: "superseded"},
		{name: "work record replacement", previousStatus: "accepted", previousKind: "record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h, owner, env := noteTestEnv(t)
			eng, room, actor := env.eng, env.room, env.me
			var err error
			var previous protocol.EngineerNote
			var sourceID string
			err = h.do(ctx, func(tx *txn) error {
				by := userActor(owner.ID)
				if tc.untrusted {
					by = actor
				}
				msg, err := tx.postMessage(newMessage{Room: room.ID, Author: by, Body: "Use the revised procedure."})
				if err != nil {
					return err
				}
				sourceID = msg.ID
				for i := 0; i < notesPerEngineer; i++ {
					n := protocol.EngineerNote{ID: domain.NewID(), EngineerID: eng.ID, Kind: "note", Scope: protocol.DecisionScope{Kind: "room", ID: room.ID}, Body: "Existing note", Status: "accepted", CreatedBy: actor, CreatedAt: h.now(), ReviewAfter: h.now().Add(noteReviewAfter)}
					if i == 0 && !tc.additive {
						n.Status = tc.previousStatus
						if tc.previousKind != "" {
							n.Kind = tc.previousKind
						}
						if tc.ownerWritten {
							n.CreatedBy = userActor(owner.ID)
						}
						previous = n
						// Retired notes and work records do not use a note slot.
						if n.Status == "superseded" || n.Kind == "record" {
							if err := store.InsertNote(ctx, tx.tx, n); err != nil {
								return err
							}
							n.ID, n.Status, n.Kind = domain.NewID(), "accepted", "note"
						}
					}
					if err := store.InsertNote(ctx, tx.tx, n); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var result any
			err = h.do(ctx, func(tx *txn) error {
				var err error
				result, err = h.toolNoteRecord(ctx, tx, toolEnv{eng: eng, room: room, me: actor, job: store.JobRow{Job: protocol.Job{Kind: protocol.JobKindReply}}}, bridge.NoteRecordArgs{Body: "Updated procedure", Sources: []string{sourceID}, Supersedes: previous.ID})
				return err
			})
			if tc.wantAccepted {
				if err != nil {
					t.Fatalf("a replacement that frees its old slot must succeed at capacity: %v", err)
				}
				out := result.(map[string]any)
				if out["status"] != "accepted" {
					t.Fatalf("replacement not accepted: %+v", out)
				}
				old, err := store.GetNote(ctx, h.st.R(), previous.ID)
				if err != nil || old.Status != "superseded" || old.SupersededByID != out["noteId"] {
					t.Fatalf("old note not superseded: %+v, %v", old, err)
				}
			} else {
				var de *domain.Error
				if !errors.As(err, &de) || de.Code != "limit_reached" && !(tc.previousStatus == "superseded" && de.Code == "conflict") {
					t.Fatalf("an extra occupied slot must still be limited: %v", err)
				}
				if previous.ID != "" {
					old, err := store.GetNote(ctx, h.st.R(), previous.ID)
					if err != nil || old.Status != previous.Status || old.SupersededByID != "" {
						t.Fatalf("rejected replacement changed old note: %+v, %v", old, err)
					}
				}
			}
			var current int
			if err := h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM engineer_notes WHERE engineer_id = ? AND kind = 'note' AND status IN ('proposed','accepted')`, eng.ID).Scan(&current); err != nil {
				t.Fatal(err)
			}
			if current != notesPerEngineer {
				t.Fatalf("current notes = %d, want %d", current, notesPerEngineer)
			}
		})
	}
}

func noteTestEnv(t *testing.T) (*Hub, protocol.User, toolEnv) {
	t.Helper()
	ctx := context.Background()
	h, err := Open(ctx, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Test", Name: "Owner", Handle: "owner", Password: "a-long-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := h.CreateEngineer(ctx, owner.ID, protocol.CreateEngineerRequest{Name: "Mira", Role: "Engineer", Provider: protocol.ProviderPreference{Provider: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	room, err := h.CreateRoom(ctx, owner.ID, protocol.CreateRoomRequest{Name: "Engineering", EngineerIDs: []string{eng.ID}, ReplyMode: protocol.ReplyModeQuiet})
	if err != nil {
		t.Fatal(err)
	}
	actor := protocol.Actor{Kind: protocol.ActorEngineer, ID: eng.ID}
	return h, owner, toolEnv{eng: eng, room: room, me: actor, job: store.JobRow{Job: protocol.Job{Kind: protocol.JobKindReply}}}
}

func TestNoteSupersessionKeepsSingleSuccessor(t *testing.T) {
	ctx := context.Background()
	h, owner, env := noteTestEnv(t)
	var ownerSource, engineerSource string
	if err := h.do(ctx, func(tx *txn) error {
		m, err := tx.postMessage(newMessage{Room: env.room.ID, Author: userActor(owner.ID), Body: "Verified procedure"})
		if err != nil {
			return err
		}
		ownerSource = m.ID
		m, err = tx.postMessage(newMessage{Room: env.room.ID, Author: env.me, Body: "Suggested correction"})
		engineerSource = m.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	record := func(source, supersedes string) (protocol.EngineerNote, error) {
		t.Helper()
		var note protocol.EngineerNote
		err := h.do(ctx, func(tx *txn) error {
			out, err := h.toolNoteRecord(ctx, tx, env, bridge.NoteRecordArgs{Body: "Procedure", Sources: []string{source}, Supersedes: supersedes})
			if err != nil {
				return err
			}
			note, err = store.GetNote(ctx, tx.tx, out.(map[string]any)["noteId"].(string))
			return err
		})
		return note, err
	}
	original, err := record(ownerSource, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := record(engineerSource, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := record(engineerSource, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.DecideNote(ctx, owner.ID, first.ID, protocol.NoteActionRequest{Action: "accept", Version: first.Version}); err != nil {
		t.Fatal(err)
	}
	_, err = h.DecideNote(ctx, owner.ID, second.ID, protocol.NoteActionRequest{Action: "accept", Version: second.Version})
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != "conflict" {
		t.Errorf("accepting a competing stale correction must conflict: %v", err)
	}
	if _, err := record(ownerSource, original.ID); !errors.As(err, &de) || de.Code != "conflict" {
		t.Errorf("recording a correction of a superseded note must conflict: %v", err)
	}
	old, err := store.GetNote(ctx, h.st.R(), original.ID)
	if err != nil || old.SupersededByID != first.ID {
		t.Errorf("the original successor was overwritten: %+v, %v", old, err)
	}
	pending, err := store.GetNote(ctx, h.st.R(), second.ID)
	if err != nil || pending.Status != "proposed" {
		t.Errorf("stale correction must remain a proposal: %+v, %v", pending, err)
	}
	// Even if its sources later become eligible, automatic acceptance must
	// leave the stale proposal alone instead of failing job completion.
	second.Sources = original.Sources
	if h.ownFinishedWorkOnly(ctx, h.st.R(), second) {
		t.Error("stale proposal became eligible for automatic acceptance")
	}
}
