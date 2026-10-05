package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/protocol"
)

func TestRePairRejectsInvalidCredentialsWithoutChangingState(t *testing.T) {
	for _, scenario := range []string{"malformed response", "wrong issuer", "wrong node", "expired certificate"} {
		t.Run(scenario, func(t *testing.T) {
			ca, err := auth.LoadOrCreateCA(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			key, csr, err := auth.NewNodeKeyAndCSR("old")
			if err != nil {
				t.Fatal(err)
			}
			cert, _, _, err := ca.SignNodeCSR(csr, "old", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := SaveLocalIdentity(dir, Identity{NodeID: "old", Fingerprint: ca.Fingerprint()}, key, cert, ca.CertPEM); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(dir, "node.json"))
			issuer := ca
			if scenario == "wrong issuer" {
				issuer, err = auth.LoadOrCreateCA(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req protocol.PairRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusCreated)
				if scenario == "malformed response" {
					_, _ = w.Write([]byte(`{"nodeId":`))
					return
				}
				node, validity := "new", time.Hour
				if scenario == "wrong node" {
					node = "another"
				}
				if scenario == "expired certificate" {
					validity = -time.Minute
				}
				cert, _, _, err := issuer.SignNodeCSR([]byte(req.CSRPEM), node, validity)
				if err != nil {
					t.Error(err)
					return
				}
				_ = json.NewEncoder(w).Encode(protocol.PairResponse{NodeID: "new", CertPEM: string(cert), CAPEM: string(ca.CertPEM)})
			}))
			srv.TLS, err = ca.ServerTLS([]string{"127.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			if _, err := RePair(context.Background(), dir, srv.URL, ca.Fingerprint(), "test-token", "new"); err == nil {
				t.Fatal("accepted invalid replacement credentials")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "node.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("failed replacement changed the active identity")
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "node.key")); !bytes.Equal(got, key) {
				t.Fatal("failed replacement changed the previous private key")
			}
			if dirs, _ := filepath.Glob(filepath.Join(dir, "identity-*")); len(dirs) != 0 {
				t.Fatalf("failed candidate directories remain: %v", dirs)
			}
		})
	}
}

func TestRePairRefusesLegacyRunnerAndUnsafeStatePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node.json"), []byte(`{"nodeId":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", (Paths{dir}).socketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := RePair(context.Background(), dir, "https://unused.invalid", "pin", "token", "name"); err == nil || !strings.Contains(err.Error(), "stop the runner service") {
		t.Fatalf("must refuse a reachable legacy runner before contacting the hub: %v", err)
	}
	for _, state := range []string{"../outside", "/outside", "identity-child/../outside"} {
		if _, err := identityStateDir(dir, Identity{StateDir: state}); err == nil {
			t.Fatalf("accepted unsafe state path %q", state)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "identity-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := identityStateDir(dir, Identity{StateDir: "identity-link"}); err == nil {
		t.Fatal("accepted a symlink identity directory")
	}
}
