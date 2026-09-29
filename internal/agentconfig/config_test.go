package agentconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func syntheticHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "CURSOR_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, "")
	}
	return home
}

func fixture(t *testing.T, home, path, content string) {
	t.Helper()
	path = filepath.Join(home, path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, provider string, keys ...string) Snapshot {
	t.Helper()
	s, err := Stage(provider, keys)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(s.Dir) })
	return s
}

func TestProviderIsolationAndPrivateFiles(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			home := syntheticHome(t)
			for _, path := range []string{".agents/skills/shared/SKILL.md", ".claude/skills/a/SKILL.md", ".codex/skills/b/SKILL.md", ".cursor/skills/c/SKILL.md", ".codex/auth.json", ".claude/.credentials.json"} {
				fixture(t, home, path, "{}")
			}
			for _, path := range []string{".ssh/id_rsa", ".claude/settings.json", ".codex/history.jsonl", ".cursor/cli-config.json", ".agents/other.txt"} {
				fixture(t, home, path, "never-import")
			}
			s := snapshot(t, provider)
			if _, err := os.Stat(filepath.Join(s.Dir, ".agents/skills/shared/SKILL.md")); err != nil {
				t.Fatal(err)
			}
			for _, other := range []string{"claude", "codex", "cursor"} {
				if other != provider {
					if provider == "cursor" {
						entries, err := os.ReadDir(filepath.Join(s.Dir, "."+other))
						if err != nil || len(entries) != 1 || entries[0].Name() != "skills" {
							t.Fatalf("compatibility import must contain skills only: %s %v %v", other, entries, err)
						}
						continue
					}
					if _, err := os.Stat(filepath.Join(s.Dir, "."+other)); !os.IsNotExist(err) {
						t.Fatalf("imported unrelated provider %s", other)
					}
				}
			}
			err := filepath.WalkDir(s.Dir, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode().Perm()&0077 != 0 {
					t.Errorf("not private: %s", path)
				}
				if !entry.IsDir() {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if strings.Contains(string(data), "never-import") {
						t.Errorf("unexpected file %s", path)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSanitizedConfig(t *testing.T) {
	home := syntheticHome(t)
	fixture(t, home, ".codex/config.toml", `model="test"
notify=["bad"]
cli_auth_credentials_store="keyring"
[mcp_servers.docs]
command="docs-server"
default_tools_approval_mode="never"
[mcp_servers.yip]
url="https://wrong-bridge"
[features]
hooks=true
[projects."/host"]
trust_level="trusted"
`)
	s := snapshot(t, "codex")
	data, err := os.ReadFile(filepath.Join(s.Dir, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"notify", "keyring", "hooks", "trusted", "/host", "never", "wrong-bridge"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("imported %s", forbidden)
		}
	}
	for _, required := range []string{"docs-server", "model", "cli_auth_credentials_store = 'file'"} {
		if !strings.Contains(string(data), required) {
			t.Errorf("missing %s: %s", required, data)
		}
	}
	fixture(t, home, ".claude.json", `{"mcpServers":{"docs":{"command":"docs"}},"projects":{"secret":{}},"oauthAccount":"never-import"}`)
	s = snapshot(t, "claude")
	data, err = os.ReadFile(filepath.Join(s.Dir, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 1 || cfg["mcpServers"] == nil {
		t.Fatalf("not sanitized: %s", data)
	}
}

func TestExplicitConfigHomes(t *testing.T) {
	for _, tc := range []struct{ provider, key, source, file, dest string }{
		{"codex", "CODEX_HOME", "custom", "auth.json", ".codex/auth.json"},
		{"claude", "CLAUDE_CONFIG_DIR", "custom", ".credentials.json", ".claude/.credentials.json"},
		{"cursor", "CURSOR_CONFIG_DIR", "custom", "skills/x/SKILL.md", ".cursor/skills/x/SKILL.md"},
		{"cursor", "XDG_CONFIG_HOME", "custom/cursor", "skills/x/SKILL.md", ".cursor/skills/x/SKILL.md"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			home := syntheticHome(t)
			t.Setenv(tc.key, filepath.Join(home, "custom"))
			fixture(t, home, filepath.Join(tc.source, tc.file), "synthetic")
			s := snapshot(t, tc.provider)
			if _, err := os.Stat(filepath.Join(s.Dir, tc.dest)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExplicitEnvironment(t *testing.T) {
	syntheticHome(t)
	t.Setenv("TEST_TOKEN", "synthetic-secret")
	t.Setenv("OTHER_TOKEN", "not-selected")
	s := snapshot(t, "codex", "TEST_TOKEN", "TEST_TOKEN")
	if !slices.Contains(s.Env, "TEST_TOKEN=synthetic-secret") || slices.Contains(s.Env, "OTHER_TOKEN=not-selected") {
		t.Fatal("wrong environment selection")
	}
	if !slices.Contains(s.Env, "CODEX_HOME="+HOME+"/.codex") {
		t.Fatal("missing home remapping")
	}
	for _, key := range []string{"HOME", "PATH", "XDG_DATA_HOME", "PI_CODING_AGENT_DIR", "SSH_AUTH_SOCK", "YIP_TOKEN", "BAD=synthetic-secret", "bad\nname"} {
		_, err := Stage("codex", []string{key})
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	t.Setenv("TEST_TOKEN", "synthetic-secret\nbad")
	_, err := Stage("codex", []string{"TEST_TOKEN"})
	if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestRejectSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"file", "directory", "dangling", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			home := syntheticHome(t)
			fixture(t, home, ".agents/skills/a/SKILL.md", "safe")
			target := filepath.Join(home, ".agents/skills/a/import")
			var err error
			switch kind {
			case "file":
				err = os.Symlink(filepath.Join(home, ".agents/skills/a/SKILL.md"), target)
			case "directory":
				err = os.Symlink(home, target)
			case "dangling":
				err = os.Symlink(filepath.Join(home, "missing"), target)
			case "fifo":
				err = syscall.Mkfifo(target, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := Stage("codex", nil)
			if err == nil || s.Dir != "" || len(s.Env) != 0 {
				t.Fatalf("unsafe import: %+v %v", s, err)
			}
		})
	}
}

func TestImportLimitsAndInvalidConfig(t *testing.T) {
	home := syntheticHome(t)
	file := filepath.Join(home, "large")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b := builder{dir: t.TempDir()}
	if _, err = b.read(file); err == nil {
		t.Fatal("accepted large file")
	}
	fixture(t, home, "small", "123")
	for _, exhausted := range []builder{{files: MaxFiles}, {bytes: MaxBytes}} {
		if _, err = exhausted.read(filepath.Join(home, "small")); err == nil {
			t.Fatal("accepted exhausted budget")
		}
	}
	fixture(t, home, ".codex/config.toml", "secret-token={broken")
	_, err = Stage("codex", nil)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe parse error: %v", err)
	}
}

func TestFreshSnapshots(t *testing.T) {
	syntheticHome(t)
	a, b := snapshot(t, "codex"), snapshot(t, "codex")
	if a.Dir == b.Dir {
		t.Fatal("snapshot reused")
	}
	if _, err := Stage("unknown", nil); err == nil {
		t.Fatal("accepted unknown provider")
	}
}

func TestFakeImportsNoHostFiles(t *testing.T) {
	home := syntheticHome(t)
	fixture(t, home, ".agents/skills/a/SKILL.md", "not-for-fake")
	s := snapshot(t, "fake")
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("fake should have empty home: %v %v", entries, err)
	}
}

func TestSkillExecutableAndTreeBounds(t *testing.T) {
	home := syntheticHome(t)
	fixture(t, home, ".agents/skills/a/run.sh", "#!/bin/sh\ntrue\n")
	path := filepath.Join(home, ".agents/skills/a/run.sh")
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	s := snapshot(t, "codex")
	info, err := os.Stat(filepath.Join(s.Dir, ".agents/skills/a/run.sh"))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("executable permission lost: %v %v", info, err)
	}
	b := builder{dir: t.TempDir(), entries: MaxFiles}
	if err := b.tree(filepath.Join(home, ".agents/skills"), ".agents/skills"); err == nil {
		t.Fatal("tree entry budget ignored")
	}
}

func TestFailureRemovesPartialSnapshot(t *testing.T) {
	home := syntheticHome(t)
	staging := filepath.Join(home, "staging")
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", staging)
	fixture(t, home, ".agents/skills/a/SKILL.md", "copied-first")
	fixture(t, home, ".codex/config.toml", "not valid = [[[")
	if _, err := Stage("codex", nil); err == nil {
		t.Fatal("expected malformed config failure")
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial snapshot retained: %v %v", entries, err)
	}
}
