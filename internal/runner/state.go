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
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

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
	// StateDir selects a fresh identity's storage without moving old worktrees.
	// Empty is the original layout, whose files live alongside node.json.
	StateDir string `json:"stateDir,omitempty"`
	// StateRoot marks generated storage as belonging to its parent root. It
	// must never be opened as an independent runner or pairing destination.
	StateRoot string `json:"stateRoot,omitempty"`
	// Prior labels remain discoverable until startup confirms Docker cleanup.
	PreviousNodeIDs       []string `json:"previousNodeIds,omitempty"`
	PreviousStateDirs     []string `json:"previousStateDirs,omitempty"`
	DockerCleanupRequired bool     `json:"dockerCleanupRequired,omitempty"`
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
	if err := json.Unmarshal(b, &id); err != nil {
		return id, err
	}
	return id, nil
}

// ActiveStateDir returns the active identity's credentials, journal and work
// directory. Re-pairing leaves previous directories and their worktrees intact.
func ActiveStateDir(dir string) (string, error) {
	id, err := LoadIdentity(dir)
	if err != nil {
		return "", err
	}
	return identityStateDir(dir, id)
}

func identityStateDir(dir string, id Identity) (string, error) {
	if id.StateDir == "" {
		return dir, nil
	}
	if filepath.Base(id.StateDir) != id.StateDir || !strings.HasPrefix(id.StateDir, "identity-") {
		return "", errors.New("invalid runner identity state directory")
	}
	p := filepath.Join(dir, id.StateDir)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("runner identity state must be a local directory")
	}
	return p, nil
}

// The lock is never unlinked: all runner and pairing processes must lock the
// same inode. Closing it releases the lock, including after a process crash.
func lockState(dir string) (*os.File, error) {
	if _, err := os.Stat((Paths{dir}).identity()); err == nil {
		id, err := LoadIdentity(dir)
		if err != nil {
			return nil, err
		}
		if id.StateRoot != "" {
			return nil, errors.New("this is generated identity storage; use the owning runner state directory, not an identity-* subdirectory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".runner.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("runner state is in use; stop the runner service or wait for pairing to finish: %w", err)
	}
	return f, nil
}

// Pair generates this node's key locally, redeems the single-use enrollment
// token against a hub whose CA fingerprint is pinned, and stores the issued
// certificate. The private key never leaves this machine.
func Pair(ctx context.Context, dir, hubURL, fingerprint, token, name string) (Identity, error) {
	return pair(ctx, dir, hubURL, fingerprint, token, name, false)
}

// RePair explicitly enrolls a new node with a fresh token. It does not change
// the revoked node's authority or reuse its journal, credentials or workspaces.
func RePair(ctx context.Context, dir, hubURL, fingerprint, token, name string) (Identity, error) {
	return pair(ctx, dir, hubURL, fingerprint, token, name, true)
}

func pair(ctx context.Context, dir, hubURL, fingerprint, token, name string, replace bool) (Identity, error) {
	p := Paths{dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	lock, err := lockState(dir)
	if err != nil {
		return Identity{}, err
	}
	defer lock.Close()
	previous, err := os.ReadFile(p.identity())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	if previous != nil {
		if !replace {
			return Identity{}, fmt.Errorf("%s is already paired; to reconnect after revocation, stop the runner and add --replace to a fresh pairing command from Machines → Add machine (previous work is preserved)", dir)
		}
		active, err := ActiveStateDir(dir)
		if err != nil {
			return Identity{}, err
		}
		// Older runner versions do not hold the state lock. Refuse replacement
		// while their bridge socket is reachable too.
		c, err := net.DialTimeout("unix", (Paths{active}).socketPath(), time.Second)
		if err == nil {
			c.Close()
			return Identity{}, errors.New("stop the runner service before replacing its pairing")
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ECONNREFUSED) {
			return Identity{}, fmt.Errorf("cannot confirm that the previous runner is stopped: %w", err)
		}
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
	id := Identity{NodeID: pr.NodeID, Name: name, HubURL: hubURL, Fingerprint: fingerprint,
		PairedAt: time.Now().UTC().Format(time.RFC3339)}
	if previous == nil {
		return saveIdentity(p, id, keyPEM, []byte(pr.CertPEM), []byte(pr.CAPEM))
	}
	var old Identity
	if err := json.Unmarshal(previous, &old); err != nil {
		return Identity{}, err
	}
	id.PreviousNodeIDs = append(old.PreviousNodeIDs, old.NodeID)
	id.PreviousStateDirs = append(old.PreviousStateDirs, old.StateDir)
	// Older toolchain-only probes did not create staging directories. When
	// Docker is installed, inspect prior labels even without that evidence.
	id.DockerCleanupRequired = old.DockerCleanupRequired || dockerCLIAvailable()
	active, err := identityStateDir(dir, old)
	if err != nil {
		return Identity{}, err
	}
	// Docker providers and shell checks stage imports before creating their
	// containers. Preserve that cleanup obligation even if the new runner
	// selects a native execution profile or is replaced again before startup.
	if _, err := os.Stat(filepath.Join(active, "container-imports")); err == nil {
		id.DockerCleanupRequired = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return Identity{}, err
	}
	// Prepare a complete fresh identity before atomically switching node.json.
	// Old paths stay stable, including Git worktree links and unpublished files.
	fresh, err := os.MkdirTemp(dir, "identity-")
	if err != nil {
		return Identity{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(fresh)
		}
	}()
	child := id
	child.StateRoot = ".."
	if _, err := saveIdentity(Paths{fresh}, child, keyPEM, []byte(pr.CertPEM), []byte(pr.CAPEM)); err != nil {
		return Identity{}, err
	}
	if err := os.WriteFile(filepath.Join(fresh, "previous-node.json"), previous, 0o600); err != nil {
		return Identity{}, err
	}
	id.StateDir = filepath.Base(fresh)
	if err := writeIdentity(p.identity(), id); err != nil {
		return Identity{}, err
	}
	committed = true
	return id, nil
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
	if _, err := auth.ClientTLS(caPEM, certPEM, keyPEM, ""); err != nil {
		return id, fmt.Errorf("invalid pairing credentials: %w", err)
	}
	cert, err := auth.ParseCertPEM(certPEM)
	if err != nil {
		return id, err
	}
	if node, _ := auth.NodeIDFromCert(cert); node != id.NodeID || node == "" {
		return id, errors.New("the hub returned a certificate for a different node")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return id, fmt.Errorf("invalid pairing certificate: %w", err)
	}
	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{{p.key(), keyPEM, 0o600}, {p.cert(), certPEM, 0o644}, {p.ca(), caPEM, 0o644}} {
		if err := os.WriteFile(f.path, f.data, f.mode); err != nil {
			return id, err
		}
	}
	return id, writeIdentity(p.identity(), id)
}

func writeIdentity(path string, id Identity) error {
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".node-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
