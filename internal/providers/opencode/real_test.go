package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// TestRealHandshake is deliberately opt-in and never sends session/prompt.
// Every home/config/data/cache path is fresh; no local sign-in is accessed.
func TestRealHandshake(t *testing.T) {
	exe := os.Getenv("YIP_TEST_REAL_OPENCODE")
	if exe == "" {
		t.Skip("set YIP_TEST_REAL_OPENCODE to an official 1.18.33 binary")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".opencode", "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"), "NO_COLOR=1",
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
	}
	a := New(WithExecutable(exe), WithProbeEnv(env))
	probe := a.Probe(context.Background())
	if probe.Version != "1.18.33" || probe.AuthState != protocol.AuthNeedsSignIn {
		t.Fatalf("isolated probe: %+v", probe)
	}
	for _, mode := range []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation} {
		t.Run(mode, func(t *testing.T) {
			ctx, stop := context.WithTimeout(context.Background(), 45*time.Second)
			s := &session{
				ctx: ctx, stop: stop, configDir: t.TempDir(), serveDone: make(chan struct{}),
				events: make(chan providers.Event, 1024),
			}
			spec := providers.StartSpec{
				Mode: mode, Workdir: t.TempDir(),
				// A no-op executable is sufficient: this test verifies registration,
				// not bridge execution or paid inference.
				MCP: providers.MCPServer{Name: "yip", Command: "/usr/bin/true"},
			}
			launch := launchEnv(env, s.configDir, mode)
			resolved, err := command(ctx, exe, launch, "debug", "config")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			var config struct {
				DefaultAgent string                     `json:"default_agent"`
				Permission   map[string]json.RawMessage `json:"permission"`
				LSP          json.RawMessage            `json:"lsp"`
				Formatter    json.RawMessage            `json:"formatter"`
				Snapshot     json.RawMessage            `json:"snapshot"`
			}
			if err := json.Unmarshal([]byte(resolved), &config); err != nil {
				stop()
				t.Fatalf("resolved configuration: %v", err)
			}
			if config.DefaultAgent != "yip" || string(config.Permission["*"]) != `"deny"` {
				stop()
				t.Fatalf("unsafe resolved configuration: %+v", config)
			}
			if string(config.LSP) != "false" || string(config.Formatter) != "false" || string(config.Snapshot) != "false" {
				stop()
				t.Fatalf("background executors must be disabled: %+v", config)
			}
			for _, name := range []string{"bash", "shell", "execute", "batch", "task"} {
				if string(config.Permission[name]) != `"deny"` {
					stop()
					t.Fatalf("unmanaged execution %s is not denied", name)
				}
			}
			if err := s.launch(exe, spec, launch); err != nil {
				stop()
				t.Fatal(err)
			}
			defer func() {
				stop()
				if !s.cleanup() {
					t.Error("process exit was not confirmed")
				}
			}()
			if err := s.setup(ctx, spec); err != nil {
				t.Fatal(err)
			}
			if err := checkHomeExtensions(filepath.Join(home, ".opencode")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
