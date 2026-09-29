package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Checks run with a scratch home and without the machine's credentials: no
// SSH agent, no user git config and no git credential helper.
func TestCheckEnvIsIsolated(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("XDG_CONFIG_HOME", "/home/someone/.config")
	r := &Runner{paths: Paths{Dir: t.TempDir()}}
	env, err := r.checkEnv("run-1")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := vars[k]; dup {
			t.Errorf("%s is set twice", k)
		}
		vars[k] = v
	}
	scratch := r.scratchDir("run-1")
	if vars["HOME"] != filepath.Join(scratch, "home") || vars["TMPDIR"] != filepath.Join(scratch, "tmp") {
		t.Fatalf("checks must use the run's scratch home and temp dir: HOME=%q TMPDIR=%q", vars["HOME"], vars["TMPDIR"])
	}
	for _, dir := range []string{vars["HOME"], vars["TMPDIR"]} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Fatalf("%s was not created: %v", dir, err)
		}
	}
	for _, k := range []string{"SSH_AUTH_SOCK", "XDG_CONFIG_HOME"} {
		if _, ok := vars[k]; ok {
			t.Errorf("%s reached the check", k)
		}
	}
	for k, want := range map[string]string{"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
		"GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": ""} {
		if got, ok := vars[k]; !ok || got != want {
			t.Errorf("%s = %q (set %v), want %q", k, got, ok, want)
		}
	}
}
