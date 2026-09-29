package providers

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processGroupExited(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	if err == syscall.ESRCH {
		return true
	}
	if err != nil {
		return false
	}
	return procGroupDead("/proc", pgid)
}

// procGroupDead requires positive evidence: at least one group member, all
// members and their threads dead, and an unrestricted view of our PID namespace.
// In particular, hidepid can silently omit live processes from ReadDir.
func procGroupDead(root string, pgid int) bool {
	mounts, err := os.ReadFile(filepath.Join(root, "self/mountinfo"))
	if err != nil || !procMountVisible(string(mounts), root) {
		return false
	}
	status, err := os.ReadFile(filepath.Join(root, "self/status"))
	if err != nil || !procPIDNamespaceMatches(string(status), os.Getpid()) {
		return false
	}
	self, err := os.ReadFile(filepath.Join(root, "self/stat"))
	pid, _, _, ok := parseProcStat(string(self))
	if err != nil || !ok || pid != os.Getpid() {
		return false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	found := false
	seen := make(map[string]bool)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		seen[entry.Name()] = true
		dir := filepath.Join(root, entry.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		id, group, state, ok := parseProcStat(string(stat))
		// Even a disappearing process leaves uncertainty about membership.
		if err != nil || !ok || id != pid {
			return false
		}
		if group != pgid {
			continue
		}
		found = true
		if !deadProcState(state) {
			return false
		}
		// A zombie thread-group leader may still have executing threads.
		tasks, err := os.ReadDir(filepath.Join(dir, "task"))
		if err != nil || len(tasks) == 0 {
			return false
		}
		for _, task := range tasks {
			tid, err := strconv.Atoi(task.Name())
			if err != nil || tid <= 0 {
				return false
			}
			stat, err := os.ReadFile(filepath.Join(dir, "task", task.Name(), "stat"))
			id, group, state, ok := parseProcStat(string(stat))
			if err != nil || !ok || id != tid || group != pgid || !deadProcState(state) {
				return false
			}
		}
	}
	// A member could fork after the first directory listing and become dead
	// before its stat was read. Do not confirm if a new PID appeared meanwhile.
	entries, err = os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 0 && !seen[entry.Name()] {
			return false
		}
	}
	return found
}

func deadProcState(state byte) bool { return state == 'Z' || state == 'X' || state == 'x' }

// NSpid lists IDs from the procfs mount's PID namespace inward. A single
// matching ID proves the mount is not showing an ancestor namespace's IDs.
// Kernels without NSpid (before Linux 4.1) conservatively remain unconfirmed.
func procPIDNamespaceMatches(status string, pid int) bool {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			return len(fields) == 2 && fields[1] == strconv.Itoa(pid)
		}
	}
	return false
}

// comm is parenthesized but may itself contain spaces, newlines and parentheses.
func parseProcStat(stat string) (pid, pgid int, state byte, ok bool) {
	open, close := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 1 || close <= open {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(stat[:open]))
	if err != nil || pid <= 0 {
		return 0, 0, 0, false
	}
	fields := strings.Fields(stat[close+1:])
	if len(fields) < 50 || len(fields[0]) != 1 || !strings.Contains("RSDZTtXxKWPI", fields[0]) {
		return 0, 0, 0, false
	}
	// Validate every numeric field, not only pgrp, so damaged stat records
	// cannot accidentally be accepted as evidence of death.
	for _, field := range fields[1:] {
		number := strings.TrimPrefix(field, "-")
		if _, err := strconv.ParseUint(number, 10, 64); err != nil {
			return 0, 0, 0, false
		}
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil || pgid < 0 {
		return 0, 0, 0, false
	}
	return pid, pgid, fields[0][0], true
}

func procMountVisible(mountinfo, root string) bool {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(mountinfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return false
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || len(fields) != separator+4 {
			return false
		}
		mountpoint := fields[4]
		if mountpoint == root {
			if found || fields[3] != "/" || fields[separator+1] != "proc" {
				return false
			}
			found = true
			for _, option := range strings.Split(fields[5]+","+fields[separator+3], ",") {
				if strings.HasPrefix(option, "hidepid=") && option != "hidepid=0" && option != "hidepid=off" {
					return false
				}
			}
		} else if strings.HasPrefix(mountpoint, root+"/") {
			component := strings.Split(strings.TrimPrefix(mountpoint, root+"/"), "/")[0]
			// Reject overlays that could hide a PID or alter our visibility
			// checks. Standard /proc/sys, /proc/irq, etc. mounts are harmless.
			if _, err := strconv.Atoi(component); err == nil || component == "self" || component == "thread-self" || strings.Contains(component, `\`) {
				return false
			}
		}
	}
	return found
}
