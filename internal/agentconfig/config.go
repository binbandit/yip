// Package agentconfig imports an explicit, bounded subset of local harness
// configuration into a disposable Linux home. It never queries a keychain or
// extracts credentials. Keychain-only logins are not portable: supply an
// explicitly selected API/token environment variable or sign in using the
// provider's supported file-backed flow. Readiness is checked by the provider
// inside the container. Imported MCP commands and skills are user-selected code;
// their host executables and absolute paths are not made portable.
//
// The allowlist is: shared ~/.agents/skills; Codex auth.json, skills and
// config.toml (MCP/model/provider fields only); file-backed .credentials.json, skills
// and the global .claude.json mcpServers registry; Cursor skills and mcp.json.
// Cursor's local login store is not a documented portable credential format,
// so use an explicitly selected CURSOR_API_KEY or CURSOR_AUTH_TOKEN instead.
// File-backed login is conditional on the installed CLI storing it;
// it does not import a macOS keychain login. No sessions, history, rules,
// hooks, plugins, permission grants or account metadata are imported.
package agentconfig

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

const (
	HOME               = "/home/yip"
	Home               = HOME
	MaxFiles           = 4096
	MaxBytes     int64 = 64 << 20
	MaxFileBytes int64 = 8 << 20
)

// Snapshot is a private home tree and explicit KEY=VALUE environment entries.
// The caller must remove Dir and must never log Env or write it to shared files.
type Snapshot struct {
	Dir string
	Env []string
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Stage imports only the selected provider and shared ~/.agents/skills. Missing
// files are normal. Symlinks (including parent directories), sockets and other
// special files are refused, not silently skipped; materialize skills as regular
// files before importing. Config JSON/TOML is rebuilt from allowlisted fields,
// excluding hooks, rules, permission grants, history, caches and helper commands.
// Environment values are never interpolated into errors.
func Stage(provider string, extraEnv []string) (snap Snapshot, err error) {
	return StageIn("", provider, "", extraEnv)
}

// StageIn uses a caller-owned private staging directory so a supervising runner
// can reap abandoned imports after a crash. Empty parent uses the OS temp dir.
// When workspace is nonempty, only that exact host workspace's local MCP scope
// is selected and remapped to /workspace. Probes have no selected workspace.
func StageIn(parent, provider, workspace string, extraEnv []string) (snap Snapshot, err error) {
	if workspace != "" {
		if !filepath.IsAbs(workspace) {
			return snap, errors.New("agent config: selected workspace must be absolute")
		}
		workspace = filepath.Clean(workspace)
	}
	switch provider {
	case "claude", "codex", "cursor", "fake":
	default:
		return snap, errors.New("agent config: unsupported provider")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return snap, errors.New("agent config: cannot locate home")
	}
	snap.Env = []string{"HOME=" + HOME, "XDG_CONFIG_HOME=" + HOME + "/.config"}
	seen := map[string]bool{}
	for _, key := range extraEnv {
		if !envName.MatchString(key) || reserved(key) {
			return Snapshot{}, errors.New("agent config: invalid or reserved environment key")
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if value, ok := os.LookupEnv(key); ok {
			if strings.ContainsAny(value, "\x00\r\n") {
				return Snapshot{}, errors.New("agent config: environment values must be single-line")
			}
			snap.Env = append(snap.Env, key+"="+value)
		}
	}
	snap.Dir, err = os.MkdirTemp(parent, "yip-agent-config-")
	if err != nil {
		return Snapshot{}, errors.New("agent config: cannot create private snapshot")
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(snap.Dir)
			snap = Snapshot{}
		}
	}()
	if provider == "fake" {
		return
	}
	b := &builder{dir: snap.Dir}
	if err = b.tree(filepath.Join(home, ".agents", "skills"), ".agents/skills"); err != nil {
		return
	}
	switch provider {
	case "codex":
		src := configHome("CODEX_HOME", filepath.Join(home, ".codex"))
		snap.Env = append(snap.Env, "CODEX_HOME="+HOME+"/.codex")
		if err = b.file(filepath.Join(src, "auth.json"), ".codex/auth.json"); err != nil {
			return
		}
		if err = b.tree(filepath.Join(src, "skills"), ".codex/skills"); err != nil {
			return
		}
		err = b.config(filepath.Join(src, "config.toml"), ".codex/config.toml", true,
			"mcp_servers", "model", "model_provider", "model_providers")
	case "claude":
		src := configHome("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		// The file-backed login is copied as an opaque file, never parsed.
		if err = b.file(filepath.Join(src, ".credentials.json"), ".claude/.credentials.json"); err != nil {
			return
		}
		if err = b.tree(filepath.Join(src, "skills"), ".claude/skills"); err != nil {
			return
		}
		// The global MCP registry lives beside the default config directory.
		registry := filepath.Join(home, ".claude.json")
		destination := ".claude.json"
		if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
			snap.Env = append(snap.Env, "CLAUDE_CONFIG_DIR="+HOME+"/.claude")
			registry = filepath.Join(src, ".claude.json")
			destination = ".claude/.claude.json"
		}
		err = b.registry(registry, destination, workspace)
	case "cursor":
		src := os.Getenv("CURSOR_CONFIG_DIR")
		if src == "" {
			if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
				src = filepath.Join(xdg, "cursor")
			} else {
				src = filepath.Join(home, ".cursor")
			}
		}
		snap.Env = append(snap.Env, "CURSOR_CONFIG_DIR="+HOME+"/.cursor")
		if err = b.tree(filepath.Join(src, "skills"), ".cursor/skills"); err != nil {
			return
		}
		// Compatibility skill locations contain code, not the other
		// providers' authentication or configuration.
		for _, name := range []string{".claude", ".codex"} {
			if err = b.tree(filepath.Join(home, name, "skills"), name+"/skills"); err != nil {
				return
			}
		}
		err = b.config(filepath.Join(src, "mcp.json"), ".cursor/mcp.json", false, "mcpServers")
	}
	return
}

func configHome(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func reserved(key string) bool {
	switch key {
	case "HOME", "PATH", "USER", "LOGNAME", "SHELL", "TMPDIR", "XDG_CONFIG_HOME",
		"XDG_RUNTIME_DIR", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "CURSOR_CONFIG_DIR",
		"SSH_AUTH_SOCK", "SSH_AGENT_PID", "DOCKER_HOST", "DOCKER_CONTEXT",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "NODE_OPTIONS", "PYTHONPATH":
		return true
	}
	return strings.HasPrefix(key, "YIP_")
}

type builder struct {
	dir     string
	files   int
	bytes   int64
	entries int
}

// openSelected walks descriptor-relative components without following symlinks,
// including ancestors. A concurrent rename cannot redirect an opened directory.
func openSelected(path string, directory bool) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("agent config: config home must be absolute")
	}
	f, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for i, part := range parts {
		next, err := openChild(f, part, directory || i < len(parts)-1)
		f.Close()
		if err != nil {
			return nil, err
		}
		f = next
	}
	return f, nil
}

func openChild(parent *os.File, name string, directory bool) (*os.File, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil, errors.New("agent config: symlink import refused; materialize the selected config or skills tree as regular files")
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG && st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, errors.New("agent config: special-file import refused")
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if errors.Is(err, unix.ELOOP) {
		return nil, errors.New("agent config: symlink import refused; materialize the selected config or skills tree as regular files")
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (b *builder) read(path string) ([]byte, error) {
	f, err := openSelected(path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return b.readFile(f)
}

func (b *builder) readFile(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("agent config: special-file import refused")
	}
	b.files++
	if b.files > MaxFiles || info.Size() > MaxFileBytes {
		return nil, errors.New("agent config: import size/count limit exceeded")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, errors.New("agent config: cannot read selected file")
	}
	b.bytes += int64(len(data))
	if int64(len(data)) > MaxFileBytes || b.bytes > MaxBytes {
		return nil, errors.New("agent config: import size/count limit exceeded")
	}
	return data, nil
}

func (b *builder) write(dst string, data []byte) error {
	path := filepath.Join(b.dir, dst)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("agent config: cannot create snapshot directory")
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return errors.New("agent config: cannot write snapshot")
	}
	return nil
}

func (b *builder) file(src, dst string) error {
	data, err := b.read(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return safeError(err)
	}
	return b.write(dst, data)
}

func safeError(err error) error {
	if strings.HasPrefix(err.Error(), "agent config:") {
		return err
	}
	return errors.New("agent config: cannot import selected file or directory")
}

func (b *builder) tree(src, dst string) error {
	r, err := openSelected(src, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return safeError(err)
	}
	defer r.Close()
	return b.copyTree(r, dst, 0)
}

func (b *builder) copyTree(dir *os.File, dst string, depth int) error {
	if depth > 64 {
		return errors.New("agent config: skills directory nesting limit exceeded")
	}
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			return safeError(err)
		}
		for _, entry := range entries {
			b.entries++
			if b.entries > MaxFiles {
				return errors.New("agent config: import size/count limit exceeded")
			}
			child, err := openChild(dir, entry.Name(), entry.IsDir())
			if err != nil {
				return safeError(err)
			}
			target := filepath.Join(dst, entry.Name())
			if entry.IsDir() {
				err = b.copyTree(child, target, depth+1)
			} else {
				var data []byte
				data, err = b.readFile(child)
				if err == nil {
					err = b.write(target, data)
				}
				if err == nil {
					var info os.FileInfo
					info, err = child.Stat()
					if err == nil && info.Mode().Perm()&0111 != 0 {
						err = os.Chmod(filepath.Join(b.dir, target), 0700)
					}
				}
			}
			child.Close()
			if err != nil {
				return safeError(err)
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

func (b *builder) readConfig(src string, isTOML bool) (map[string]any, error) {
	data, err := b.read(src)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return nil, safeError(err)
	}
	all := map[string]any{}
	if len(data) > 0 {
		if isTOML {
			err = toml.Unmarshal(data, &all)
		} else {
			err = json.Unmarshal(data, &all)
		}
		if err != nil {
			return nil, errors.New("agent config: invalid selected config; fix its JSON/TOML syntax before importing")
		}
	}
	return all, nil
}

func (b *builder) config(src, dst string, isTOML bool, keys ...string) error {
	all, err := b.readConfig(src, isTOML)
	if err != nil {
		return err
	}
	selected := map[string]any{}
	for _, k := range keys {
		if v, ok := all[k]; ok {
			selected[k] = v
		}
	}
	sanitizeMCP(selected)
	var data []byte
	if isTOML {
		selected["cli_auth_credentials_store"] = "file"
		data, err = toml.Marshal(selected)
	} else {
		data, err = json.Marshal(selected)
	}
	if err != nil {
		return errors.New("agent config: cannot encode selected configuration")
	}
	return b.write(dst, data)
}

// registry preserves user and selected local scopes, never a whole projects
// record: those records also contain trust grants and unrelated project data.
func (b *builder) registry(src, dst, workspace string) error {
	all, err := b.readConfig(src, false)
	if err != nil {
		return err
	}
	selected := map[string]any{}
	if servers, ok := all["mcpServers"]; ok {
		selected["mcpServers"] = servers
	}
	sanitizeMCP(selected)
	if projects, ok := all["projects"].(map[string]any); ok && workspace != "" {
		if project, ok := projects[workspace].(map[string]any); ok {
			if servers, ok := project["mcpServers"].(map[string]any); ok {
				scoped := map[string]any{"mcpServers": servers}
				sanitizeMCP(scoped)
				selected["projects"] = map[string]any{"/workspace": scoped}
			}
		}
	}
	data, err := json.Marshal(selected)
	if err != nil {
		return errors.New("agent config: cannot encode selected configuration")
	}
	return b.write(dst, data)
}

// The bridge is supplied afresh for each run. Never merge a host definition
// into it, or import MCP-specific permission bypasses, in any selected scope.
func sanitizeMCP(selected map[string]any) {
	for _, key := range []string{"mcp_servers", "mcpServers"} {
		if servers, ok := selected[key].(map[string]any); ok {
			delete(servers, "yip")
			for _, server := range servers {
				if config, ok := server.(map[string]any); ok {
					for _, field := range []string{"alwaysAllow", "autoApprove", "default_tools_approval_mode", "tools"} {
						delete(config, field)
					}
				}
			}
		}
	}
}
