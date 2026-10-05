// Package install replaces the CLI and existing per-user macOS services.
package install

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Install copies source to an explicit binDir, an existing yip on PATH, or
// ~/.local/bin. It does not choose a service role or change Linux services.
func Install(source, binDir string, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	i := installer{home: home, uid: os.Getuid(), goos: runtime.GOOS, path: os.Getenv("PATH"), out: out,
		verify: verifyBinary, launchctl: launchctl, rename: os.Rename}
	return i.run(source, binDir)
}

type installer struct {
	home, goos, path string
	uid              int
	out              io.Writer
	verify           func(string) error
	launchctl        func(...string) error
	rename           func(string, string) error
}

type replacement struct {
	path, staged, backup string
	applied              bool
}

type agent struct {
	label, path string
	loaded      bool
	touched     bool
}

func verifyBinary(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "github.com/binbandit/yip/cmd/yip" || info.Main.Path != "github.com/binbandit/yip" {
		return fmt.Errorf("%s is not a verified yip CLI binary", path)
	}
	return nil
}

func launchctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (i *installer) run(source, binDir string) (result error) {
	if i.goos != "darwin" && i.goos != "linux" {
		return fmt.Errorf("CLI installation is not supported on %s", i.goos)
	}
	if i.uid == 0 {
		return errors.New("run the installer as your normal user, without sudo")
	}
	if err := i.verify(source); err != nil {
		return err
	}
	destination, err := i.destination(binDir)
	if err != nil {
		return err
	}
	if err := i.checkBinary(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create install directory (use --bin-dir for another location): %w", err)
	}
	// O_EXCL serializes installers using the same destination. A stale lock
	// after an interrupted install is deliberately left for manual inspection.
	lockPath := destination + ".install-lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("lock install destination (use --bin-dir for another location): %w", err)
	}
	_ = lock.Close()
	defer os.Remove(lockPath)
	// Different --bin-dir values still update the same per-user agents.
	userLockPath := filepath.Join(i.home, ".yip-install-lock")
	userLock, err := os.OpenFile(userLockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("lock user installation: %w", err)
	}
	_ = userLock.Close()
	defer os.Remove(userLockPath)
	var files []*replacement
	var agents []*agent
	defer func() {
		for _, f := range files {
			_ = os.Remove(f.staged)
			// Keep recovery files if rollback itself failed.
			if !f.applied {
				_ = os.Remove(f.backup)
			}
		}
	}()
	file, err := i.stage(destination, source, nil, 0o755)
	if err != nil {
		return err
	}
	files = append(files, file)
	if i.goos == "darwin" {
		for _, role := range []string{"hub", "runner"} {
			label := "dev.getyip." + role
			path := filepath.Join(i.home, "Library", "LaunchAgents", label+".plist")
			stat, err := i.ownedFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			original, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			updated, disabled, err := updatePlist(original, label, destination, i.checkBinary)
			if err != nil {
				return fmt.Errorf("refusing to replace %s: %w", path, err)
			}
			file, err := i.stage(path, "", updated, stat.Mode().Perm())
			if err != nil {
				return err
			}
			files = append(files, file)
			a := &agent{label: label, path: path}
			if err := i.launchctl("print", i.domain()); err != nil {
				return fmt.Errorf("cannot access your GUI launchd domain; log in locally before upgrading services: %w", err)
			}
			loaded, err := i.loaded(a)
			if err != nil {
				return err
			}
			// Never activate an unloaded job, or a Disabled definition.
			a.loaded = loaded && !disabled
			agents = append(agents, a)
		}
	}
	// All identities, permissions, service queries and staging must succeed
	// before any running job is stopped.
	defer func() {
		if result != nil {
			result = errors.Join(result, i.rollback(files, agents))
		}
	}()
	for _, a := range agents {
		if a.loaded {
			a.touched = true
			if err := i.launchctl("bootout", i.target(a)); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		if err := i.rename(f.staged, f.path); err != nil {
			return fmt.Errorf("replace %s: %w", f.path, err)
		}
		f.applied = true
	}
	for _, a := range agents {
		if a.loaded {
			if err := i.start(a); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		_ = os.Remove(f.backup)
		f.applied = false
	}
	fmt.Fprintf(i.out, "Installed yip at %s\n", destination)
	for _, a := range agents {
		if a.loaded {
			fmt.Fprintf(i.out, "Restarted %s with its existing settings.\n", a.label)
		} else {
			fmt.Fprintf(i.out, "Updated %s; left its activation state unchanged.\n", a.label)
		}
	}
	if len(agents) == 0 {
		fmt.Fprintln(i.out, "Start a hub with yip hub, or pair a runner with yip runner pair --help.\nFor background operation, choose yip service install hub or yip service install runner and follow its activation instructions.")
	}
	if !onPath(filepath.Dir(destination), i.path) {
		fmt.Fprintf(i.out, "Add %s to PATH to run yip by name.\n", filepath.Dir(destination))
	}
	if i.goos == "linux" {
		fmt.Fprintln(i.out, "Existing Linux services are unchanged; restart your yip systemd user service after installing.")
	}
	return nil
}

func (i *installer) destination(binDir string) (string, error) {
	if binDir != "" {
		return filepath.Abs(filepath.Join(binDir, "yip"))
	}
	for _, dir := range filepath.SplitList(i.path) {
		if dir == "" {
			dir = "."
		}
		candidate, err := filepath.Abs(filepath.Join(dir, "yip"))
		if err != nil {
			return "", err
		}
		stat, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if stat.Mode()&os.ModeSymlink != 0 || (stat.Mode().IsRegular() && stat.Mode().Perm()&0o111 != 0) {
			return candidate, nil
		}
	}
	return filepath.Join(i.home, ".local", "bin", "yip"), nil
}

func (i *installer) ownedFile(path string) (os.FileInfo, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	owner, ok := stat.Sys().(*syscall.Stat_t)
	if !stat.Mode().IsRegular() || !ok || int(owner.Uid) != i.uid || stat.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("refusing %s: expected a regular file owned by this user without group/world write access (symlinks are not replaced)", path)
	}
	return stat, nil
}

func (i *installer) checkBinary(path string) error {
	if _, err := i.ownedFile(path); err != nil {
		return err
	}
	return i.verify(path)
}

func (i *installer) domain() string         { return fmt.Sprintf("gui/%d", i.uid) }
func (i *installer) target(a *agent) string { return i.domain() + "/" + a.label }

func (i *installer) loaded(a *agent) (bool, error) {
	err := i.launchctl("print", i.target(a))
	if err == nil {
		return true, nil
	}
	// launchctl reports an absent service as ESRCH (3) or bootstrap's
	// BOOTSTRAP_UNKNOWN_SERVICE (113). Other failures are not absence.
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) && (exit.ExitCode() == 113 || exit.ExitCode() == 3) {
		return false, nil
	}
	return false, err
}

func (i *installer) start(a *agent) error {
	if err := i.launchctl("bootstrap", i.domain(), a.path); err != nil {
		return err
	}
	return i.launchctl("kickstart", i.target(a))
}

func (i *installer) rollback(files []*replacement, agents []*agent) error {
	var failures []error
	// Stop any newly bootstrapped jobs before putting their old files back.
	for _, a := range agents {
		if a.touched {
			loaded, err := i.loaded(a)
			if err == nil && loaded {
				err = i.launchctl("bootout", i.target(a))
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("rollback could not stop services; recovery files retained: %w", errors.Join(failures...))
	}
	for _, f := range files {
		if !f.applied {
			continue
		}
		var err error
		if f.backup == "" {
			err = os.Remove(f.path)
		} else {
			err = i.rename(f.backup, f.path)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("restore %s from %s: %w", f.path, f.backup, err))
		} else {
			f.applied = false
		}
	}
	if len(failures) == 0 {
		for _, a := range agents {
			if a.touched {
				if err := i.start(a); err != nil {
					failures = append(failures, err)
				}
			}
		}
	}
	if len(failures) != 0 {
		return fmt.Errorf("rollback needs manual recovery: %w", errors.Join(failures...))
	}
	return nil
}

func (i *installer) stage(path, source string, data []byte, mode os.FileMode) (*replacement, error) {
	r := &replacement{path: path}
	var err error
	if stat, statErr := os.Lstat(path); statErr == nil {
		r.backup, err = stageFile(path, path, nil, stat.Mode().Perm())
	} else if !errors.Is(statErr, os.ErrNotExist) {
		err = statErr
	}
	if err == nil {
		r.staged, err = stageFile(path, source, data, mode)
	}
	if err != nil {
		_ = os.Remove(r.backup)
		return nil, fmt.Errorf("stage %s (check directory permissions): %w", path, err)
	}
	return r, nil
}

func stageFile(path, source string, data []byte, mode os.FileMode) (name string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".yip-install-*")
	if err != nil {
		return "", err
	}
	name = f.Name()
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(name)
		}
	}()
	if source != "" {
		var input *os.File
		input, err = os.Open(source)
		if err == nil {
			_, err = io.Copy(f, input)
			_ = input.Close()
		}
	} else {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	return name, err
}

func onPath(dir, path string) bool {
	for _, entry := range filepath.SplitList(path) {
		abs, err := filepath.Abs(entry)
		if err == nil && abs == dir {
			return true
		}
	}
	return false
}
