package integration

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/protocol"
)

func TestRevokedRunnerPairingHasExplicitRecovery(t *testing.T) {
	e := newEnv(t, envOptions{})
	srv := httptest.NewUnstartedServer(httpapi.NewRunnerServer(e.hub, nil))
	var err error
	srv.TLS, err = e.hub.CA().ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	enroll := func() protocol.Enrollment {
		t.Helper()
		en, err := e.hub.CreateEnrollment(e.ctx, "", protocol.CreateEnrollmentRequest{Name: "Work Mac"})
		if err != nil {
			t.Fatal(err)
		}
		return en
	}
	dir := t.TempDir()
	en := enroll()
	id, err := runner.Pair(e.ctx, dir, srv.URL, en.HubFingerprint, en.Token, "Work Mac")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.hub.RevokeNode(e.ctx, "", id.NodeID); err != nil {
		t.Fatal(err)
	}
	fresh := enroll()
	_, err = runner.Pair(e.ctx, dir, srv.URL, fresh.HubFingerprint, fresh.Token, "Work Mac")
	if err == nil || !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("revoked local pairing must explain the explicit replacement flow: %v", err)
	}
	work := filepath.Join(dir, "work", "job-unpublished")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "changes.txt"), []byte("unpublished work"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := runner.OpenJournal(filepath.Join(dir, "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := j.RecordCommand("old-command", "offer_run", "old-run", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	old := map[string][]byte{}
	for _, name := range []string{"node.json", "node.key", "node.pem", "hub-ca.pem", "journal.db", "work/job-unpublished/changes.txt"} {
		old[name] = read(filepath.Join(dir, name))
	}
	unchanged := func(includeIdentity bool) {
		t.Helper()
		for name, want := range old {
			if name == "node.json" && !includeIdentity {
				continue
			}
			if got := read(filepath.Join(dir, name)); !bytes.Equal(got, want) {
				t.Fatalf("replacement changed previous state %s", name)
			}
		}
	}
	for _, tc := range []struct{ name, fingerprint, token string }{
		{"invalid token", fresh.HubFingerprint, "invalid"},
		{"used token", fresh.HubFingerprint, en.Token},
		{"wrong hub pin", "sha256:" + strings.Repeat("0", 64), fresh.Token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runner.RePair(e.ctx, dir, srv.URL, tc.fingerprint, tc.token, "Work Mac"); err == nil {
				t.Fatal("replacement must require a fresh token and the pinned hub")
			}
			unchanged(true)
		})
	}
	paired, err := runner.RePair(e.ctx, dir, srv.URL, fresh.HubFingerprint, fresh.Token, "Work Mac")
	if err != nil {
		t.Fatal(err)
	}
	if paired.NodeID == id.NodeID || paired.StateDir == "" {
		t.Fatalf("replacement must create a distinct identity: %+v", paired)
	}
	unchanged(false)
	active, err := runner.ActiveStateDir(dir)
	if err != nil || active != filepath.Join(dir, paired.StateDir) {
		t.Fatalf("replacement state: %s, %v", active, err)
	}
	if !bytes.Equal(read(filepath.Join(active, "previous-node.json")), old["node.json"]) {
		t.Fatal("previous identity metadata was not preserved")
	}
	for _, tc := range []struct {
		pem  []byte
		want bool
	}{{old["node.pem"], false}, {read(filepath.Join(active, "node.pem")), true}} {
		cert, err := auth.ParseCertPEM(tc.pem)
		if err != nil {
			t.Fatal(err)
		}
		node, serial := auth.NodeIDFromCert(cert)
		if err := e.hub.AuthorizeNode(e.ctx, node, serial); (err == nil) != tc.want {
			t.Fatalf("old revocation/new enrollment authority changed: %v", err)
		}
	}
	r, err := runner.New(runner.Options{StateDir: dir, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	newJournal, err := runner.OpenJournal(filepath.Join(active, "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, found := newJournal.CommandAck("old-command"); found {
		t.Fatal("new identity must not replay old journal commands")
	}
	newJournal.Close()
	if files, err := os.ReadDir(filepath.Join(active, "work")); err != nil || len(files) != 0 {
		t.Fatalf("new identity must not expose previous workspaces: %v, %v", files, err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("replacement runner: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("replacement runner did not stop")
			}
		})
	}
	t.Cleanup(stop)
	e.waitFor("replacement runner connection", 5*time.Second, func() bool { return e.hub.Connected(paired.NodeID) })
	next := enroll()
	if _, err := runner.RePair(e.ctx, dir, srv.URL, next.HubFingerprint, next.Token, "Work Mac"); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("must refuse to replace a running runner: %v", err)
	}
	if _, err := runner.New(runner.Options{StateDir: dir}); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("must refuse concurrent runner startup: %v", err)
	}
	still, err := runner.LoadIdentity(dir)
	if err != nil || still.NodeID != paired.NodeID {
		t.Fatal("refused replacement changed the active identity")
	}
	stop()
	// A runner constructed just before replacement must not start later with
	// cached credentials and replay the previous identity's journal.
	stale, err := runner.New(runner.Options{StateDir: dir, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.hub.RevokeNode(e.ctx, "", paired.NodeID); err != nil {
		t.Fatal(err)
	}
	previousGeneration := read(filepath.Join(active, "node.json"))
	newer, err := runner.RePair(e.ctx, dir, srv.URL, next.HubFingerprint, next.Token, "Work Mac")
	if err != nil || newer.NodeID == paired.NodeID || newer.StateDir == paired.StateDir {
		t.Fatalf("repeat replacement after stopping runner: %+v, %v", newer, err)
	}
	if !bytes.Equal(previousGeneration, read(filepath.Join(active, "node.json"))) {
		t.Fatal("repeat replacement changed the previous generation")
	}
	startCtx, stopStart := context.WithTimeout(e.ctx, time.Second)
	defer stopStart()
	if err := stale.Run(startCtx); err == nil || !strings.Contains(err.Error(), "pairing changed") {
		t.Fatalf("stale runner must reload before it can start: %v", err)
	}
}
