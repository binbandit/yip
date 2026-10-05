package main

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/backupcrypt"
	"github.com/binbandit/yip/internal/buildinfo"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/internal/store"
	"github.com/google/uuid"
	"golang.org/x/term"
)

type check struct {
	ok     bool
	name   string
	detail string
}

func printChecks(title string, cs []check) bool {
	fmt.Println(title)
	all := true
	for _, c := range cs {
		mark := "✓"
		if !c.ok {
			mark, all = "✗", false
		}
		fmt.Printf("  %s %-28s %s\n", mark, c.name, c.detail)
	}
	return all
}

// ---- doctor ----

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory (skipped if absent)")
	state := fs.String("state", defaultRunnerDir(), "runner state directory (skipped if absent)")
	provs := fs.String("providers", defaultProviders, "providers to probe on this machine")
	_ = fs.Parse(args)
	ctx := context.Background()
	healthy := true
	found := false
	if _, err := os.Stat(filepath.Join(*data, "hub.db")); err == nil {
		found = true
		healthy = printChecks("Hub at "+*data, hubChecks(ctx, *data)) && healthy
	}
	if _, err := os.Stat(filepath.Join(*state, "node.json")); err == nil {
		found = true
		healthy = printChecks("Runner at "+*state, runnerChecks(ctx, *state, *provs)) && healthy
	}
	if !found {
		fmt.Printf("No hub (%s) or runner (%s) found on this machine.\n", *data, *state)
		healthy = printChecks("Providers on this machine", providerChecks(ctx, *provs)) && healthy
	}
	if !healthy {
		return errors.New("some checks failed")
	}
	return nil
}

func hubChecks(ctx context.Context, data string) []check {
	var cs []check
	st, err := store.Open(ctx, filepath.Join(data, "hub.db"))
	if err != nil {
		return append(cs, check{false, "database", err.Error()})
	}
	defer st.Close()
	v, _ := st.SchemaVersion(ctx)
	integ := store.IntegrityCheck(ctx, st.R())
	cs = append(cs, check{integ == nil, "database integrity", fmt.Sprintf("schema v%d %s", v, errOr(integ, "ok"))})
	var users int
	_ = st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users)
	cs = append(cs, check{users > 0, "owner", map[bool]string{true: "configured", false: "not set up yet (run yip hub)"}[users > 0]})
	if ca, err := auth.LoadOrCreateCA(filepath.Join(data, "pki")); err == nil {
		cs = append(cs, check{true, "hub CA", ca.Fingerprint()})
	} else {
		cs = append(cs, check{false, "hub CA", err.Error()})
	}
	if fi, err := os.Stat(filepath.Join(data, "hub.key")); err == nil {
		cs = append(cs, check{fi.Mode().Perm()&0o077 == 0, "hub key permissions", fi.Mode().Perm().String()})
	}
	arts, _ := store.ListArtifacts(ctx, st.R())
	bad := 0
	as, _ := hub.NewArtifactStore(filepath.Join(data, "artifacts"))
	for i, a := range arts {
		if i >= 200 {
			break
		}
		if as.Verify(a.Hash) != nil {
			bad++
		}
	}
	cs = append(cs, check{bad == 0, "artifact hashes", fmt.Sprintf("%d checked, %d bad", min(len(arts), 200), bad)})
	free := diskFreeBytes(data)
	cs = append(cs, check{free == 0 || free > 2<<30, "disk space", fmt.Sprintf("%.1f GB free", float64(free)/(1<<30))})
	if v, ok, _ := store.Setting(ctx, st.R(), "last_backup_at"); ok {
		cs = append(cs, check{true, "last backup", v})
	} else {
		cs = append(cs, check{true, "last backup", "none recorded yet — run yip backup --out <dir>"})
	}
	return cs
}

func runnerChecks(ctx context.Context, state, provs string) []check {
	var cs []check
	id, err := runner.LoadIdentity(state)
	if err != nil {
		return append(cs, check{false, "identity", err.Error()})
	}
	cs = append(cs, check{true, "identity", id.Name + " (" + id.NodeID + ") → " + id.HubURL})
	state, err = runner.ActiveStateDir(state)
	if err != nil {
		return append(cs, check{false, "state directory", err.Error()})
	}
	if b, err := os.ReadFile(filepath.Join(state, "node.pem")); err == nil {
		if cert, err := auth.ParseCertPEM(b); err == nil {
			left := time.Until(cert.NotAfter)
			cs = append(cs, check{left > 14*24*time.Hour, "certificate", "expires " + cert.NotAfter.Format("2006-01-02")})
		}
	}
	if fi, err := os.Stat(filepath.Join(state, "node.key")); err == nil {
		cs = append(cs, check{fi.Mode().Perm()&0o077 == 0, "key permissions", fi.Mode().Perm().String()})
	}
	if j, err := runner.OpenJournal(filepath.Join(state, "journal.db")); err == nil {
		j.Close()
		cs = append(cs, check{true, "journal", "readable"})
	} else {
		cs = append(cs, check{false, "journal", err.Error()})
	}
	_, gitErr := exec.LookPath("git")
	cs = append(cs, check{gitErr == nil, "git", errOr(gitErr, "installed")})
	cs = append(cs, check{true, "service", serviceDescription()})
	return append(cs, providerChecks(ctx, provs)...)
}

func providerChecks(ctx context.Context, provs string) []check {
	ad, err := adapters(provs)
	if err != nil {
		return []check{{false, "providers", err.Error()}}
	}
	var cs []check
	for _, name := range strings.Split(provs, ",") {
		a := ad[strings.TrimSpace(name)]
		if a == nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		inst := a.Probe(pctx)
		cancel()
		detail := inst.AuthState
		if inst.Version != "" {
			detail = inst.Version + " · " + detail
		}
		if inst.AuthDetail != "" {
			detail += " (" + inst.AuthDetail + ")"
		}
		if !inst.Tested && inst.AuthState != "not_installed" {
			detail += " · untested version"
		}
		cs = append(cs, check{inst.AuthState == "ready", a.Label(), detail})
	}
	return cs
}

func serviceDescription() string {
	switch runtime.GOOS {
	case "darwin":
		return "use `yip service install runner` for launchd; check restart after reboot and FileVault unlock"
	case "linux":
		return "use `yip service install runner` for systemd; enable lingering for unattended user services"
	}
	return "not supervised"
}

func errOr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

// ---- backup & restore ----

type backupManifest struct {
	Format        string                    `json:"format"`
	Version       string                    `json:"version"`
	CreatedAt     string                    `json:"createdAt"`
	SchemaVersion int                       `json:"schemaVersion"`
	DatabaseSHA   string                    `json:"databaseSha256"`
	Counts        map[string]int            `json:"counts"`
	Artifacts     []backupArtifact          `json:"artifacts"`
	Notes         []string                  `json:"notes"`
	Workspaces    map[string]backupManifest `json:"workspaces,omitempty"`
}

type backupArtifact struct {
	Hash string `json:"sha256"`
	Size int64  `json:"size"`
}

var countTables = []string{"messages", "jobs", "runs", "reviews", "decisions", "artifacts", "engineers", "rooms", "projects"}

func tableCounts(ctx context.Context, q store.Q) map[string]int {
	out := map[string]int{}
	for _, t := range countTables {
		var n int
		_ = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n)
		out[t] = n
	}
	return out
}

// runBackup writes a transactionally consistent database copy (VACUUM INTO,
// safe while the hub runs), the CA and hub key needed for recovery, and every
// artifact, then verifies the copy. Provider logins are never included.
func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	out := fs.String("out", "", "new directory to write the backup into (with --encrypt: a new file, e.g. hub.yipenc)")
	encrypt := fs.Bool("encrypt", false, "write one passphrase-encrypted file instead of a directory")
	passFile := fs.String("passphrase-file", "", "read the passphrase from this file (default: YIP_BACKUP_PASSPHRASE, or ask)")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("--out is required")
	}
	if !*encrypt {
		if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
			return fmt.Errorf("%s is not empty; choose a new directory", *out)
		}
		m, err := writeBackup(*data, *out)
		if err != nil {
			return err
		}
		fmt.Printf("Backup written to %s (verified: root database, %d root artifacts and %d workspaces).\n%s\n", *out, len(m.Artifacts), len(m.Workspaces), strings.Join(m.Notes, "\n"))
		return nil
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists; choose a new file", *out)
	}
	pass, err := backupPassphrase(*passFile, true)
	if err != nil {
		return err
	}
	// The plain backup is staged in a private temporary directory, then
	// packed and encrypted into one file; the staging copy is removed.
	tmp, err := os.MkdirTemp("", "yip-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	m, err := writeBackup(*data, filepath.Join(tmp, "backup"))
	if err != nil {
		return err
	}
	if err := sealBackup(filepath.Join(tmp, "backup"), *out, pass); err != nil {
		return err
	}
	fmt.Printf("Encrypted backup written to %s (verified before encryption: database integrity ok, %d artifacts).\n", *out, len(m.Artifacts))
	fmt.Println("Keep the passphrase somewhere safe: without it the backup can't be restored.")
	fmt.Println("Provider sign-ins are not included; re-establish them on each runner after a restore.")
	return nil
}

// writeBackup writes a verified plain backup directory.
func writeBackup(data, out string) (backupManifest, error) {
	return writeBackupSnapshot(data, out, true)
}

// Each workspace gets its own online snapshot; only the root owns the CA.
func writeBackupSnapshot(data, out string, root bool) (backupManifest, error) {
	var m backupManifest
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(data, "hub.db")); err != nil {
		return m, err
	}
	st, err := store.Open(ctx, filepath.Join(data, "hub.db"))
	if err != nil {
		return m, err
	}
	defer st.Close()
	if err := os.MkdirAll(out, 0o700); err != nil {
		return m, err
	}
	dbPath := filepath.Join(out, "hub.db")
	if err := st.Backup(ctx, dbPath); err != nil {
		return m, fmt.Errorf("database backup: %w", err)
	}
	secrets := []string{"hub.key"}
	if root {
		secrets = append(secrets, "pki")
	}
	for _, sub := range secrets {
		if err := copyTree(filepath.Join(data, sub), filepath.Join(out, sub)); err != nil {
			return m, err
		}
	}
	b, err := store.Open(ctx, dbPath)
	if err != nil {
		return m, err
	}
	defer b.Close()
	if err := store.IntegrityCheck(ctx, b.R()); err != nil {
		return m, fmt.Errorf("backup failed verification: %w", err)
	}
	m = backupManifest{Format: "yip-backup v1", Version: buildinfo.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Counts: tableCounts(ctx, b.R()),
		Notes: []string{"Contains the hub CA private key and hub.key: store this backup encrypted (yip backup --encrypt).",
			"Provider sign-ins are not included; re-establish them on each runner after a restore."}}
	m.SchemaVersion, _ = b.SchemaVersion(ctx)
	m.DatabaseSHA, _ = fileSHA(dbPath)
	src, _ := hub.NewArtifactStore(filepath.Join(data, "artifacts"))
	dst, _ := hub.NewArtifactStore(filepath.Join(out, "artifacts"))
	arts, _ := store.ListArtifacts(ctx, b.R())
	seen := map[string]bool{}
	for _, a := range arts {
		if seen[a.Hash] {
			continue
		}
		seen[a.Hash] = true
		f, err := src.Open(a.Hash)
		if err != nil {
			return m, fmt.Errorf("artifact %s missing from the live store: %w", a.Hash, err)
		}
		err = dst.Put(f, a.Hash, a.Size)
		f.Close()
		if err != nil {
			return m, fmt.Errorf("artifact %s: %w", a.Hash, err)
		}
		m.Artifacts = append(m.Artifacts, backupArtifact{Hash: a.Hash, Size: a.Size})
	}
	if root {
		entries, err := os.ReadDir(filepath.Join(data, "workspaces"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return m, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue // Workspace creation is committed by renaming its staging directory.
			}
			if !entry.IsDir() || !backupWorkspaceID(entry.Name()) {
				return m, fmt.Errorf("unexpected workspace entry %q", entry.Name())
			}
			child, err := writeBackupSnapshot(filepath.Join(data, "workspaces", entry.Name()), filepath.Join(out, "workspaces", entry.Name()), false)
			if err != nil {
				return m, fmt.Errorf("workspace %s: %w", entry.Name(), err)
			}
			if m.Workspaces == nil {
				m.Workspaces = map[string]backupManifest{}
			}
			m.Workspaces[entry.Name()] = child
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "backup.json"), mb, 0o600); err != nil {
		return m, err
	}
	_ = st.Tx(ctx, func(tx *sql.Tx) error { return store.SetSetting(ctx, tx, "last_backup_at", m.CreatedAt) })
	return m, nil
}

func backupWorkspaceID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

// backupPassphrase reads the passphrase from a file, YIP_BACKUP_PASSPHRASE,
// or the terminal (asked twice for a new backup).
func backupPassphrase(file string, confirm bool) ([]byte, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		return []byte(strings.TrimRight(string(b), "\r\n")), nil
	}
	if v := os.Getenv("YIP_BACKUP_PASSPHRASE"); v != "" {
		return []byte(v), nil
	}
	p, err := readSecret("Backup passphrase: ", false)
	if err != nil {
		return nil, err
	}
	if confirm {
		again, err := readSecret("Repeat it: ", false)
		if err != nil {
			return nil, err
		}
		if again != p {
			return nil, errors.New("the passphrases don't match")
		}
	}
	return []byte(p), nil
}

// sealBackup packs a backup directory as a tar stream and encrypts it into
// out (written to a temporary name, then moved into place).
func sealBackup(dir, out string, pass []byte) error {
	tmp := out + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	w, err := backupcrypt.NewWriter(f, pass)
	if err != nil {
		f.Close()
		return err
	}
	tw := tar.NewWriter(w)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	for _, c := range []io.Closer{tw, w, f} {
		if cerr := c.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

// openBackup decrypts an encrypted backup file into a private temporary
// directory and returns it (the caller removes it).
func openBackup(file string, pass []byte) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r, err := backupcrypt.NewReader(f, pass)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "yip-restore-*")
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return tmp, nil
		}
		if err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			os.RemoveAll(tmp)
			return "", fmt.Errorf("the backup contains an unsafe path %q", hdr.Name)
		}
		dest := filepath.Join(tmp, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dest, 0o700)
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(dest), 0o700); err == nil {
				var out *os.File
				if out, err = os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
					_, err = io.Copy(out, tr)
					if cerr := out.Close(); err == nil {
						err = cerr
					}
				}
			}
		default:
			err = fmt.Errorf("the backup contains an unexpected entry %q", hdr.Name)
		}
		if err != nil {
			os.RemoveAll(tmp)
			return "", err
		}
	}
}

// runRestore restores into a separate, new directory and verifies the
// database and every artifact hash before it can be activated.
func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	from := fs.String("from", "", "backup directory, or an encrypted backup file")
	data := fs.String("data", "", "new, empty hub data directory to restore into")
	passFile := fs.String("passphrase-file", "", "for an encrypted backup: read the passphrase from this file (default: YIP_BACKUP_PASSPHRASE, or ask)")
	_ = fs.Parse(args)
	if *from == "" || *data == "" {
		return errors.New("--from and --data are required")
	}
	if entries, err := os.ReadDir(*data); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty; restore into a new directory, verify it, then point the hub at it", *data)
	}
	if fi, err := os.Stat(*from); err == nil && fi.Mode().IsRegular() {
		f, err := os.Open(*from)
		if err != nil {
			return err
		}
		enc := backupcrypt.IsEncrypted(f)
		f.Close()
		if !enc {
			return fmt.Errorf("%s is neither a backup directory nor an encrypted yip backup", *from)
		}
		pass, err := backupPassphrase(*passFile, false)
		if err != nil {
			return err
		}
		dir, err := openBackup(*from, pass)
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		*from = dir
	}
	raw, err := os.ReadFile(filepath.Join(*from, "backup.json"))
	if err != nil {
		return fmt.Errorf("not a yip backup: %w", err)
	}
	var m backupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if err := restoreBackupSnapshot(*from, *data, m, true); err != nil {
		return err
	}
	fmt.Printf("Restored into %s and verified: database integrity, artifact hashes and counts match (root and %d workspaces).\n", *data, len(m.Workspaces))
	fmt.Println("Start the hub with --data pointing here. Machines keep their certificates; sign providers in again on runners if needed.")
	return nil
}

func restoreBackupSnapshot(from, data string, m backupManifest, root bool) error {
	if m.Format != "yip-backup v1" {
		return fmt.Errorf("unsupported backup format %q", m.Format)
	}
	if !root && len(m.Workspaces) != 0 {
		return errors.New("nested workspaces are not supported")
	}
	for id := range m.Workspaces {
		if !backupWorkspaceID(id) {
			return fmt.Errorf("invalid backup workspace ID %q", id)
		}
	}
	if sha, err := fileSHA(filepath.Join(from, "hub.db")); err != nil || sha != m.DatabaseSHA {
		return errors.New("the backup database does not match its recorded checksum")
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	files := []string{"hub.db", "hub.key", "artifacts"}
	if root {
		files = append(files, "pki")
	}
	for _, sub := range files {
		if err := copyTree(filepath.Join(from, sub), filepath.Join(data, sub)); err != nil {
			return err
		}
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(data, "hub.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.IntegrityCheck(ctx, st.R()); err != nil {
		return err
	}
	as, _ := hub.NewArtifactStore(filepath.Join(data, "artifacts"))
	for _, a := range m.Artifacts {
		if err := as.Verify(a.Hash); err != nil {
			return fmt.Errorf("restored artifact failed verification: %w", err)
		}
	}
	counts := tableCounts(ctx, st.R())
	for k, v := range m.Counts {
		if counts[k] != v {
			return fmt.Errorf("restored %s count %d differs from the backup's %d", k, counts[k], v)
		}
	}
	for id, child := range m.Workspaces {
		if err := restoreBackupSnapshot(filepath.Join(from, "workspaces", id), filepath.Join(data, "workspaces", id), child, false); err != nil {
			return fmt.Errorf("workspace %s: %w", id, err)
		}
	}
	return nil
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode().Perm())
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			if filepath.Base(p) == "tmp" {
				return fs.SkipDir
			}
			return os.MkdirAll(target, 0o700)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, target, fi.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- owner recovery & credentials ----

func readSecret(prompt string, stdin bool) (string, error) {
	if stdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func runOwner(args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		return errors.New("usage: yip owner reset-password --handle HANDLE [--data DIR] [--password-stdin]")
	}
	fs := flag.NewFlagSet("owner reset-password", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory (requires access to the hub host)")
	handle := fs.String("handle", "", "owner handle")
	fromStdin := fs.Bool("password-stdin", false, "read the new password from stdin")
	_ = fs.Parse(args[1:])
	if *handle == "" {
		return errors.New("--handle is required")
	}
	pw, err := readSecret("New password: ", *fromStdin)
	if err != nil {
		return err
	}
	if !*fromStdin && term.IsTerminal(int(os.Stdin.Fd())) {
		again, _ := readSecret("Repeat: ", false)
		if again != pw {
			return errors.New("passwords don't match")
		}
	}
	ctx := context.Background()
	h, err := openHub(ctx, *data)
	if err != nil {
		return err
	}
	defer h.Close()
	if err := h.ResetOwnerPassword(ctx, *handle, pw); err != nil {
		return err
	}
	fmt.Println("Password reset. Every existing session was revoked.")
	return nil
}

func runForge(args []string) error {
	if len(args) >= 2 && args[0] == "github" && args[1] == "webhook-secret" {
		fs := flag.NewFlagSet("forge github webhook-secret", flag.ExitOnError)
		data := fs.String("data", defaultDataDir(), "hub data directory")
		_ = fs.Parse(args[2:])
		ctx := context.Background()
		h, err := openHub(ctx, *data)
		if err != nil {
			return err
		}
		defer h.Close()
		secret := domain.RandomToken(32)
		if err := h.SetWebhookSecret(ctx, "github", secret); err != nil {
			return err
		}
		fmt.Printf("Webhook secret (configure it on the repository's webhook, content type application/json):\n%s\n\nPayload URL: https://<your hub>/v1/forge/github/webhook\nEvents: pull requests, pull request reviews, check runs, check suites, statuses.\n", secret)
		return nil
	}
	if len(args) < 2 || args[0] != "github" || args[1] != "add" {
		return errors.New("usage: yip forge github add [--data DIR] [--host github.com] [--label L] [--from-gh] (token read from stdin, or from the GitHub CLI with --from-gh)")
	}
	fs := flag.NewFlagSet("forge github add", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	host := fs.String("host", "github.com", "GitHub host")
	label := fs.String("label", "GitHub", "label shown in audit entries")
	fromGH := fs.Bool("from-gh", false, "store the token the GitHub CLI is signed in with on this machine (gh auth token)")
	_ = fs.Parse(args[2:])
	ctx := context.Background()
	var token string
	var err error
	if *fromGH {
		if token, err = github.CLIToken(ctx, *host); err != nil {
			return fmt.Errorf("%w; sign in with `gh auth login --hostname %s` first", err, *host)
		}
	} else if token, err = readSecret("GitHub token: ", false); err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("no token provided")
	}
	h, err := openHub(ctx, *data)
	if err != nil {
		return err
	}
	defer h.Close()
	if err := h.PutForgeToken(ctx, "github", *host, *label, token); err != nil {
		return err
	}
	fmt.Println("Stored (sealed with the hub key). Engineers can publish reviews only on projects that grant publish_review.")
	return nil
}

// ---- runner workspaces ----

func activeRunnerState(state string) (string, error) {
	// Historical/unpaired directories can still be inspected explicitly.
	if _, err := os.Stat(filepath.Join(state, "node.json")); os.IsNotExist(err) {
		return state, nil
	}
	return runner.ActiveStateDir(state)
}

func runWorkspaces(args []string) error {
	fs := flag.NewFlagSet("runner workspaces", flag.ExitOnError)
	state := fs.String("state", defaultRunnerDir(), "runner state directory")
	_ = fs.Parse(args)
	active, err := activeRunnerState(*state)
	if err != nil {
		return err
	}
	dir := filepath.Join(active, "work")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		out, _ := exec.Command("git", "-C", p, "status", "--porcelain").Output()
		branch, _ := exec.Command("git", "-C", p, "branch", "--show-current").Output()
		status := "clean"
		if st := strings.TrimSpace(string(out)); st != "" {
			status = fmt.Sprintf("DIRTY (%d changes)", strings.Count(st, "\n")+1)
		}
		fmt.Printf("%-24s %-40s %s\n", e.Name(), strings.TrimSpace(string(branch)), status)
	}
	return nil
}

// runCleanup deletes one workspace only on explicit selection and
// confirmation. Unreviewed or dirty work is never removed automatically.
func runCleanup(args []string) error {
	flags := flag.NewFlagSet("runner cleanup", flag.ExitOnError)
	state := flags.String("state", defaultRunnerDir(), "runner state directory")
	name := flags.String("workspace", "", "workspace name from `yip runner workspaces`")
	confirm := flags.String("confirm", "", "repeat the workspace name to confirm deletion")
	_ = flags.Parse(args)
	if *name == "" || *confirm != *name || strings.ContainsAny(*name, "/\\") {
		return errors.New("choose a workspace with --workspace and repeat it with --confirm; this permanently deletes its files, including uncommitted work")
	}
	active, err := activeRunnerState(*state)
	if err != nil {
		return err
	}
	p := filepath.Join(active, "work", *name)
	if _, err := os.Stat(p); err != nil {
		return err
	}
	out, _ := exec.Command("git", "-C", p, "status", "--porcelain").Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Printf("%s has uncommitted changes that will be lost:\n%s\n", *name, out)
	}
	_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	_, _ = exec.Command("git", "-C", filepath.Join(active, "replicas"), "worktree", "prune").Output()
	for _, rep := range globReplicas(active) {
		_, _ = exec.Command("git", "-C", rep, "worktree", "prune").Output()
	}
	fmt.Println("Removed", p)
	return nil
}

func globReplicas(state string) []string {
	m, _ := filepath.Glob(filepath.Join(state, "replicas", "*.git"))
	return m
}
