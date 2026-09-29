package integration

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/binbandit/yip/protocol"
)

func TestExportIncludesArchivedRooms(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{noRunner: true})
	sent := e.post("Engineering", "Keep this after the room is archived.", nil, nil)

	var rooms []protocol.Room
	e.c.must("GET", "/v1/rooms", nil, &rooms)
	var room protocol.Room
	for _, r := range rooms {
		if r.Name == "Engineering" {
			room = r
		}
	}
	archived := true
	e.c.must("PATCH", "/v1/rooms/"+room.ID, protocol.UpdateRoomRequest{Version: room.Version, Archived: &archived}, nil)
	e.c.must("GET", "/v1/rooms", nil, &rooms)
	for _, r := range rooms {
		if r.ID == room.ID {
			t.Fatal("an archived room is still listed")
		}
	}

	resp, body := e.c.fetch("/v1/export")
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("export: HTTP %d, %v", resp.StatusCode, err)
	}
	read := func(name string, out any) {
		t.Helper()
		f, err := zr.Open(name)
		if err != nil {
			t.Fatalf("export has no %s: %v", name, err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var exported []protocol.Room
	read("rooms.json", &exported)
	var found bool
	for _, r := range exported {
		if r.ID == room.ID {
			found = r.Archived
		}
	}
	if !found {
		t.Fatalf("rooms.json doesn't carry the archived room: %+v", exported)
	}
	var msgs []protocol.Message
	read("messages/"+room.ID+".json", &msgs)
	for _, m := range msgs {
		if m.ID == sent.Message.ID {
			return
		}
	}
	t.Fatalf("the archived room's messages aren't exported: %d messages", len(msgs))
}
