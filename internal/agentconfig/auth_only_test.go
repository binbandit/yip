package agentconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var authOnlyProviders = []struct {
	provider, key, sourceDir, destinationDir, envValue string
}{
	{"opencode", "XDG_DATA_HOME", "opencode", ".local/share/opencode", HOME + "/.local/share"},
	{"pi", "PI_CODING_AGENT_DIR", "", ".pi/agent", HOME + "/.pi/agent"},
}

func TestAuthOnlyProfiles(t *testing.T) {
	for _, tc := range authOnlyProviders {
		for _, profile := range []string{"default", "selected", "missing"} {
			t.Run(tc.provider+"/"+profile, func(t *testing.T) {
				home := syntheticHome(t)
				dest := filepath.Join(tc.destinationDir, "auth.json")
				fixture(t, home, dest, "synthetic-default")
				sourceDir := filepath.Join(home, tc.destinationDir)
				want := "synthetic-default"
				if profile != "default" {
					t.Setenv(tc.key, filepath.Join(home, "selected"))
					sourceDir = filepath.Join(home, "selected", tc.sourceDir)
					want = "synthetic-selected\x00opaque\nnot-json"
					if profile == "selected" {
						fixture(t, sourceDir, "auth.json", want)
					}
				}
				// Neither provider configuration nor shared executable skills
				// are part of these adapters' auth-only import contract.
				for _, name := range []string{
					"settings.json", "models.json", "opencode.json", "mcp.json",
					"plugins/plugin.js", "extensions/extension.ts",
					"agents/agent.md", "skills/private/SKILL.md",
				} {
					fixture(t, sourceDir, name, "never-import")
				}
				for _, name := range []string{
					".agents/skills/shared/SKILL.md", ".config/opencode/opencode.json",
					".codex/auth.json", ".claude/.credentials.json",
				} {
					fixture(t, home, name, "never-import")
				}
				s := snapshot(t, tc.provider)
				if !slices.Contains(s.Env, tc.key+"="+tc.envValue) {
					t.Fatal("missing controlled destination environment")
				}
				var files []string
				err := filepath.WalkDir(s.Dir, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					info, err := entry.Info()
					if err != nil {
						return err
					}
					if info.Mode().Perm()&0077 != 0 {
						t.Errorf("snapshot is not private: %s", path)
					}
					if !entry.IsDir() {
						rel, err := filepath.Rel(s.Dir, path)
						if err != nil {
							return err
						}
						files = append(files, rel)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if profile == "missing" {
					if len(files) != 0 {
						t.Fatalf("missing selected auth must not fall back: %v", files)
					}
					return
				}
				if !slices.Equal(files, []string{dest}) {
					t.Fatalf("import must contain only selected auth: %v", files)
				}
				data, err := os.ReadFile(filepath.Join(s.Dir, dest))
				if err != nil || string(data) != want {
					t.Fatal("opaque authentication copy changed")
				}
			})
		}
	}
}

func TestAuthOnlyRejectsUnsafeSources(t *testing.T) {
	for _, tc := range authOnlyProviders {
		for _, kind := range []string{"file-symlink", "parent-symlink", "relative", "oversized"} {
			t.Run(tc.provider+"/"+kind, func(t *testing.T) {
				home := syntheticHome(t)
				root := filepath.Join(home, "selected")
				t.Setenv(tc.key, root)
				auth := filepath.Join(root, tc.sourceDir, "auth.json")
				fixture(t, root, filepath.Join(tc.sourceDir, "auth.json"), "synthetic")
				switch kind {
				case "file-symlink":
					if err := os.Remove(auth); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(home, "missing"), auth); err != nil {
						t.Fatal(err)
					}
				case "parent-symlink":
					link := filepath.Join(home, "link")
					if err := os.Symlink(root, link); err != nil {
						t.Fatal(err)
					}
					t.Setenv(tc.key, link)
				case "relative":
					t.Setenv(tc.key, "relative-profile")
				case "oversized":
					if err := os.Truncate(auth, MaxFileBytes+1); err != nil {
						t.Fatal(err)
					}
				}
				parent := t.TempDir()
				s, err := StageIn(parent, tc.provider, "", nil)
				if err == nil || s.Dir != "" || len(s.Env) != 0 {
					t.Fatalf("unsafe import: %+v %v", s, err)
				}
				if strings.Contains(err.Error(), "synthetic") {
					t.Fatal("error exposed authentication contents")
				}
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 0 {
					t.Fatalf("failed import left private snapshot: %v %v", entries, err)
				}
			})
		}
	}
}
