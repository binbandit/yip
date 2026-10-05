package integration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func TestRemoveRevokedMachine(t *testing.T) {
	t.Parallel()
	e := newEnv(t, envOptions{})
	n := e.connectNode("Retired test machine")
	path := "/v1/nodes/" + n.id
	wantStatus := func(c *client, want int) {
		t.Helper()
		var ae *apiError
		if err := c.do("DELETE", path, nil, nil); !errors.As(err, &ae) || ae.status != want {
			t.Fatalf("remove status: %v; want %d", err, want)
		}
	}
	unsigned := *e.c
	unsigned.hc = &http.Client{}
	wantStatus(&unsigned, http.StatusUnauthorized)
	noCSRF := *e.c
	noCSRF.csrf = ""
	wantStatus(&noCSRF, http.StatusForbidden)
	wantStatus(e.c, http.StatusConflict)
	// A signed-in user in the same workspace still needs owner authority.
	member, err := store.GetUser(e.ctx, e.hub.Store().R(), e.c.boot.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	member.ID, member.Handle = "non-owner", "non-owner"
	member.CreatedAt = member.CreatedAt.Add(time.Second)
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		return store.InsertUser(e.ctx, tx, member)
	}); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	nonOwner := *e.c
	nonOwner.hc = &http.Client{Jar: jar}
	nonOwner.must("POST", "/v1/session", map[string]string{"handle": member.Handle, "password": ownerPassword}, nil)
	nonOwner.must("GET", "/v1/bootstrap", nil, &nonOwner.boot)
	nonOwner.csrf = nonOwner.boot.CSRFToken
	wantStatus(&nonOwner, http.StatusForbidden)
	other := newEnv(t, envOptions{})
	var ae *apiError
	if err := other.c.do("DELETE", path, nil, nil); !errors.As(err, &ae) || ae.status != http.StatusNotFound {
		t.Fatalf("another workspace can remove the node: %v", err)
	}

	n.createWork(e, "Security", "Mira", "removal history test", codeWork("Preserve machine history"))
	r := n.startJob(e, "Preserve machine history")
	localFile := filepath.Join(t.TempDir(), "unpublished-work.txt")
	if err := os.WriteFile(localFile, []byte("keep this work"), 0600); err != nil {
		t.Fatal(err)
	}
	workspaces := []protocol.WorkspaceInfo{{Name: "job-aaaaaaaaaaaa", Kind: "job", Ref: "aaaaaaaaaaaa", Changes: 1}}
	if err := e.hub.Store().Tx(e.ctx, func(tx *sql.Tx) error {
		return store.SetNodeWorkspaces(e.ctx, tx, n.id, workspaces)
	}); err != nil {
		t.Fatal(err)
	}
	e.c.must("DELETE", path+"/credential", nil, nil)
	wantStatus(&nonOwner, http.StatusForbidden)
	before, err := store.GetNode(e.ctx, e.hub.Store().R(), n.id)
	if err != nil {
		t.Fatal(err)
	}
	var visible []protocol.Node
	e.c.must("GET", "/v1/nodes", nil, &visible)
	if len(visible) != 1 || visible[0].Status != protocol.NodeRevoked {
		t.Fatalf("revocation alone must leave the machine visible: %+v", visible)
	}
	runBefore, err := store.GetRun(e.ctx, e.hub.Store().R(), r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	detailBefore := e.jobDetail(runBefore.JobID)

	e.c.must("DELETE", path, nil, nil)
	e.c.must("DELETE", path, nil, nil)
	after, err := store.GetNode(e.ctx, e.hub.Store().R(), n.id)
	if err != nil || after.RemovedAt == nil {
		t.Fatalf("removed record missing tombstone: %+v %v", after, err)
	}
	removedAt := *after.RemovedAt
	after.RemovedAt = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("removal changed historical machine data: before=%+v after=%+v", before, after)
	}
	runAfter, err := store.GetRun(e.ctx, e.hub.Store().R(), r.RunID)
	if err != nil || !reflect.DeepEqual(runBefore, runAfter) {
		t.Fatalf("removal changed the run: %+v %v", runAfter, err)
	}
	if detailAfter := e.jobDetail(runBefore.JobID); !reflect.DeepEqual(detailBefore, detailAfter) {
		t.Fatal("removal changed job history")
	}
	if contents, err := os.ReadFile(localFile); err != nil || string(contents) != "keep this work" {
		t.Fatalf("local file changed: %q %v", contents, err)
	}
	if _, sent := n.find(func(f protocol.Frame) bool { return f.Type == protocol.CmdCleanup }); sent {
		t.Fatal("machine removal sent a workspace cleanup command")
	}
	var audits int
	if err := e.hub.Store().R().QueryRowContext(e.ctx, `SELECT COUNT(*) FROM audit_entries WHERE action = 'node.remove' AND target = ?`, n.id).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("removal audit must be emitted once: %d %v", audits, err)
	}
	events, err := store.EventsAfter(e.ctx, e.hub.Store().R(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	removals := 0
	for _, event := range events {
		if event.Type != "node.updated" {
			continue
		}
		var node protocol.Node
		if err := json.Unmarshal(event.Payload, &node); err != nil {
			t.Fatal(err)
		}
		if node.ID == n.id && node.RemovedAt != nil {
			removals++
		}
	}
	if removals != 1 {
		t.Fatalf("expected one removal event, got %d", removals)
	}
	for _, restart := range []bool{false, true} {
		if restart {
			e.restartHub()
		}
		e.c.must("GET", "/v1/nodes", nil, &visible)
		var boot protocol.Bootstrap
		e.c.must("GET", "/v1/bootstrap", nil, &boot)
		if len(visible) != 0 || len(boot.Nodes) != 0 {
			t.Fatal("removed node returned to list or bootstrap")
		}
		e.c.must("DELETE", path, nil, nil)
		stored, err := store.GetNode(e.ctx, e.hub.Store().R(), n.id)
		if err != nil || stored.RemovedAt == nil || !stored.RemovedAt.Equal(removedAt) {
			t.Fatalf("repeated removal changed timestamp: %+v %v", stored, err)
		}
		conn := &hub.NodeConn{NodeID: n.id, Serial: n.conn.Serial}
		if err := e.hub.ConnectRunner(e.ctx, conn, protocol.Hello{NodeID: n.id, ProtocolVersion: protocol.RunnerProtocolVersion}); err == nil {
			t.Fatal("removed credential was allowed to reconnect")
		}
	}
}
