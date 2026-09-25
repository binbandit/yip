package providers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// baseEnvKeys are the only host environment variables passed to a provider by
// default. Provider-specific additions are explicit in each adapter.
var baseEnvKeys = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE",
	"TERM", "TMPDIR", "TZ", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"XDG_RUNTIME_DIR", "SSH_AUTH_SOCK", "GOPATH", "GOCACHE", "GOMODCACHE",
}

// BaseEnv builds an allowlisted environment from the current process plus
// the named extra keys and explicit KEY=VALUE overrides.
func BaseEnv(extraKeys []string, overrides ...string) []string {
	seen := map[string]bool{}
	var env []string
	add := func(k string) {
		if seen[k] {
			return
		}
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
			seen[k] = true
		}
	}
	for _, k := range baseEnvKeys {
		add(k)
	}
	for _, k := range extraKeys {
		add(k)
	}
	for _, kv := range overrides {
		k, _, _ := strings.Cut(kv, "=")
		for i, e := range env {
			if strings.HasPrefix(e, k+"=") {
				env = append(env[:i], env[i+1:]...)
				break
			}
		}
		env = append(env, kv)
	}
	return env
}

// Process wraps a provider subprocess running in its own process group so
// cancellation reaches every child the provider spawned.
type Process struct {
	Cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
}

// PrepareCommand configures cmd to run in a new process group. Call before Start.
func PrepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	// Never let Go's context cancellation send only SIGKILL to the leader;
	// Terminate handles the whole group.
	cmd.Cancel = nil
}

// StartProcess starts a prepared command and begins waiting on it.
func StartProcess(cmd *exec.Cmd) (*Process, error) {
	PrepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{Cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Done is closed when the process leader exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// Err returns the wait error after Done is closed.
func (p *Process) Err() error { return p.err }

// Exited reports whether the process leader has exited.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Terminate sends SIGTERM to the process group, waits up to grace, then
// SIGKILLs the group. It returns true when exit of the leader was confirmed
// and the group no longer has live members.
func (p *Process) Terminate(grace time.Duration) bool {
	if p == nil || p.Cmd.Process == nil {
		return true
	}
	pgid := p.Cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			return false
		}
	}
	// Reap any stragglers left in the group.
	if err := syscall.Kill(-pgid, 0); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		time.Sleep(100 * time.Millisecond)
		if err := syscall.Kill(-pgid, 0); err == nil {
			return false
		}
	}
	return true
}

// ExitCode extracts a process exit code from a wait error (-1 if unknown).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// LookPath resolves an executable explicitly, preferring an override path.
func LookPath(override string, names ...string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", err
		}
		return override, nil
	}
	var lastErr error
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// RunVersion runs `<exe> <args...>` with a short timeout and returns trimmed stdout.
func RunVersion(ctx context.Context, exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = BaseEnv(nil)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
