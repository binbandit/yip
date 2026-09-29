package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/protocol"
)

// workspaceKinds maps a workspace directory prefix to its kind.
var workspaceKinds = map[string]string{
	"job-": "job", "review-": "review", "scratch-": "scratch",
	"docker-job-": "job", "docker-review-": "review", "docker-scratch-": "scratch",
}

// listWorkspaces reports every workspace under the work directory: its
// branch and head, uncommitted changes, size, and whether an active attempt
// is using it. Nothing is modified.
func (r *Runner) listWorkspaces(ctx context.Context) []protocol.WorkspaceInfo {
	entries, err := os.ReadDir(r.paths.work())
	if err != nil {
		return []protocol.WorkspaceInfo{}
	}
	out := []protocol.WorkspaceInfo{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		kind, ref := workspaceKind(e.Name())
		if kind == "" {
			continue
		}
		dir := filepath.Join(r.paths.work(), e.Name())
		info := protocol.WorkspaceInfo{Name: e.Name(), Kind: kind, Ref: ref, InUse: r.workspaceInUse(kind, ref, dir)}
		if fi, err := e.Info(); err == nil {
			info.ModifiedAt = fi.ModTime().UTC()
		}
		if kind != "scratch" {
			gctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			ws := &Workspace{Dir: dir, Isolated: strings.HasPrefix(e.Name(), "docker-")}
			info.Branch, _ = ws.git(gctx, "branch", "--show-current")
			info.Head, _ = ws.git(gctx, "rev-parse", "HEAD")
			if st, err := ws.status(gctx); err == nil && strings.TrimSpace(st) != "" {
				info.Changes = len(strings.Split(strings.TrimSpace(st), "\n"))
			}
			cancel()
		}
		info.SizeBytes, info.SizeKnown, info.SizeApprox = measureDir(dir)
		info.SizeMB = info.SizeBytes / (1 << 20)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out
}

func workspaceKind(name string) (kind, ref string) {
	for prefix, k := range workspaceKinds {
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" && !strings.ContainsAny(rest, `/\.`) {
			return k, rest
		}
	}
	return "", ""
}

func (r *Runner) workspaceInUse(kind, ref, dir string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ar := range r.runs {
		ar.mu.Lock()
		m, ws := ar.m, ar.ws
		ar.mu.Unlock()
		switch {
		case ws != nil && ws.Dir == dir:
			return true
		case kind == "job" && domain.Short(m.JobID) == ref:
			return true
		case kind != "job" && domain.Short(m.RunID) == ref:
			return true
		}
	}
	return false
}

// sizeWalkLimit is how many entries measureDir visits before it stops, so a
// huge dependency tree can't stall a probe.
var sizeWalkLimit = 200_000

// measureDir sums the sizes of the files under dir. known is false when dir
// couldn't be read at all, so nothing was measured; approx is true when the
// walk stopped at sizeWalkLimit or skipped something it couldn't read, so
// the total is a lower bound.
func measureDir(dir string) (total int64, known, approx bool) {
	n := 0
	rootErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			approx = true
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if n++; n > sizeWalkLimit {
			approx = true
			return fs.SkipAll
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				approx = true
				return nil
			}
			total += fi.Size()
		}
		return nil
	})
	if rootErr != nil {
		return 0, false, false
	}
	return total, true, approx
}

// cleanupWorkspace deletes one workspace on the owner's explicit request.
// It refuses a workspace an active attempt is using, and one with
// uncommitted changes unless forced (A24).
func (r *Runner) cleanupWorkspace(ctx context.Context, req protocol.CleanupWorkspace) error {
	kind, ref := workspaceKind(req.Workspace)
	if kind == "" || filepath.Base(req.Workspace) != req.Workspace {
		return fmt.Errorf("%q isn't a workspace on this machine", req.Workspace)
	}
	dir := filepath.Join(r.paths.work(), req.Workspace)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s no longer exists on this machine", req.Workspace)
	}
	if r.workspaceInUse(kind, ref, dir) {
		return fmt.Errorf("%s is being used by a running attempt", req.Workspace)
	}
	if kind != "scratch" && !req.Force {
		ws := &Workspace{Dir: dir, Isolated: strings.HasPrefix(req.Workspace, "docker-")}
		st, err := ws.status(ctx)
		if err != nil {
			return fmt.Errorf("could not verify %s has no uncommitted changes; inspect it or explicitly force cleanup: %w", req.Workspace, err)
		}
		if strings.TrimSpace(st) != "" {
			return fmt.Errorf("%s has %d uncommitted changes; confirm that they may be lost", req.Workspace, len(strings.Split(strings.TrimSpace(st), "\n")))
		}
	}
	_ = makeWritable(dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	// Forget the worktree in every replica; the branch itself stays.
	if reps, _ := filepath.Glob(filepath.Join(r.paths.replicas(), "*")); len(reps) > 0 {
		for _, rep := range reps {
			_, _ = git(ctx, rep, "worktree", "prune")
		}
	}
	r.log.Info("workspace removed on the owner's request", "workspace", req.Workspace, "forced", req.Force)
	return nil
}

func (r *Runner) onCleanup(ctx context.Context, f protocol.Frame) error {
	var req protocol.CleanupWorkspace
	ack := protocol.CommandAck{CommandID: f.ID, OK: true}
	if err := json.Unmarshal(f.Payload, &req); err != nil {
		ack.OK, ack.Error = false, "malformed request"
	} else if err := r.cleanupWorkspace(ctx, req); err != nil {
		ack.OK, ack.Error = false, err.Error()
	}
	go r.probeOnce(ctx) // report the new list
	return r.sendTyped(protocol.EvCommandAck, "", 0, ack)
}
