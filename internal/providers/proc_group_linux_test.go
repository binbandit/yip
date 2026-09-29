package providers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupRetainedZombie(t *testing.T) {
	// Keep the leader alive until Terminate, and deliberately do not Wait on
	// its group member: this test, not init or a shell, owns zombie reaping.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	leader := exec.Command("sh", "-c", "read line")
	leader.Stdin = r
	p, err := StartProcess(leader)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Terminate(time.Second)
	child := exec.Command("sh", "-c", "exit 0")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child.Process.Pid))
		_, _, state, ok := parseProcStat(string(data))
		if err == nil && ok && state == 'Z' {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child did not become an unreaped zombie: %s (%v)", data, err)
		}
		time.Sleep(time.Millisecond)
	}
	pgid := leader.Process.Pid
	if processGroupExited(pgid) {
		t.Fatal("confirmed exit while group leader was still live")
	}
	if !p.Terminate(time.Second) {
		t.Fatal("did not confirm terminated leader plus retained zombie")
	}
	if !p.Exited() {
		t.Fatal("leader was not waited")
	}
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("zombie group must still answer signal zero: %v", err)
	}
	if !processGroupExited(pgid) {
		t.Fatal("zombie-only group still reported live")
	}
}

func procStatFixture(pid, group int, state string) string {
	return fmt.Sprintf("%d (name with ) and (\n) %s 1 %d%s", pid, state, group, strings.Repeat(" 0", 47))
}

func TestParseProcStat(t *testing.T) {
	for _, state := range []string{"Z", "X", "x", "R", "S", "D", "T", "I"} {
		pid, group, got, ok := parseProcStat(procStatFixture(123, 456, state))
		if !ok || pid != 123 || group != 456 || got != state[0] {
			t.Fatalf("parse %s: %d %d %c %v", state, pid, group, got, ok)
		}
	}
	for _, input := range []string{
		"", "123 (bad) Z 1 456", procStatFixture(123, 456, "?"),
		procStatFixture(123, 456, "ZZ"), procStatFixture(0, 456, "Z"),
		procStatFixture(123, -1, "Z"), procStatFixture(123, 456, "Z") + " broken",
	} {
		if _, _, _, ok := parseProcStat(input); ok {
			t.Errorf("accepted malformed stat %q", input)
		}
	}
}

func TestProcMountVisible(t *testing.T) {
	base := "1 0 0:1 / /proc rw - proc proc rw"
	for _, tc := range []struct {
		name, mounts string
		want         bool
	}{
		{"plain", base, true},
		{"explicit", base + ",hidepid=0", true},
		{"sys overlay", base + "\n2 1 0:1 /sys /proc/sys ro - proc proc rw", true},
		{"hidepid", base + ",hidepid=2", false},
		{"hidepid invisible", base + ",hidepid=invisible,gid=42", false},
		{"hidepid ptrace", base + ",hidepid=ptraceable", false},
		{"pid overlay", base + "\n2 1 0:2 / /proc/123 rw - tmpfs tmpfs rw", false},
		{"self overlay", base + "\n2 1 0:2 / /proc/self rw - tmpfs tmpfs rw", false},
		{"subtree", strings.Replace(base, " / /proc ", " /subset /proc ", 1), false},
		{"not procfs", strings.Replace(base, "- proc ", "- tmpfs ", 1), false},
		{"duplicate", base + "\n" + base, false},
		{"missing", "", false},
		{"malformed", base + "\nbad", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := procMountVisible(tc.mounts, "/proc"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProcPIDNamespaceMatches(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"Name:\ttest\nNSpid:\t123\n", true},
		{"NSpid:\t456\t123\n", false},
		{"NSpid:\t123\t123\n", false},
		{"NSpid:\t456\n", false},
		{"NSpid:\tinvalid\n", false},
		{"NSpid:\n", false},
		{"Name:\ttest\n", false},
	} {
		if got := procPIDNamespaceMatches(tc.status, 123); got != tc.want {
			t.Errorf("status %q: got %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestProcGroupDeadFailsClosed(t *testing.T) {
	for _, scenario := range []string{
		"dead", "live", "unknown", "bad stat", "missing stat", "unreadable stat", "empty",
		"live thread", "missing tasks", "bad task", "wrong namespace", "wrong self",
		"hidden", "missing mountinfo",
	} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, contents string) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			mounts := "1 0 0:1 / " + root + " rw - proc proc rw"
			if scenario == "hidden" {
				mounts += ",hidepid=2"
			}
			if scenario != "missing mountinfo" {
				write("self/mountinfo", mounts)
			}
			selfPID := os.Getpid()
			if scenario == "wrong self" {
				selfPID++
			}
			write("self/stat", procStatFixture(selfPID, 7, "S"))
			write("1/stat", procStatFixture(1, 1, "S"))
			status := fmt.Sprintf("NSpid:\t%d\n", os.Getpid())
			if scenario == "wrong namespace" {
				status = fmt.Sprintf("NSpid:\t%d\t%d\n", os.Getpid(), os.Getpid())
			}
			write("self/status", status)
			if scenario != "empty" {
				state := "Z"
				if scenario == "live" {
					state = "S"
				} else if scenario == "unknown" {
					state = "?"
				}
				stat := procStatFixture(123, 42, state)
				if scenario == "bad stat" {
					stat = "broken"
				}
				if scenario != "missing stat" {
					write("123/stat", stat)
				}
				if scenario == "unreadable stat" {
					if err := os.Remove(filepath.Join(root, "123/stat")); err != nil {
						t.Fatal(err)
					}
					// A directory deterministically fails ReadFile, even as root.
					if err := os.Mkdir(filepath.Join(root, "123/stat"), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "missing tasks" {
					task := procStatFixture(123, 42, "Z")
					if scenario == "bad task" {
						task = "broken"
					}
					write("123/task/123/stat", task)
					if scenario == "live thread" {
						write("123/task/124/stat", procStatFixture(124, 42, "R"))
					}
				}
			}
			if got := procGroupDead(root, 42); got != (scenario == "dead") {
				t.Fatalf("confirmation = %v for %s", got, strconv.Quote(scenario))
			}
		})
	}
}
