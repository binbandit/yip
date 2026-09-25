package main

import (
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
	"github.com/binbandit/yip/internal/buildinfo"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/runner"
	"github.com/binbandit/yip/internal/store"
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
	provs := fs.String("providers", "codex,claude,cursor", "providers to probe on this machine")
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
	Format        string           `json:"format"`
	Version       string           `json:"version"`
	CreatedAt     string           `json:"createdAt"`
	SchemaVersion int              `json:"schemaVersion"`
	DatabaseSHA   string           `json:"databaseSha256"`
	Counts        map[string]int   `json:"counts"`
	Artifacts     []backupArtifact `json:"artifacts"`
	Notes         []string         `json:"notes"`
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
	out := fs.String("out", "", "new directory to write the backup into")
	_ = fs.Parse(args)
	if *out == "" {
		return errors.New("--out is required")
	}
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty; choose a new directory", *out)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*data, "hub.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	dbPath := filepath.Join(*out, "hub.db")
	if err := st.Backup(ctx, dbPath); err != nil {
		return fmt.Errorf("database backup: %w", err)
	}
	for _, sub := range []string{"pki", "hub.key"} {
		if err := copyTree(filepath.Join(*data, sub), filepath.Join(*out, sub)); err != nil {
			return err
		}
	}
	b, err := store.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer b.Close()
	if err := store.IntegrityCheck(ctx, b.R()); err != nil {
		return fmt.Errorf("backup failed verification: %w", err)
	}
	m := backupManifest{Format: "yip-backup v1", Version: buildinfo.Version, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Counts: tableCounts(ctx, b.R()),
		Notes: []string{"Contains the hub CA private key and hub.key: store this backup encrypted.",
			"Provider sign-ins are not included; re-establish them on each runner after a restore."}}
	m.SchemaVersion, _ = b.SchemaVersion(ctx)
	m.DatabaseSHA, _ = fileSHA(dbPath)
	src, _ := hub.NewArtifactStore(filepath.Join(*data, "artifacts"))
	dst, _ := hub.NewArtifactStore(filepath.Join(*out, "artifacts"))
	arts, _ := store.ListArtifacts(ctx, b.R())
	seen := map[string]bool{}
	for _, a := range arts {
		if seen[a.Hash] {
			continue
		}
		seen[a.Hash] = true
		f, err := src.Open(a.Hash)
		if err != nil {
			return fmt.Errorf("artifact %s missing from the live store: %w", a.Hash, err)
		}
		err = dst.Put(f, a.Hash, a.Size)
		f.Close()
		if err != nil {
			return fmt.Errorf("artifact %s: %w", a.Hash, err)
		}
		m.Artifacts = append(m.Artifacts, backupArtifact{Hash: a.Hash, Size: a.Size})
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "backup.json"), mb, 0o600); err != nil {
		return err
	}
	_ = st.Tx(ctx, func(tx *sqlTx) error { return store.SetSetting(ctx, tx, "last_backup_at", m.CreatedAt) })
	fmt.Printf("Backup written to %s (verified: database integrity ok, %d artifacts).\n%s\n", *out, len(m.Artifacts), strings.Join(m.Notes, "\n"))
	return nil
}

// runRestore restores into a separate, new directory and verifies the
// database and every artifact hash before it can be activated.
func runRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	from := fs.String("from", "", "backup directory")
	data := fs.String("data", "", "new, empty hub data directory to restore into")
	_ = fs.Parse(args)
	if *from == "" || *data == "" {
		return errors.New("--from and --data are required")
	}
	if entries, err := os.ReadDir(*data); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty; restore into a new directory, verify it, then point the hub at it", *data)
	}
	raw, err := os.ReadFile(filepath.Join(*from, "backup.json"))
	if err != nil {
		return fmt.Errorf("not a yip backup: %w", err)
	}
	var m backupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if sha, _ := fileSHA(filepath.Join(*from, "hub.db")); sha != m.DatabaseSHA {
		return errors.New("the backup database does not match its recorded checksum")
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		return err
	}
	for _, sub := range []string{"hub.db", "pki", "hub.key", "artifacts"} {
		if err := copyTree(filepath.Join(*from, sub), filepath.Join(*data, sub)); err != nil {
			return err
		}
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(*data, "hub.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.IntegrityCheck(ctx, st.R()); err != nil {
		return err
	}
	as, _ := hub.NewArtifactStore(filepath.Join(*data, "artifacts"))
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
	fmt.Printf("Restored into %s and verified: integrity ok, %d artifacts match their hashes, counts match %v.\n", *data, len(m.Artifacts), counts)
	fmt.Println("Start the hub with --data pointing here. Machines keep their certificates; sign providers in again on runners if needed.")
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
	h, err := hub.Open(ctx, hub.Config{DataDir: *data, Version: buildinfo.Version, Logger: logger()})
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
		h, err := hub.Open(ctx, hub.Config{DataDir: *data, Version: buildinfo.Version, Logger: logger()})
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
		return errors.New("usage: yip forge github add [--data DIR] [--host github.com] [--label L] (token read from stdin)")
	}
	fs := flag.NewFlagSet("forge github add", flag.ExitOnError)
	data := fs.String("data", defaultDataDir(), "hub data directory")
	host := fs.String("host", "github.com", "GitHub host")
	label := fs.String("label", "GitHub", "label shown in audit entries")
	_ = fs.Parse(args[2:])
	token, err := readSecret("GitHub token: ", false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("no token provided")
	}
	ctx := context.Background()
	h, err := hub.Open(ctx, hub.Config{DataDir: *data, Version: buildinfo.Version, Logger: logger()})
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

func runWorkspaces(args []string) error {
	fs := flag.NewFlagSet("runner workspaces", flag.ExitOnError)
	state := fs.String("state", defaultRunnerDir(), "runner state directory")
	_ = fs.Parse(args)
	dir := filepath.Join(*state, "work")
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
		if n := strings.Count(strings.TrimSpace(string(out)), "\n"); len(strings.TrimSpace(string(out))) > 0 {
			status = fmt.Sprintf("DIRTY (%d changes)", n+1)
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
	p := filepath.Join(*state, "work", *name)
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
	_, _ = exec.Command("git", "-C", filepath.Join(*state, "replicas"), "worktree", "prune").Output()
	for _, rep := range globReplicas(*state) {
		_, _ = exec.Command("git", "-C", rep, "worktree", "prune").Output()
	}
	fmt.Println("Removed", p)
	return nil
}

func globReplicas(state string) []string {
	m, _ := filepath.Glob(filepath.Join(state, "replicas", "*.git"))
	return m
}

type sqlTx = sql.Tx
