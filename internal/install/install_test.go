package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", e) }
func (e exitCode) ExitCode() int { return int(e) }

type fixture struct {
	i          installer
	source     string
	old        string
	output     bytes.Buffer
	commands   [][]string
	loaded     map[string]bool
	overrides  map[string]bool
	fail       func([]string) error
	beforeStop func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("installer requires a normal user")
	}
	f := &fixture{loaded: map[string]bool{}, overrides: map[string]bool{}}
	f.i = installer{home: t.TempDir(), uid: os.Getuid(), goos: "darwin", out: &f.output, rename: os.Rename}
	f.i.verify = func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(data, []byte("yip-")) {
			return errors.New("foreign executable")
		}
		return nil
	}
	f.source = filepath.Join(t.TempDir(), "checkout with spaces", "bin", "yip")
	f.old = filepath.Join(f.i.home, "existing bin", "yip")
	f.i.path = filepath.Dir(f.old)
	writeFixture(t, f.source, []byte("yip-new"), 0o755)
	f.i.disabled = func(domain string) (map[string]bool, error) {
		args := []string{"print-disabled", domain}
		f.commands = append(f.commands, args)
		if f.fail != nil {
			if err := f.fail(args); err != nil {
				return nil, err
			}
		}
		return f.overrides, nil
	}
	f.i.launchctl = func(args ...string) error {
		f.commands = append(f.commands, append([]string(nil), args...))
		if f.fail != nil {
			if err := f.fail(args); err != nil {
				return err
			}
		}
		switch args[0] {
		case "print":
			if args[1] == f.i.domain() || f.loaded[args[1]] {
				return nil
			}
			return exitCode(113)
		case "bootout":
			if len(args) != 2 || !strings.HasPrefix(args[1], f.i.domain()+"/dev.getyip.") {
				t.Fatalf("unsafe bootout: %q", args)
			}
			if f.beforeStop != nil {
				f.beforeStop()
			}
			delete(f.loaded, args[1])
		case "bootstrap":
			if len(args) != 3 || args[1] != f.i.domain() {
				t.Fatalf("unsafe bootstrap: %q", args)
			}
			label := strings.TrimSuffix(filepath.Base(args[2]), ".plist")
			if f.overrides[label] {
				return errors.New("service is persistently disabled")
			}
			f.loaded[f.i.domain()+"/"+label] = true
		case "kickstart":
			if len(args) != 2 || !f.loaded[args[1]] {
				t.Fatalf("unexpected kickstart: %q", args)
			}
		default:
			t.Fatalf("unexpected launchctl action: %q", args)
		}
		return nil
	}
	return f
}

func writeFixture(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) service(t *testing.T, role string, loaded bool) (string, []byte) {
	t.Helper()
	writeFixture(t, f.old, []byte("yip-old"), 0o755)
	label := "dev.getyip." + role
	path := filepath.Join(f.i.home, "Library", "LaunchAgents", label+".plist")
	x := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>Program</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>%s</string><string>--state</string><string>/a custom/state &amp; data</string><string>--slots=4</string></array>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>/custom/bin:/usr/bin</string><key>EXAMPLE</key><string>hello &lt;world&gt;</string></dict>
<key>StandardOutPath</key><string>/custom logs/out.log</string>
<key>StandardErrorPath</key><string>/custom logs/error.log</string>
<key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>17</integer><key>WorkingDirectory</key><string>/working dir</string>
</dict></plist>`, label, f.old, f.old, role)
	data := []byte(x)
	writeFixture(t, path, data, 0o640)
	if loaded {
		f.loaded[f.i.domain()+"/"+label] = true
	}
	return path, data
}

func requireFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s: got %q, error %v; want %q", path, got, err, want)
	}
}

func TestFirstInstallAndRepeat(t *testing.T) {
	f := newFixture(t)
	for range 2 {
		if err := f.i.run(f.source, ""); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(f.i.home, ".local", "bin", "yip")
	requireFile(t, destination, []byte("yip-new"))
	stat, _ := os.Stat(destination)
	if stat.Mode().Perm() != 0o755 || len(f.commands) != 0 {
		t.Fatalf("mode %v; commands %q", stat.Mode(), f.commands)
	}
	if !strings.Contains(f.output.String(), "yip runner pair --help") || !strings.Contains(f.output.String(), "Add ") {
		t.Fatalf("missing setup guidance: %s", &f.output)
	}
}

func TestUpgradePreservesSettingsAndExactServiceTargets(t *testing.T) {
	f := newFixture(t)
	hub, beforeHub := f.service(t, "hub", true)
	runner, beforeRunner := f.service(t, "runner", false)
	unrelated := filepath.Join(filepath.Dir(hub), "org.example.agent.plist")
	writeFixture(t, unrelated, []byte("unrelated"), 0o644)
	workspace := filepath.Join(f.i.home, "workspaces", "edited.txt")
	writeFixture(t, workspace, []byte("uncommitted work"), 0o644)
	binDir := filepath.Join(f.i.home, "new bin & tools")
	f.beforeStop = func() {
		requireFile(t, hub, beforeHub)
		requireFile(t, runner, beforeRunner)
		requireFile(t, f.old, []byte("yip-old"))
	}
	if err := f.i.run(f.source, binDir); err != nil {
		t.Fatal(err)
	}
	requireFile(t, filepath.Join(binDir, "yip"), []byte("yip-new"))
	requireFile(t, f.old, []byte("yip-old"))
	requireFile(t, unrelated, []byte("unrelated"))
	requireFile(t, workspace, []byte("uncommitted work"))
	for path, original := range map[string][]byte{hub: beforeHub, runner: beforeRunner} {
		want, _, err := updatePlist(original, strings.TrimSuffix(filepath.Base(path), ".plist"), filepath.Join(binDir, "yip"), f.i.verify)
		if err != nil {
			t.Fatal(err)
		}
		requireFile(t, path, want)
		stat, _ := os.Stat(path)
		if stat.Mode().Perm() != 0o640 {
			t.Fatalf("plist mode changed: %v", stat.Mode())
		}
	}
	want := [][]string{
		{"print", f.i.domain()}, {"print", f.i.domain() + "/dev.getyip.hub"},
		{"print-disabled", f.i.domain()},
		{"print", f.i.domain()}, {"print", f.i.domain() + "/dev.getyip.runner"},
		{"bootout", f.i.domain() + "/dev.getyip.hub"},
		{"bootstrap", f.i.domain(), hub}, {"kickstart", f.i.domain() + "/dev.getyip.hub"},
	}
	if !reflect.DeepEqual(f.commands, want) {
		t.Fatalf("commands = %q, want %q", f.commands, want)
	}
}

func TestUpgradeBothServicesAndIdempotency(t *testing.T) {
	f := newFixture(t)
	f.service(t, "hub", true)
	f.service(t, "runner", true)
	for range 2 {
		if err := f.i.run(f.source, ""); err != nil {
			t.Fatal(err)
		}
		requireFile(t, f.old, []byte("yip-new"))
		if len(f.loaded) != 2 {
			t.Fatalf("loaded services: %v", f.loaded)
		}
	}
}

func TestFailureRestoresFilesAndPreviouslyLoadedServices(t *testing.T) {
	for _, failure := range []string{"bootout", "rename-binary", "rename-plist", "bootstrap", "kickstart"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			hub, beforeHub := f.service(t, "hub", true)
			runner, beforeRunner := f.service(t, "runner", true)
			failed := false
			f.fail = func(args []string) error {
				if !failed && args[0] == failure {
					failed = true
					return errors.New("injected service failure")
				}
				return nil
			}
			f.i.rename = func(from, to string) error {
				if !failed && ((failure == "rename-binary" && to == f.old) || (failure == "rename-plist" && to == runner)) {
					failed = true
					return errors.New("injected rename failure")
				}
				return os.Rename(from, to)
			}
			if err := f.i.run(f.source, ""); err == nil {
				t.Fatal("expected failure")
			}
			requireFile(t, f.old, []byte("yip-old"))
			requireFile(t, hub, beforeHub)
			requireFile(t, runner, beforeRunner)
			if len(f.loaded) != 2 {
				t.Fatalf("previous services not restored: %v", f.loaded)
			}
		})
	}
}

func TestUnsafeInputsFailBeforeServiceChanges(t *testing.T) {
	for _, bad := range []string{"binary", "binary-symlink", "plist-symlink", "plist-permissions", "wrong-owner", "label", "role", "program", "malformed", "duplicate-key", "no-gui", "status-error", "disabled-query", "locked", "user-locked", "permissions"} {
		t.Run(bad, func(t *testing.T) {
			f := newFixture(t)
			path, original := f.service(t, "runner", true)
			data := original
			switch bad {
			case "binary":
				writeFixture(t, f.old, []byte("other program"), 0o755)
			case "binary-symlink", "plist-symlink":
				target := f.old
				if bad == "plist-symlink" {
					target = path
				}
				if err := os.Rename(target, target+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target+".real", target); err != nil {
					t.Fatal(err)
				}
			case "plist-permissions":
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			case "wrong-owner":
				f.i.uid++
			case "label":
				data = bytes.Replace(data, []byte("dev.getyip.runner"), []byte("org.example.runner"), 1)
			case "role":
				data = bytes.Replace(data, []byte("<string>runner</string>"), []byte("<string>hub</string>"), 1)
			case "program":
				data = bytes.Replace(data, []byte("<key>Program</key><string>"+f.old), []byte("<key>Program</key><string>/foreign"), 1)
			case "malformed":
				data = []byte("not a plist")
			case "duplicate-key":
				data = bytes.Replace(data, []byte("<key>RunAtLoad</key>"), []byte("<key>Label</key>"), 1)
			case "no-gui", "status-error", "disabled-query":
				f.fail = func(args []string) error {
					if bad == "disabled-query" && args[0] == "print-disabled" {
						return exitCode(1)
					}
					if args[0] == "print" && ((bad == "no-gui" && args[1] == f.i.domain()) || (bad == "status-error" && args[1] != f.i.domain())) {
						return exitCode(1)
					}
					return nil
				}
			case "locked":
				writeFixture(t, f.old+".install-lock", []byte(""), 0o600)
			case "user-locked":
				writeFixture(t, filepath.Join(f.i.home, ".yip-install-lock"), []byte(""), 0o600)
			case "permissions":
				if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o755) })
			}
			if !bytes.Equal(data, original) {
				writeFixture(t, path, data, 0o640)
			}
			if err := f.i.run(f.source, ""); err == nil {
				t.Fatal("expected refusal")
			}
			for _, cmd := range f.commands {
				if cmd[0] != "print" && cmd[0] != "print-disabled" {
					t.Fatalf("service changed before preflight passed: %q", f.commands)
				}
			}
			if bad != "binary" {
				requireFile(t, f.old, []byte("yip-old"))
			}
		})
	}
}

func TestLiveLoadedStateOverridesDisabledPlistDefault(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		t.Run(fmt.Sprintf("loaded=%t", loaded), func(t *testing.T) {
			f := newFixture(t)
			path, data := f.service(t, "runner", loaded)
			// An explicit enable overrides the plist's Disabled default.
			f.overrides["dev.getyip.runner"] = false
			data = bytes.Replace(data, []byte("<key>RunAtLoad</key>"), []byte("<key>Disabled</key><true/><key>RunAtLoad</key>"), 1)
			writeFixture(t, path, data, 0o640)
			binDir := filepath.Join(f.i.home, "new bin")
			if err := f.i.run(f.source, binDir); err != nil {
				t.Fatal(err)
			}
			target := f.i.domain() + "/dev.getyip.runner"
			want := [][]string{{"print", f.i.domain()}, {"print", target}}
			if loaded {
				want = append(want, []string{"print-disabled", f.i.domain()}, []string{"bootout", target}, []string{"bootstrap", f.i.domain(), path}, []string{"kickstart", target})
			}
			if !reflect.DeepEqual(f.commands, want) || f.loaded[target] != loaded {
				t.Fatalf("commands %q, loaded %v; want %q, loaded %v", f.commands, f.loaded, want, loaded)
			}
			updated, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			root, err := parsePlist(updated)
			if err != nil {
				t.Fatal(err)
			}
			dict := &root.Nodes[0]
			if dict.get("Disabled").XMLName.Local != "true" || dict.get("ProgramArguments").Nodes[0].Text != filepath.Join(binDir, "yip") {
				t.Fatalf("updated definition lost its default or new executable: %s", updated)
			}
		})
	}
}

func TestLinuxCopiesBinaryWithoutLaunchctl(t *testing.T) {
	f := newFixture(t)
	f.i.goos = "linux"
	if err := f.i.run(f.source, filepath.Join(f.i.home, "custom bin")); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 0 || !strings.Contains(f.output.String(), "systemd") {
		t.Fatalf("commands %q, output %q", f.commands, &f.output)
	}
}

func TestLoadedDisabledServiceRefusedBeforeStoppingAnything(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted=%t", persisted), func(t *testing.T) {
			f := newFixture(t)
			hub, beforeHub := f.service(t, "hub", true)
			runner, beforeRunner := f.service(t, "runner", true)
			if persisted {
				f.overrides["dev.getyip.runner"] = true
			} else {
				beforeRunner = bytes.Replace(beforeRunner, []byte("<key>RunAtLoad</key>"), []byte("<key>Disabled</key><true/><key>RunAtLoad</key>"), 1)
				writeFixture(t, runner, beforeRunner, 0o640)
			}
			if err := f.i.run(f.source, ""); err == nil || !strings.Contains(err.Error(), "loaded but disabled") {
				t.Fatalf("expected refusal before bootout, got %v", err)
			}
			requireFile(t, f.old, []byte("yip-old"))
			requireFile(t, hub, beforeHub)
			requireFile(t, runner, beforeRunner)
			if len(f.loaded) != 2 {
				t.Fatalf("lost previously loaded services: %v", f.loaded)
			}
			for _, cmd := range f.commands {
				if cmd[0] != "print" && cmd[0] != "print-disabled" {
					t.Fatalf("mutated service activation: %q", f.commands)
				}
			}
		})
	}
}

func TestUnloadedPersistentlyDisabledServiceStaysUnloaded(t *testing.T) {
	f := newFixture(t)
	f.service(t, "runner", false)
	f.overrides["dev.getyip.runner"] = true
	if err := f.i.run(f.source, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.loaded) != 0 || !f.overrides["dev.getyip.runner"] {
		t.Fatalf("activation state changed: loaded=%v, overrides=%v", f.loaded, f.overrides)
	}
}

func TestDisabledOutputParsingFailsClosed(t *testing.T) {
	got, err := parseDisabled([]byte("disabled services = {\n\t\"dev.getyip.runner\" => false\n\t\"another.job\" => true\n}\n"))
	if err != nil || !reflect.DeepEqual(got, map[string]bool{"dev.getyip.runner": false, "another.job": true}) {
		t.Fatalf("overrides=%v, error=%v", got, err)
	}
	if got, err := parseDisabled([]byte("disabled services = {\n}\n")); err != nil || len(got) != 0 {
		t.Fatalf("empty overrides=%v, error=%v", got, err)
	}
	for _, input := range []string{"", "{}", "disabled services = {\n\"x\" => unknown\n}", "disabled services = {\n\"x\" => true\n\"x\" => false\n}", "disabled services = {\n}\nextra"} {
		f := newFixture(t)
		f.service(t, "runner", true)
		f.i.disabled = func(string) (map[string]bool, error) { return parseDisabled([]byte(input)) }
		if err := f.i.run(f.source, ""); err == nil {
			t.Fatalf("accepted ambiguous activation output %q", input)
		}
		requireFile(t, f.old, []byte("yip-old"))
		for _, cmd := range f.commands {
			if cmd[0] != "print" {
				t.Fatalf("changed services before parsing activation state: %q", f.commands)
			}
		}
	}
}

func TestRejectForeignBinaryWithoutExecutingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yip")
	writeFixture(t, path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if err := verifyBinary(path); err == nil {
		t.Fatal("accepted a shell script as yip")
	}
}
