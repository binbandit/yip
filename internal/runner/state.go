// Package runner implements the yip runner: it pairs with a hub, connects
// outbound over mutual TLS, journals accepted work, creates isolated
// workspaces, supervises provider processes, exposes the yip bridge to them,
// and reports events and evidence back to the hub.
//
// The runner never makes organisation-level authorization decisions; it
// executes what its lease allows and asks the hub for everything else.
package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/protocol"
)

// Identity is the paired node's persistent state.
type Identity struct {
	NodeID      string `json:"nodeId"`
	Name        string `json:"name"`
	HubURL      string `json:"hubUrl"`
	Fingerprint string `json:"fingerprint"`
	PairedAt    string `json:"pairedAt"`
}

// Paths lays out a runner state directory.
type Paths struct{ Dir string }

func (p Paths) identity() string { return filepath.Join(p.Dir, "node.json") }
func (p Paths) key() string      { return filepath.Join(p.Dir, "node.key") }
func (p Paths) cert() string     { return filepath.Join(p.Dir, "node.pem") }
func (p Paths) ca() string       { return filepath.Join(p.Dir, "hub-ca.pem") }
func (p Paths) journal() string  { return filepath.Join(p.Dir, "journal.db") }
func (p Paths) replicas() string { return filepath.Join(p.Dir, "replicas") }
func (p Paths) work() string     { return filepath.Join(p.Dir, "work") }
func (p Paths) logs() string     { return filepath.Join(p.Dir, "logs") }

// socketPath returns a short unix socket path (macOS limits socket paths to
// 104 bytes, and TMPDIR there is long).
func (p Paths) socketPath() string {
	sum := sha256.Sum256([]byte(p.Dir))
	dir := filepath.Join("/tmp", fmt.Sprintf("yip-%d", os.Getuid()))
	_ = os.MkdirAll(dir, 0o700)
	_ = os.Chmod(dir, 0o700)
	return filepath.Join(dir, hex.EncodeToString(sum[:6])+".sock")
}

// LoadIdentity reads a paired identity.
func LoadIdentity(dir string) (Identity, error) {
	var id Identity
	b, err := os.ReadFile(Paths{dir}.identity())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return id, fmt.Errorf("this machine isn't paired yet (no %s). Run `yip runner pair` with a token from Machines → Add machine", Paths{dir}.identity())
		}
		return id, err
	}
	return id, json.Unmarshal(b, &id)
}

// Pair generates this node's key locally, redeems the single-use enrollment
// token against a hub whose CA fingerprint is pinned, and stores the issued
// certificate. The private key never leaves this machine.
func Pair(ctx context.Context, dir, hubURL, fingerprint, token, name string) (Identity, error) {
	p := Paths{dir}
	if _, err := os.Stat(p.identity()); err == nil {
		return Identity{}, fmt.Errorf("%s is already paired; use a different --state directory or remove it after revoking the machine", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	hubURL = strings.TrimRight(hubURL, "/")
	if !strings.HasPrefix(hubURL, "https://") {
		return Identity{}, errors.New("--hub must be the https:// runner URL shown in the pairing command")
	}
	if name == "" {
		name, _ = os.Hostname()
	}
	keyPEM, csrPEM, err := auth.NewNodeKeyAndCSR(name)
	if err != nil {
		return Identity{}, err
	}
	host, _ := os.Hostname()
	body, _ := json.Marshal(protocol.PairRequest{Token: strings.TrimSpace(token), CSRPEM: string(csrPEM), Name: name, Hostname: host,
		OS: runtime.GOOS, Arch: runtime.GOARCH})
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: auth.PinnedTLS(fingerprint)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hubURL+"/v1/nodes/pair", bytes.NewReader(body))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("reach hub: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		var apiErr protocol.APIError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Message != "" {
			return Identity{}, errors.New(apiErr.Message)
		}
		return Identity{}, fmt.Errorf("pairing failed: HTTP %d", resp.StatusCode)
	}
	var pr protocol.PairResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return Identity{}, err
	}
	return saveIdentity(p, Identity{NodeID: pr.NodeID, Name: name, HubURL: hubURL, Fingerprint: fingerprint,
		PairedAt: time.Now().UTC().Format(time.RFC3339)}, keyPEM, []byte(pr.CertPEM), []byte(pr.CAPEM))
}

// SaveLocalIdentity stores credentials obtained through in-process pairing.
func SaveLocalIdentity(dir string, id Identity, keyPEM, certPEM, caPEM []byte) (Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return id, err
	}
	return saveIdentity(Paths{dir}, id, keyPEM, certPEM, caPEM)
}

func saveIdentity(p Paths, id Identity, keyPEM, certPEM, caPEM []byte) (Identity, error) {
	ca, err := auth.ParseCertPEM(caPEM)
	if err != nil {
		return id, err
	}
	if id.Fingerprint != "" && auth.CertFingerprint(ca) != id.Fingerprint {
		return id, errors.New("the hub returned a CA that doesn't match the pinned fingerprint")
	}
	id.Fingerprint = auth.CertFingerprint(ca)
	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{{p.key(), keyPEM, 0o600}, {p.cert(), certPEM, 0o644}, {p.ca(), caPEM, 0o644}} {
		if err := os.WriteFile(f.path, f.data, f.mode); err != nil {
			return id, err
		}
	}
	b, _ := json.MarshalIndent(id, "", "  ")
	return id, os.WriteFile(p.identity(), b, 0o600)
}
