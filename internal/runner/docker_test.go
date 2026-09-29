package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/agentconfig"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/codex"
	"github.com/binbandit/yip/internal/providers/worker"
	"github.com/binbandit/yip/protocol"
)

func dockerTestRunner() *Runner {
	return &Runner{opts: Options{ExecutionProfile: "docker", Docker: DockerOptions{Image: DefaultDockerImage}},
		id: Identity{NodeID: "test-node"}}
}

func fakeDockerCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix:///unused-test-socket")
}

func TestDockerIsolationArguments(t *testing.T) {
	r := dockerTestRunner()
	ws, imports := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	args, err := r.dockerArgs("unit-container", ws, imports, false)
	if os.Getuid() == 0 {
		if err == nil {
			t.Fatal("root runner must be refused")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--init",
		"--rm", "--pull=never", "--pids-limit=512", "--memory=4g", "--memory-swap=4g", "--cpus=2"} {
		if !slices.Contains(args, flag) {
			t.Errorf("missing isolation argument %s", flag)
		}
	}
	text := strings.Join(args, "\n")
	for _, wanted := range []string{"dst=/workspace\n", "dst=/workspace/.git,readonly", "dst=/run/yip-import,readonly", "HOME=/home/yip"} {
		if !strings.Contains(text, wanted) {
			t.Errorf("missing mount/environment %s", wanted)
		}
	}
	for _, forbidden := range []string{"docker.sock", "--privileged", "network=host", "--pid=host", "--env-file"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unsafe argument %s", forbidden)
		}
	}
	ro, err := r.dockerArgs("unit-container", ws, "", true)
	if err != nil || !strings.Contains(strings.Join(ro, "\n"), "dst=/workspace,readonly") {
		t.Fatalf("read-only workspace not enforced: %v %v", ro, err)
	}
}

func TestDockerMountRejectsOptionInjection(t *testing.T) {
	for _, path := range []string{"bad,readonly=false", "bad\npath"} {
		if _, err := dockerMount(filepath.Join(t.TempDir(), path), "/workspace", false); err == nil {
			t.Errorf("accepted mount path %q", path)
		}
	}
}

func TestDockerCleanupConfirmsDaemonState(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         bool
	}{
		{"removed", "exit 0", true},
		{"already absent", `[ "$1" = rm ] && exit 1; exit 0`, true},
		{"still exists", `[ "$1" = container ] && echo still-running; exit 0`, false},
		{"daemon lost", "exit 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeDockerCLI(t, tc.script)
			c := &dockerContainer{name: "test-container"}
			if got := c.remove(); got != tc.want {
				t.Fatalf("confirmed=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestDockerReadinessRejectsRemoteAndMissingImage(t *testing.T) {
	r := dockerTestRunner()
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "tcp://remote:2375")
	if err := r.dockerReady(context.Background()); err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("remote engine accepted: %v", err)
	}
	fakeDockerCLI(t, `case "$1" in info) echo linux;; image) exit 1;; *) exit 0;; esac`)
	if err := r.dockerReady(context.Background()); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("missing image accepted: %v", err)
	}
}

func TestDockerProbeDoesNotFallBackToHost(t *testing.T) {
	fakeDockerCLI(t, "exit 1")
	r := dockerTestRunner()
	r.paths = Paths{Dir: t.TempDir()}
	r.opts.Adapters = map[string]providers.Adapter{"codex": codex.New()}
	caps := r.Probe(context.Background())
	if len(caps.Providers) != 1 || caps.Providers[0].AuthState != protocol.AuthError {
		t.Fatalf("must not report the host's provider: %+v", caps.Providers)
	}
	if len(caps.Toolchains) != 0 {
		t.Fatalf("must not advertise host toolchains: %v", caps.Toolchains)
	}
	for _, p := range caps.Profiles {
		if p.Available {
			t.Fatalf("unavailable Docker advertised profile %s", p.Name)
		}
	}
}

func TestDockerCreateFailureDrainsWhenCleanupUnknown(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root runner is rejected before container creation")
	}
	fakeDockerCLI(t, "exit 1")
	r := dockerTestRunner()
	_, err := r.createDocker(context.Background(), "", false, agentconfig.Snapshot{}, []string{"true"})
	if err == nil || !r.draining || !r.uncertain.Load() {
		t.Fatalf("uncertain startup did not fail closed: err=%v drained=%v uncertain=%v", err, r.draining, r.uncertain.Load())
	}
}

func TestDockerToolchainProbeDrainsWhenCleanupUnknown(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root runner is rejected before container creation")
	}
	fakeDockerCLI(t, `case "$1" in create) exit 0;; start) echo '{"git":"container-git"}';; *) exit 1;; esac`)
	r := dockerTestRunner()
	if tools := r.dockerToolchains(t.Context()); len(tools) != 0 || !r.uncertain.Load() {
		t.Fatalf("uncertain probe advertised tools: %v uncertain=%v", tools, r.uncertain.Load())
	}
}

func TestDockerRestartReapsStagedImports(t *testing.T) {
	fakeDockerCLI(t, "exit 0")
	r := dockerTestRunner()
	r.paths = Paths{t.TempDir()}
	dir, err := r.dockerStaging()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abandoned-fixture"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.reapDocker(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("abandoned staging was not removed")
	}
}

// TestDockerImage is deliberately opt-in: it never imports real credentials,
// downloads an image or starts a provider session.
func TestDockerImage(t *testing.T) {
	if os.Getenv("YIP_DOCKER_TESTS") != "1" {
		t.Skip("set YIP_DOCKER_TESTS=1 after building packaging/container/Dockerfile.agent")
	}
	r := dockerTestRunner()
	if image := os.Getenv("YIP_DOCKER_IMAGE"); image != "" {
		r.opts.Docker.Image = image
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := r.dockerReady(ctx); err != nil {
		t.Fatal(err)
	}
	ws, imports := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imports, "synthetic-skill"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := r.createDocker(ctx, ws, false, agentconfig.Snapshot{Dir: imports},
		[]string{"/bin/sh", "-c", `test "$(id -u)" -ne 0 && test ! -e /var/run/docker.sock && test -f "$HOME/synthetic-skill" && ! touch /.yip-escape && ! touch .git/escape && printf isolated > isolation.txt`})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.remove() })
	if out, err := c.attach(ctx).CombinedOutput(); err != nil {
		t.Fatalf("isolation checks failed: %v: %s", err, out)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "isolation.txt")); err != nil || string(b) != "isolated" {
		t.Fatalf("workspace write did not reach the host: %q %v", b, err)
	}
	w, err := r.createDocker(ctx, "", true, agentconfig.Snapshot{}, []string{"/usr/local/bin/yip", "agent-worker", "--provider", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.remove() })
	if inst, err := worker.Probe(ctx, w.attach(ctx)); err != nil || inst.Version == "" || inst.AuthState == protocol.AuthReady {
		t.Fatalf("worker probe through the container: %+v %v", inst, err)
	}
	if tools := r.dockerToolchains(ctx); tools["git"] == "" {
		t.Fatalf("image toolchains: %v", tools)
	}
	// A separate check container must enforce a readonly workspace and have
	// neither the imported home nor the previous attempt's temporary state.
	check, err := r.createDocker(ctx, ws, true, agentconfig.Snapshot{},
		[]string{"/bin/sh", "-c", `test ! -e "$HOME/synthetic-skill" && ! touch /workspace/readonly-escape`})
	if err != nil {
		t.Fatal(err)
	}
	defer check.remove()
	if out, err := check.attach(ctx).CombinedOutput(); err != nil {
		t.Fatalf("read-only/credential-free check failed: %v: %s", err, out)
	}
}
