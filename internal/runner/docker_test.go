package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/agentconfig"
	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/codex"
	"github.com/binbandit/yip/internal/providers/worker"
	"github.com/binbandit/yip/protocol"
)

func dockerTestRunner(t *testing.T) *Runner {
	t.Helper()
	return &Runner{opts: Options{ExecutionProfile: "docker", Docker: DockerOptions{Image: DefaultDockerImage}},
		id: Identity{NodeID: "test-node"}, paths: Paths{t.TempDir()}}
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
	r := dockerTestRunner(t)
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
	r := dockerTestRunner(t)
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
	r := dockerTestRunner(t)
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
	r := dockerTestRunner(t)
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
	r := dockerTestRunner(t)
	if tools := r.dockerToolchains(t.Context()); len(tools) != 0 || !r.uncertain.Load() {
		t.Fatalf("uncertain probe advertised tools: %v uncertain=%v", tools, r.uncertain.Load())
	}
}

func TestDockerRestartReapsStagedImports(t *testing.T) {
	fakeDockerCLI(t, "exit 0")
	r := dockerTestRunner(t)
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

func TestReplacementReapsOnlyOwnedDockerContainers(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	old := filepath.Join(dir, "old-container")
	older := filepath.Join(dir, "older-container")
	unrelated := filepath.Join(dir, "unrelated-container")
	workspace := filepath.Join(dir, "unpublished.txt")
	for _, state := range []string{"", "identity-old"} {
		imports := filepath.Join(dir, state, "container-imports")
		if err := os.MkdirAll(imports, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(imports, "auth.json"), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{old, older, unrelated, workspace} {
		if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fakeDockerCLI(t, fmt.Sprintf(`
printf '%%s\n' "$*" >> %q
case "$1" in
container)
  case "$*" in
    *label=dev.yip.runner=old) [ ! -e %q ] || echo old-container;;
    *label=dev.yip.runner=older) [ ! -e %q ] || echo older-container;;
  esac
  exit 0;;
rm)
  case "$3" in
    old-container) rm %q;;
    older-container) rm %q;;
    *) exit 1;;
  esac;;
*) exit 1;;
esac`, log, old, older, old, older))
	r := dockerTestRunner(t)
	r.paths = Paths{dir}
	r.opts.StateDir = dir
	if err := json.Unmarshal([]byte(`{"nodeId":"new","previousNodeIds":["older","old"],"previousStateDirs":["","identity-old"]}`), &r.id); err != nil {
		t.Fatal(err)
	}
	if err := r.reapDocker(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{old, older} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("previous identity's container survived: %s", path)
		}
	}
	for _, state := range []string{"", "identity-old"} {
		if _, err := os.Stat(filepath.Join(dir, state, "container-imports")); !os.IsNotExist(err) {
			t.Fatal("previous identity's temporary credential imports survived confirmed cleanup")
		}
	}
	for _, path := range []string{unrelated, workspace} {
		if got, err := os.ReadFile(path); err != nil || string(got) != "preserve" {
			t.Fatalf("unrelated container or unpublished work changed: %s, %v", path, err)
		}
	}
}

func TestReplacementNativeStartupRequiresDockerCleanup(t *testing.T) {
	for _, scenario := range []string{"daemon unavailable", "inspection failed", "removal failed", "container remains", "unsafe previous storage", "imports removal failed", "removed", "legacy removed", "legacy daemon unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "imports removal failed" && os.Getuid() == 0 {
				t.Skip("root can remove files through read-only directories")
			}
			dir := t.TempDir()
			ca, err := auth.LoadOrCreateCA(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			key, csr, err := auth.NewNodeKeyAndCSR("new")
			if err != nil {
				t.Fatal(err)
			}
			cert, _, _, err := ca.SignNodeCSR(csr, "new", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			id := Identity{NodeID: "new", HubURL: "https://127.0.0.1:1", PreviousNodeIDs: []string{"old"}, DockerCleanupRequired: true}
			if strings.HasPrefix(scenario, "legacy ") {
				id.DockerCleanupRequired = false
			}
			if scenario == "unsafe previous storage" {
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "identity-old")); err != nil {
					t.Fatal(err)
				}
				id.PreviousStateDirs = []string{"identity-old"}
			}
			if scenario == "imports removal failed" {
				oldState := filepath.Join(dir, "identity-old")
				if err := os.MkdirAll(filepath.Join(oldState, "container-imports"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(oldState, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(oldState, 0700) })
				id.PreviousStateDirs = []string{"identity-old"}
			}
			if _, err := SaveLocalIdentity(dir, id, key, cert, ca.CertPEM); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(dir, "old-container")
			if err := os.WriteFile(old, []byte("running"), 0600); err != nil {
				t.Fatal(err)
			}
			fakeDockerCLI(t, fmt.Sprintf(`
case "$1" in
info) case %q in 'daemon unavailable'|'legacy daemon unavailable') exit 1;; esac; echo linux;;
container)
  [ %q != 'inspection failed' ] || exit 1
  case "$*" in
    *label=dev.yip.runner=old) [ ! -e %q ] || echo old-container;;
  esac
  exit 0;;
rm)
  [ %q != 'removal failed' ] || exit 1
  case %q in 'removed'|'legacy removed'|'unsafe previous storage'|'imports removal failed') rm %q;; esac;;
*) exit 1;;
esac`, scenario, scenario, old, scenario, scenario, old))
			r, err := New(Options{StateDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			removed := scenario == "removed" || scenario == "legacy removed"
			if removed {
				go func() {
					for ctx.Err() == nil {
						current, err := LoadIdentity(dir)
						if err == nil && !current.DockerCleanupRequired && len(current.PreviousNodeIDs) == 0 {
							cancel()
							return
						}
						time.Sleep(time.Millisecond)
					}
				}()
			}
			err = r.Run(ctx)
			current, readErr := LoadIdentity(dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if removed {
				if err != nil || current.DockerCleanupRequired || len(current.PreviousNodeIDs) != 0 {
					t.Fatalf("confirmed cleanup did not clear its durable obligation: %+v, %v", current, err)
				}
				if _, err := os.Stat(old); !os.IsNotExist(err) {
					t.Fatal("old container survived native startup")
				}
			} else {
				if err == nil || !current.DockerCleanupRequired || !slices.Equal(current.PreviousNodeIDs, []string{"old"}) {
					t.Fatalf("uncertain cleanup must block startup and remain retryable: %+v, %v", current, err)
				}
				if r.bridgeLn != nil {
					t.Fatal("runner admitted work before confirming old container cleanup")
				}
			}
		})
	}
}

// TestDockerImage is deliberately opt-in: it never imports real credentials,
// downloads an image or starts a provider session.
func TestDockerImage(t *testing.T) {
	if os.Getenv("YIP_DOCKER_TESTS") != "1" {
		t.Skip("set YIP_DOCKER_TESTS=1 after building packaging/container/Dockerfile.agent")
	}
	r := dockerTestRunner(t)
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
