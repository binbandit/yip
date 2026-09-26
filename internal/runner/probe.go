package runner

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/binbandit/yip/protocol"
)

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
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, a := range r.opts.Adapters {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			inst := a.Probe(pctx)
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
	// Toolchains a project can require (ProjectPolicy.Requires).
	for tool, args := range map[string][]string{"git": {"--version"}, "go": {"version"}, "node": {"--version"}, "python3": {"--version"},
		"docker": {"--version"}, "cargo": {"--version"}, "swift": {"--version"}, "xcodebuild": {"-version"}} {
		wg.Add(1)
		go func(tool string, args []string) {
			defer wg.Done()
			if v := toolVersion(ctx, tool, args...); v != "" {
				mu.Lock()
				caps.Toolchains[tool] = v
				mu.Unlock()
			}
		}(tool, args)
	}
	wg.Wait()
	caps.Profiles = r.profiles(caps)
	caps.Workspaces = r.listWorkspaces(ctx)
	return caps
}

func (r *Runner) profiles(caps protocol.RunnerCapabilities) []protocol.ExecutionProfile {
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
		Reason: "Run a runner inside the yip container image (packaging/container) to offer this profile; this runner runs natively."}
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
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

func memMB() int64 {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
			return n / (1 << 20)
		}
	case "linux":
		b, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
					n, _ := strconv.ParseInt(f[1], 10, 64)
					return n / 1024
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
