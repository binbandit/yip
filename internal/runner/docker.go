package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/agentconfig"
	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/worker"
	"github.com/binbandit/yip/protocol"
)

const DefaultDockerImage = "yip-agent:local"

// DockerOptions deliberately has no arbitrary mount or Docker-argument escape
// hatch. Extend the image to install toolchains and local MCP dependencies.
type DockerOptions struct {
	Image string
	Env   []string // names explicitly authorized for import, never values
}

func dockerCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = providers.BaseEnv([]string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"})
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

func dockerCLIAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

func (r *Runner) dockerReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := dockerEngineReady(ctx); err != nil {
		return err
	}
	if err := dockerCommand(ctx, "image", "inspect", r.opts.Docker.Image).Run(); err != nil {
		return fmt.Errorf("agent image %q is not available locally; build packaging/container/Dockerfile.agent first", r.opts.Docker.Image)
	}
	return nil
}

func dockerEngineReady(ctx context.Context) error {
	endpoint := os.Getenv("DOCKER_HOST")
	if os.Getenv("DOCKER_CONTEXT") != "" || endpoint == "" {
		out, err := dockerCommand(ctx, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			return errors.New("Docker is unavailable; install Docker and start its local Linux engine")
		}
		endpoint = strings.TrimSpace(string(out))
	}
	if !strings.HasPrefix(endpoint, "unix://") {
		return errors.New("ephemeral agents require a local Docker Unix socket; remote Docker engines are not supported")
	}
	out, err := dockerCommand(ctx, "info", "--format", "{{.OSType}}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "linux" {
		return errors.New("a running Linux Docker engine is required")
	}
	return nil
}

type dockerContainer struct {
	name string
}

func (r *Runner) dockerUncertain() {
	r.uncertain.Store(true)
	r.mu.Lock()
	r.draining = true
	r.mu.Unlock()
}

func (r *Runner) dockerStaging() (string, error) {
	dir := filepath.Join(r.paths.Dir, "container-imports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, os.Chmod(dir, 0o700)
}

// remove confirms absence at the daemon, not merely exit of the Docker client.
// A lost daemon connection must never be reported as confirmed agent exit.
func (c *dockerContainer) remove() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = dockerCommand(ctx, "rm", "--force", c.name).Run()
	out, err := dockerCommand(ctx, "container", "ls", "--all", "--quiet", "--filter", "name=^/"+c.name+"$").Output()
	return err == nil && strings.TrimSpace(string(out)) == ""
}

func dockerMount(source, target string, readonly bool) (string, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	// --mount uses CSV; do not let a path inject a second mount option.
	if strings.ContainsAny(abs, ",\r\n") {
		return "", errors.New("Docker mount paths cannot contain commas or newlines")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(resolved, ",\r\n") {
		return "", errors.New("Docker mount paths cannot contain commas or newlines")
	}
	mount := "type=bind,src=" + resolved + ",dst=" + target
	if readonly {
		mount += ",readonly"
	}
	return mount, nil
}

func (r *Runner) dockerArgs(name, workspace, imports string, readonly bool) ([]string, error) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		return nil, errors.New("ephemeral agents require the runner to use a non-root account")
	}
	args := []string{"create", "--pull=never", "--name", name, "--rm", "--interactive", "--init",
		"--label", "dev.yip.runner=" + r.id.NodeID,
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--pids-limit=512", "--memory=4g", "--memory-swap=4g", "--cpus=2",
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=1g",
		"--tmpfs", fmt.Sprintf("/home/yip:rw,nosuid,nodev,uid=%d,gid=%d,mode=0700,size=512m", uid, gid),
		"--env", "HOME=/home/yip", "--env", "TMPDIR=/tmp",
		"--env", "XDG_CONFIG_HOME=/home/yip/.config", "--env", "XDG_CACHE_HOME=/home/yip/.cache",
		"--env", "GIT_CONFIG_NOSYSTEM=1", "--env", "GIT_CONFIG_GLOBAL=/dev/null",
		"--env", "GIT_TERMINAL_PROMPT=0", "--workdir", "/workspace",
		"--entrypoint", "/bin/sh"}
	if workspace != "" {
		mount, err := dockerMount(workspace, "/workspace", readonly)
		if err != nil {
			return nil, err
		}
		args = append(args, "--mount", mount)
		// Git metadata is writable only through runner-mediated publication.
		// Otherwise repository filters/config installed by an agent could
		// execute on the host during a later checkpoint.
		meta := filepath.Join(workspace, ".git")
		if info, err := os.Lstat(meta); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("Docker workspace must have standalone Git metadata")
			}
			mount, err := dockerMount(meta, "/workspace/.git", true)
			if err != nil {
				return nil, err
			}
			args = append(args, "--mount", mount)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		args = append(args, "--tmpfs", fmt.Sprintf("/workspace:rw,nosuid,nodev,uid=%d,gid=%d,mode=0700,size=64m", uid, gid))
	}
	if imports != "" {
		mount, err := dockerMount(imports, "/run/yip-import", true)
		if err != nil {
			return nil, err
		}
		args = append(args, "--mount", mount)
	}
	return args, nil
}

// createDocker fixes the container identity before attaching stdio. Killing a
// `docker run` client alone would leave an independently running daemon process.
func (r *Runner) createDocker(ctx context.Context, workspace string, readonly bool, snapshot agentconfig.Snapshot, command []string) (*dockerContainer, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Keep durable evidence of Docker usage even for probes with no imports,
	// so re-pairing cannot bypass orphan cleanup by switching profiles.
	if _, err := r.dockerStaging(); err != nil {
		return nil, err
	}
	c := &dockerContainer{name: "yip-agent-" + domain.NewID()}
	args, err := r.dockerArgs(c.name, workspace, snapshot.Dir, readonly)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Env) > 0 {
		staging, err := r.dockerStaging()
		if err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(staging, "env-")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		for _, kv := range snapshot.Env {
			if strings.ContainsAny(kv, "\r\n\x00") {
				_ = f.Close()
				return nil, errors.New("imported environment values cannot contain newlines or NUL")
			}
			if _, err := fmt.Fprintln(f, kv); err != nil {
				_ = f.Close()
				return nil, err
			}
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		args = append(args, "--env-file", f.Name())
	}
	// The image's entrypoint cannot override this bootstrap. Imported files
	// are copied to disposable tmpfs; refreshed credentials never modify host
	// sign-ins, nor do provider sessions persist across attempts.
	script := `exec "$@"`
	if snapshot.Dir != "" {
		script = `cp -R /run/yip-import/. /home/yip/ && exec "$@"`
	}
	args = append(args, r.opts.Docker.Image, "-c", script, "yip-agent")
	args = append(args, command...)
	if err := dockerCommand(ctx, args...).Run(); err != nil {
		if !c.remove() {
			r.dockerUncertain()
			return nil, fmt.Errorf("could not create or confirm cleanup of Docker container %s", c.name)
		}
		return nil, errors.New("could not create agent container; check the Docker engine, image and mount permissions")
	}
	return c, nil
}

func (c *dockerContainer) attach(ctx context.Context) *exec.Cmd {
	return dockerCommand(ctx, "start", "--attach", "--interactive", c.name)
}

func (r *Runner) startDocker(ctx context.Context, ar *activeRun, spec providers.StartSpec) (providers.Session, error) {
	if err := r.dockerReady(ctx); err != nil {
		return nil, err
	}
	staging, err := r.dockerStaging()
	if err != nil {
		return nil, err
	}
	hostWorkspace, err := filepath.Abs(ar.ws.Dir)
	if err != nil {
		return nil, err
	}
	snapshot, err := agentconfig.StageIn(staging, ar.m.Provider, hostWorkspace, r.opts.Docker.Env)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(snapshot.Dir)
	c, err := r.createDocker(ctx, ar.ws.Dir, ar.ws.ReadOnly || spec.Mode != protocol.ModeEdit, snapshot,
		[]string{"/usr/local/bin/yip", "agent-worker", "--provider", ar.m.Provider})
	if err != nil {
		return nil, err
	}
	ar.mu.Lock()
	ar.container = c
	ar.mu.Unlock()
	spec.Workdir = "/workspace"
	spec.Executable = ""
	// Provider session files live in tmpfs. A retry uses yip's context and
	// checkpoint instead of attempting to resume a deleted vendor session.
	spec.ResumeSessionID = ""
	spec.InheritUserConfig = spec.Mode == protocol.ModeEdit
	// Only values explicitly imported into the container are available. Its
	// full environment is used by the worker, including authorized MCP keys.
	spec.Env = nil
	sess, err := worker.Start(ctx, c.attach(ctx), spec, func(req bridge.LocalRequest) bridge.LocalResponse {
		// Bind all requests to this attempt, regardless of any token sent by
		// a compromised worker. Runner credentials never cross the boundary.
		req.Token = ar.token
		return r.handleBridge(req)
	})
	if err != nil {
		return nil, err // execute's terminal path removes the known container
	}
	return sess, nil
}

func (r *Runner) probeDocker(ctx context.Context, name string) (inst protocol.ProviderInstallation) {
	unavailable := func(err error) protocol.ProviderInstallation {
		return protocol.ProviderInstallation{Provider: name, AuthState: "unknown", Limitations: []string{err.Error()}}
	}
	staging, err := r.dockerStaging()
	if err != nil {
		return unavailable(err)
	}
	snapshot, err := agentconfig.StageIn(staging, name, "", r.opts.Docker.Env)
	if err != nil {
		return unavailable(err)
	}
	defer os.RemoveAll(snapshot.Dir)
	c, err := r.createDocker(ctx, "", true, snapshot, []string{"/usr/local/bin/yip", "agent-worker", "--provider", name})
	if err != nil {
		return unavailable(err)
	}
	defer func() {
		if !c.remove() {
			r.dockerUncertain()
			inst = unavailable(errors.New("probe container cleanup could not be confirmed; runner drained"))
		}
	}()
	inst, err = worker.Probe(ctx, c.attach(ctx))
	if err != nil {
		return unavailable(errors.New("agent image probe failed; check that the image includes yip and this provider"))
	}
	inst.Capabilities.SessionResume = false
	return inst
}

func (r *Runner) dockerToolchains(ctx context.Context) (tools map[string]string) {
	c, err := r.createDocker(ctx, "", true, agentconfig.Snapshot{}, []string{"/usr/local/bin/yip", "agent-worker", "--toolchains"})
	if err != nil {
		return map[string]string{}
	}
	defer func() {
		if !c.remove() {
			r.dockerUncertain()
			tools = map[string]string{}
		}
	}()
	out, err := c.attach(ctx).Output()
	if err != nil {
		return map[string]string{}
	}
	_ = json.Unmarshal(out, &tools)
	return tools
}

// reapDocker removes only containers owned by this state root's identities. It runs
// before accepting leases after a restart; --rm also covers normal worker exit.
func (r *Runner) reapDocker(ctx context.Context) error {
	seen := map[string]bool{}
	for _, node := range append([]string{r.id.NodeID}, r.id.PreviousNodeIDs...) {
		if node == "" || seen[node] {
			continue
		}
		seen[node] = true
		args := []string{"container", "ls", "--all", "--quiet", "--filter", "label=dev.yip.runner=" + node}
		out, err := dockerCommand(ctx, args...).Output()
		if err != nil {
			return errors.New("could not inspect orphaned agent containers")
		}
		for _, id := range strings.Fields(string(out)) {
			if err := dockerCommand(ctx, "rm", "--force", id).Run(); err != nil {
				return errors.New("could not remove an orphaned agent container")
			}
		}
		out, err = dockerCommand(ctx, args...).Output()
		if err != nil || strings.TrimSpace(string(out)) != "" {
			return errors.New("could not confirm removal of orphaned agent containers")
		}
	}
	// Reap sensitive staged imports only after every owned container is gone.
	// These are disposable copies; credentials, journals and workspaces stay.
	for _, state := range r.id.PreviousStateDirs {
		dir, err := identityStateDir(r.opts.StateDir, Identity{StateDir: state})
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(dir, "container-imports")); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Join(r.paths.Dir, "container-imports"))
}
