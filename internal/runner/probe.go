package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/binbandit/yip/protocol"
)

func runtimeOS() string   { return runtime.GOOS }
func runtimeArch() string { return runtime.GOARCH }

func diskFreeMB(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize) / (1 << 20)
}

// probeLoop refreshes capabilities periodically (sign-in state can change).
func (r *Runner) probeLoop(ctx context.Context) {
	r.probeOnce(ctx)
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.probeOnce(ctx)
		}
	}
}

func (r *Runner) probeOnce(ctx context.Context) {
	caps := r.Probe(ctx)
	r.caps.Store(caps)
	_ = r.sendTyped(protocol.EvCapabilities, "", 0, caps)
}

// Probe discovers providers, toolchains, and execution profiles.
func (r *Runner) Probe(ctx context.Context) protocol.RunnerCapabilities {
	caps := protocol.RunnerCapabilities{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Slots: r.opts.Slots,
		DiskFreeMB: diskFreeMB(r.paths.Dir), MemMB: memMB(), Toolchains: map[string]string{}, ServiceState: serviceState()}
	var dockerErr error
	if r.opts.ExecutionProfile == "docker" {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		dockerErr = r.dockerReady(pctx)
		if dockerErr == nil {
			out, err := dockerCommand(pctx, "image", "inspect", "--format", "{{.Architecture}}", r.opts.Docker.Image).Output()
			if err != nil || strings.TrimSpace(string(out)) == "" {
				dockerErr = errors.New("could not determine the agent image architecture")
			} else {
				caps.Arch = strings.TrimSpace(string(out))
			}
		}
		cancel()
		caps.OS, caps.CPUs, caps.MemMB = "linux", 2, 4096
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, a := range r.opts.Adapters {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var inst protocol.ProviderInstallation
			if r.opts.ExecutionProfile == "docker" {
				if dockerErr != nil {
					inst = protocol.ProviderInstallation{Provider: name, AuthState: protocol.AuthError, AuthDetail: dockerErr.Error()}
				} else {
					inst = r.probeDocker(pctx, name)
				}
			} else {
				inst = a.Probe(pctx)
			}
			if inst.Provider == "" {
				inst.Provider = name
			}
			if inst.ProfileID == "" {
				// Installations of one account share its allowance; without a
				// vendor account label they are pooled per provider.
				inst.ProfileID = name + ":" + firstNonEmpty(inst.Account, "default")
			}
			mu.Lock()
			caps.Providers = append(caps.Providers, inst)
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	if r.opts.ExecutionProfile == "docker" {
		if dockerErr == nil {
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			caps.Toolchains = r.dockerToolchains(pctx)
			cancel()
		}
	} else {
		caps.Toolchains = ProbeToolchains(ctx)
	}
	caps.Profiles = r.profiles(caps)
	if r.uncertain.Load() {
		dockerErr = errors.New("container cleanup is unconfirmed; restore Docker connectivity and restart this runner")
	}
	if dockerErr != nil {
		for i := range caps.Profiles {
			caps.Profiles[i].Available = false
			caps.Profiles[i].Reason = dockerErr.Error()
		}
	}
	caps.Workspaces = r.listWorkspaces(ctx)
	return caps
}

// ProbeToolchains runs inside the same execution environment as the agents.
func ProbeToolchains(ctx context.Context) map[string]string {
	tools := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for tool, args := range map[string][]string{"git": {"--version"}, "go": {"version"}, "node": {"--version"}, "python3": {"--version"},
		"docker": {"--version"}, "cargo": {"--version"}, "swift": {"--version"}, "xcodebuild": {"-version"}, "gh": {"--version"}} {
		wg.Add(1)
		go func(tool string, args []string) {
			defer wg.Done()
			if v := toolVersion(ctx, tool, args...); v != "" {
				mu.Lock()
				tools[tool] = v
				mu.Unlock()
			}
		}(tool, args)
	}
	wg.Wait()
	return tools
}

func (r *Runner) profiles(caps protocol.RunnerCapabilities) []protocol.ExecutionProfile {
	if r.opts.ExecutionProfile == "docker" {
		return []protocol.ExecutionProfile{
			{Name: "container", Available: true, Summary: "A fresh non-root Docker container per attempt. Only the job workspace and disposable copies of selected harness configuration are exposed."},
			{Name: "readonly", Available: true, Summary: "A read-only workspace mount in an ephemeral Docker container; checks also run in containers."},
			{Name: "native", Available: false, Reason: "This runner only executes agents and checks in Docker; it never falls back to the host."},
		}
	}
	if r.opts.ExecutionProfile == "container" {
		return []protocol.ExecutionProfile{
			{Name: "container", Available: true, Summary: "Runner inside a restricted Linux container: non-root, workspace volume only, no host Docker socket or home directory."},
			{Name: "readonly", Available: true, Summary: "Read-only snapshot of an exact revision inside the container."},
			{Name: "native", Available: false, Reason: "This runner is containerized; native host tooling is not exposed."},
		}
	}
	out := []protocol.ExecutionProfile{
		{Name: "native", Available: true, Summary: "Trusted native execution in a per-job git worktree under the runner's account, using each provider's own sandbox and permission controls. A worktree is not a sandbox."},
		{Name: "readonly", Available: true, Summary: "Detached, read-only snapshot of the exact revision, with the provider in its read-only permission mode."},
	}
	container := protocol.ExecutionProfile{Name: "container", Available: false,
		Reason: "Start this runner with --profile docker for ephemeral containers, or run a containerized runner (packaging/container)."}
	if _, ok := caps.Toolchains["docker"]; !ok {
		container.Reason = "Docker is not installed here. " + container.Reason
	}
	return append(out, container)
}

func toolVersion(ctx context.Context, tool string, args ...string) string {
	if _, err := exec.LookPath(tool); err != nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, tool, args...).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return line
}

func memMB() int64 {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			var n int64
			for _, c := range strings.TrimSpace(string(out)) {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int64(c-'0')
			}
			return n / (1 << 20)
		}
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					f := strings.Fields(line)
					if len(f) >= 2 {
						var n int64
						for _, c := range f[1] {
							n = n*10 + int64(c-'0')
						}
						return n / 1024
					}
				}
			}
		}
	}
	return 0
}

// serviceState reports how this runner is supervised, so the UI can show the
// actual tested state rather than promising unconditional uptime.
func serviceState() string {
	if os.Getenv("INVOCATION_ID") != "" {
		return "systemd service"
	}
	if os.Getenv("XPC_SERVICE_NAME") != "" && strings.Contains(os.Getenv("XPC_SERVICE_NAME"), "yip") {
		return "launchd service"
	}
	return "foreground process (not supervised: stops if this session ends)"
}
