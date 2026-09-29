package agentconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectedProjectMCPScope(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "default home", true: "config override"}[override], func(t *testing.T) {
			home := syntheticHome(t)
			registry := ".claude.json"
			if override {
				t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
				registry = ".claude/.claude.json"
			}
			workspace := filepath.Join(home, "selected-job")
			data, err := json.Marshal(map[string]any{
				"mcpServers": map[string]any{"shared": map[string]any{"command": "shared-server"}, "yip": map[string]any{"command": "discard"}},
				"projects": map[string]any{
					workspace: map[string]any{
						"hasTrustDialogAccepted": true,
						"allowedTools":           []string{"Bash"},
						"mcpServers": map[string]any{
							"local": map[string]any{"command": "local-server", "env": map[string]string{"PROJECT_KEY": "synthetic"}, "alwaysAllow": []string{"all"}},
							"yip":   map[string]any{"command": "discard"},
						},
					},
					workspace + "-other": map[string]any{"mcpServers": map[string]any{"unrelated": map[string]any{"command": "never-import"}}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture(t, home, registry, string(data))
			s, err := StageIn(t.TempDir(), "claude", workspace, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(s.Dir)
			imported, err := os.ReadFile(filepath.Join(s.Dir, registry))
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(imported, &got); err != nil {
				t.Fatal(err)
			}
			projects := got["projects"].(map[string]any)
			if len(projects) != 1 || projects["/workspace"] == nil {
				t.Fatalf("selected scope not remapped: %s", imported)
			}
			scoped := projects["/workspace"].(map[string]any)
			servers := scoped["mcpServers"].(map[string]any)
			if len(scoped) != 1 || len(servers) != 1 || servers["local"] == nil {
				t.Fatalf("unexpected local scope: %s", imported)
			}
			for _, forbidden := range []string{"hasTrustDialogAccepted", "allowedTools", "alwaysAllow", "discard", "never-import", workspace} {
				if strings.Contains(string(imported), forbidden) {
					t.Fatalf("copied excluded data %q: %s", forbidden, imported)
				}
			}
			probe := snapshot(t, "claude")
			probeData, err := os.ReadFile(filepath.Join(probe.Dir, registry))
			if err != nil || strings.Contains(string(probeData), "projects") {
				t.Fatalf("unscoped probe selected a project: %s %v", probeData, err)
			}
		})
	}
}

func TestProjectMCPScopeRequiresExactAbsoluteWorkspace(t *testing.T) {
	home := syntheticHome(t)
	fixture(t, home, ".claude.json", `{"projects":{"/different":{"mcpServers":{"local":{"command":"never-import"}}}}}`)
	if _, err := StageIn(t.TempDir(), "claude", "relative", nil); err == nil {
		t.Fatal("relative workspace accepted")
	}
	s, err := StageIn(t.TempDir(), "claude", filepath.Join(home, "job"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s.Dir)
	data, err := os.ReadFile(filepath.Join(s.Dir, ".claude.json"))
	if err != nil || strings.Contains(string(data), "projects") {
		t.Fatalf("unrelated project imported: %s %v", data, err)
	}
}
