package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func (h *Hub) loop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.closed:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.kick:
		}
		h.tick(ctx)
	}
}

// Tick runs one scheduler pass (exported for tests).
func (h *Hub) Tick(ctx context.Context) { h.tick(ctx) }

func (h *Hub) tick(ctx context.Context) {
	if h.Org().ID == "" {
		return
	}
	h.expireLeases(ctx)
	h.expireApprovals(ctx)
	h.retryDue(ctx)
	h.nodeHealth(ctx)
	h.schedule(ctx)
	if h.now().Sub(h.lastPRPoll) > time.Minute {
		h.lastPRPoll = h.now()
		go h.pollPRs(context.Background())
	}
	for _, id := range h.nodes.ids() {
		h.flushOutbox(ctx, id)
	}
}

type placement struct {
	node store.NodeRow
	inst protocol.ProviderInstallation
}

type schedCounts struct {
	engineer map[string]int
	profile  map[string]int
	node     map[string]int
	org      int
}

func (h *Hub) counts(ctx context.Context) (schedCounts, error) {
	c := schedCounts{engineer: map[string]int{}, profile: map[string]int{}, node: map[string]int{}}
	runs, err := store.RunsInStates(ctx, h.st.R(), protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
		protocol.RunAwaitingInput, protocol.RunStopping)
	if err != nil {
		return c, err
	}
	for _, r := range runs {
		c.engineer[r.EngineerID]++
		c.profile[r.ProfileID]++
		c.node[r.NodeID]++
		c.org++
	}
	return c, nil
}

func (h *Hub) profileMax(ctx context.Context, profileID string) int {
	var n int
	if err := h.st.R().QueryRowContext(ctx, `SELECT max_concurrency FROM provider_profiles WHERE id = ?`, profileID).Scan(&n); err != nil || n < 1 {
		return h.lim.ActiveRunsPerAccount
	}
	return n
}

// schedule offers created runs to eligible machines, fairly and with the
// configured concurrency limits.
func (h *Hub) schedule(ctx context.Context) {
	runs, err := store.RunsInStates(ctx, h.st.R(), protocol.RunCreated)
	if err != nil || len(runs) == 0 {
		return
	}
	counts, err := h.counts(ctx)
	if err != nil {
		return
	}
	nodes, err := store.ListNodes(ctx, h.st.R())
	if err != nil {
		return
	}
	type item struct {
		run store.RunRow
		job store.JobRow
		key float64
	}
	now := h.now()
	var items []item
	perProject := map[string]int{}
	for _, r := range runs {
		j, err := store.GetJob(ctx, h.st.R(), r.JobID)
		if err != nil {
			continue
		}
		if !domain.JobLive(j.State) {
			_ = h.do(ctx, func(t *txn) error {
				_ = store.SetRunState(ctx, t.tx, r.ID, protocol.RunCancelled, "job is "+string(j.State))
				return h.runChanged(ctx, t, r.ID)
			})
			continue
		}
		// Priority first, then age; projects already holding many queued
		// runs are aged more slowly so one project cannot starve others.
		age := now.Sub(r.CreatedAt).Seconds()
		perProject[j.ProjectID]++
		items = append(items, item{run: r, job: j, key: float64(j.Priority)*1e6 + age/float64(perProject[j.ProjectID])})
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].key > items[b].key })
	for _, it := range items {
		r, j := it.run, it.job
		if r.RetryAt != nil && now.Before(*r.RetryAt) {
			continue
		}
		if counts.engineer[r.EngineerID] >= h.lim.ActiveRunsPerEngineer {
			h.noteQueued(ctx, j, h.engineerName(ctx, h.st.R(), r.EngineerID)+" is finishing other work first.")
			continue
		}
		if counts.org >= h.lim.ActiveRunsPerOrg {
			h.noteQueued(ctx, j, fmt.Sprintf("Waiting for a slot: %d runs are active across the workspace.", counts.org))
			continue
		}
		p, why, impossible := h.place(ctx, r, j, nodes, counts)
		if p == nil {
			if impossible {
				h.noteWaiting(ctx, j, why)
			} else {
				h.noteQueued(ctx, j, why)
			}
			continue
		}
		if err := h.offer(ctx, r, j, *p); err != nil {
			h.log.Warn("offer failed", "run", r.ID, "err", err)
			continue
		}
		counts.engineer[r.EngineerID]++
		counts.profile[p.inst.ProfileID]++
		counts.node[p.node.ID]++
		counts.org++
	}
}

// place selects a node for a run, or explains why none can take it.
// impossible is true when no machine could run it even if idle (missing
// provider, sign-in, capability), as opposed to temporarily busy.
func (h *Hub) place(ctx context.Context, r store.RunRow, j store.JobRow, nodes []store.NodeRow, counts schedCounts) (*placement, string, bool) {
	provider := ProviderLabel(r.Provider)
	// Workspace affinity: edit work continues where its worktree lives unless
	// a verified checkpoint of its published revision exists.
	affinity := ""
	if r.Mode == protocol.ModeEdit && r.PreviousRunID != "" {
		if prev, err := store.GetRun(ctx, h.st.R(), r.PreviousRunID); err == nil && prev.NodeID != "" {
			affinity = prev.NodeID
			if j.Revision != nil && j.Revision.Head != "" && h.bundleFor(ctx, h.st.R(), j.Revision.Head) != nil {
				// portable, but still prefer the same machine
			} else if prevNode, err := store.GetNode(ctx, h.st.R(), prev.NodeID); err == nil && prevNode.RevokedAt != nil {
				return nil, "The machine holding this work's workspace was revoked and no verified checkpoint exists. Retry explicitly to start a fresh attempt.", true
			}
		}
	}
	portable := affinity == "" || (j.Revision != nil && j.Revision.Head != "" && h.bundleFor(ctx, h.st.R(), j.Revision.Head) != nil)

	var reasons []string
	anyPossible := false
	var best *placement
	bestScore := -1
	for _, n := range nodes {
		if n.RevokedAt != nil {
			continue
		}
		if !portable && n.ID != affinity {
			continue
		}
		var inst *protocol.ProviderInstallation
		for i := range n.Providers {
			if n.Providers[i].Provider == r.Provider {
				inst = &n.Providers[i]
			}
		}
		if inst == nil || inst.AuthState == protocol.AuthNotInstalled {
			reasons = append(reasons, provider+" isn't installed on "+n.Name+".")
			continue
		}
		if inst.AuthState != protocol.AuthReady {
			reasons = append(reasons, provider+" needs sign-in on "+n.Name+".")
			continue
		}
		if r.Mode == protocol.ModeReadOnly && !inst.Capabilities.ReadOnly {
			reasons = append(reasons, provider+" on "+n.Name+" can't enforce a read-only review.")
			continue
		}
		profileOK := false
		for _, p := range n.Profiles {
			if p.Name == r.ExecutionProfile && p.Available {
				profileOK = true
			}
		}
		if !profileOK {
			reasons = append(reasons, n.Name+" has no available "+r.ExecutionProfile+" execution profile.")
			continue
		}
		anyPossible = true
		if !h.Connected(n.ID) {
			reasons = append(reasons, n.Name+" is not connected.")
			continue
		}
		if n.Draining {
			reasons = append(reasons, n.Name+" is draining.")
			continue
		}
		if n.Capacity.DiskPressure {
			reasons = append(reasons, n.Name+" is low on disk space.")
			continue
		}
		h.mu.Lock()
		until, excluded := h.excluded[r.ID+"|"+n.ID]
		h.mu.Unlock()
		if excluded && h.now().Before(until) {
			reasons = append(reasons, n.Name+" declined this run recently.")
			continue
		}
		slots := n.Capacity.Slots
		if slots <= 0 {
			slots = 1
		}
		if counts.node[n.ID] >= slots {
			reasons = append(reasons, n.Name+" is busy.")
			continue
		}
		if counts.profile[inst.ProfileID] >= h.profileMax(ctx, inst.ProfileID) {
			reasons = append(reasons, "The "+provider+" account ("+firstNonEmpty(inst.Account, inst.ProfileID)+") is at its concurrency limit.")
			continue
		}
		score := 100 - counts.node[n.ID]*10
		if n.ID == affinity {
			score += 1000
		}
		if score > bestScore {
			bestScore = score
			best = &placement{node: n, inst: *inst}
		}
	}
	if best != nil {
		return best, "", false
	}
	if len(nodes) == 0 {
		return nil, "No machines are paired yet. Add a machine to run work.", true
	}
	why := "No machine can take this yet."
	if len(reasons) > 0 {
		why = dedupeJoin(reasons)
	}
	return nil, why, !anyPossible
}

func dedupeJoin(ss []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > 3 {
		out = append(out[:3], fmt.Sprintf("(+%d more)", len(out)-3))
	}
	s := ""
	for i, o := range out {
		if i > 0 {
			s += " "
		}
		s += o
	}
	return s
}

func (h *Hub) noteQueued(ctx context.Context, j store.JobRow, detail string) {
	if j.StateDetail == detail && (j.State == protocol.JobQueued || j.State == protocol.JobRunning) {
		return
	}
	_ = h.do(ctx, func(t *txn) error {
		cur, err := store.GetJob(ctx, t.tx, j.ID)
		if err != nil {
			return err
		}
		switch cur.State {
		case protocol.JobWaiting:
			if cur.WaitingReason == protocol.WaitMachine || cur.WaitingReason == protocol.WaitProviderSignIn {
				_, err = h.setJobState(ctx, t, j.ID, protocol.JobQueued, "", detail)
			}
			return err
		case protocol.JobQueued, protocol.JobRunning, protocol.JobReviewReady:
			if _, err := store.SetJobState(ctx, t.tx, j.ID, cur.Version, cur.State, cur.WaitingReason, detail); err != nil {
				return err
			}
			_, err = t.jobChanged(ctx, j.ID)
			return err
		}
		return nil
	})
}

func (h *Hub) noteWaiting(ctx context.Context, j store.JobRow, detail string) {
	reason := protocol.WaitMachine
	if len(detail) > 0 && containsStr(detail, "needs sign-in") {
		reason = protocol.WaitProviderSignIn
	}
	if j.State == protocol.JobWaiting && j.WaitingReason == reason && j.StateDetail == detail {
		return
	}
	_ = h.do(ctx, func(t *txn) error {
		cur, err := store.GetJob(ctx, t.tx, j.ID)
		if err != nil || !(cur.State == protocol.JobQueued || cur.State == protocol.JobWaiting) {
			return err
		}
		_, err = h.setJobState(ctx, t, j.ID, protocol.JobWaiting, reason, detail)
		return err
	})
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

// offer assigns the run to a node under a fresh lease epoch and queues the
// offer command in the same transaction.
func (h *Hub) offer(ctx context.Context, r store.RunRow, j store.JobRow, p placement) error {
	return h.do(ctx, func(t *txn) error {
		cur, err := store.GetRun(ctx, t.tx, r.ID)
		if err != nil || cur.State != protocol.RunCreated {
			return err
		}
		// Access may have changed since the run was queued: never build or
		// send context for an engineer who no longer belongs here.
		if aerr := h.checkRunAccess(ctx, t.tx, cur); aerr != nil {
			reason := domain.AsError(aerr).Message
			if err := store.SetRunState(ctx, t.tx, cur.ID, protocol.RunCancelled, reason); err != nil {
				return err
			}
			if err := h.runChanged(ctx, t, cur.ID); err != nil {
				return err
			}
			if job, err := store.GetJob(ctx, t.tx, cur.JobID); err == nil && domain.JobLive(job.State) {
				name := h.engineerName(ctx, t.tx, cur.EngineerID)
				if _, err := h.setJobState(ctx, t, job.ID, protocol.JobFailed, "", name+" no longer has access: "+reason); err != nil {
					return err
				}
			}
			return nil
		}
		cur.ProfileID = p.inst.ProfileID
		if _, err := t.tx.ExecContext(ctx, `UPDATE runs SET profile_id = ? WHERE id = ?`, cur.ProfileID, cur.ID); err != nil {
			return err
		}
		job, err := store.GetJob(ctx, t.tx, j.ID)
		if err != nil {
			return err
		}
		x, m, err := h.buildManifest(ctx, t.tx, cur, job, p.node.ID)
		if err != nil {
			return err
		}
		epoch := cur.LeaseEpoch + 1
		record, _ := json.Marshal(m)
		commandID := fmt.Sprintf("offer:%s", cur.ID)
		if err := store.OfferRun(ctx, t.tx, cur.ID, p.node.ID, epoch, h.now().Add(h.lim.OfferAckTimeout), commandID+fmt.Sprintf(":%d", epoch), string(record)); err != nil {
			return err
		}
		if _, err := t.tx.ExecContext(ctx, `UPDATE runs SET scope_fingerprint = ?, model = ? WHERE id = ?`, m.ScopeFingerprint, x.Model, cur.ID); err != nil {
			return err
		}
		// Inputs in the manifest are bound to this attempt but consumed only
		// when it starts: a rejected or expired offer leaves them for the next.
		for _, in := range m.Inputs {
			if in.Kind == "owner_input" {
				if err := store.SetInputDelivery(ctx, t.tx, in.ID, cur.ID, "queued"); err != nil {
					return err
				}
			}
		}
		if err := h.queueCommand(ctx, t, p.node.ID, cur.ID, epoch, protocol.CmdOfferRun, commandID, protocol.OfferRun{
			Manifest: x, LeaseMs: h.lim.LeaseDuration.Milliseconds(), StopMarginMs: h.lim.StopMargin.Milliseconds(),
			DeadlineAt: h.now().Add(h.lim.OfferAckTimeout)}); err != nil {
			return err
		}
		if job.State == protocol.JobWaiting {
			if _, err := h.setJobState(ctx, t, job.ID, protocol.JobQueued, "", ""); err != nil {
				return err
			}
		}
		detail := "Starting on " + p.node.Name
		if _, err := store.SetJobState(ctx, t.tx, job.ID, mustVersion(ctx, t, job.ID), stateOf(ctx, t, job.ID), "", detail); err != nil {
			return err
		}
		if err := store.SetJobActivity(ctx, t.tx, job.ID, detail, h.now()); err != nil {
			return err
		}
		if _, err := t.jobChanged(ctx, job.ID); err != nil {
			return err
		}
		return h.runChanged(ctx, t, cur.ID)
	})
}

func mustVersion(ctx context.Context, t *txn, jobID string) int64 {
	j, _ := store.GetJob(ctx, t.tx, jobID)
	return j.Version
}

func stateOf(ctx context.Context, t *txn, jobID string) protocol.JobState {
	j, _ := store.GetJob(ctx, t.tx, jobID)
	return j.State
}

// retryDue resumes work that waited for a provider allowance.
func (h *Hub) retryDue(ctx context.Context) {
	jobs, err := store.ListJobs(ctx, h.st.R(), store.JobFilter{States: []protocol.JobState{protocol.JobWaiting}, IncludeReply: true})
	if err != nil {
		return
	}
	now := h.now()
	for _, j := range jobs {
		if j.WaitingReason != protocol.WaitProviderLimit || j.RetryAt == nil || now.Before(*j.RetryAt) {
			continue
		}
		_ = h.do(ctx, func(t *txn) error {
			if err := store.SetJobRetry(ctx, t.tx, j.ID, nil, j.AutoRetries); err != nil {
				return err
			}
			cur, err := store.GetJob(ctx, t.tx, j.ID)
			if err != nil {
				return err
			}
			_, err = h.enqueueRun(ctx, t, cur, runReason{Purpose: "continue", Cause: j.ID, Note: "Resuming after the provider allowance wait."})
			return err
		})
	}
}

// nodeHealth marks nodes suspect after missed heartbeats.
func (h *Hub) nodeHealth(ctx context.Context) {
	nodes, err := store.ListNodes(ctx, h.st.R())
	if err != nil {
		return
	}
	suspectAfter := time.Duration(h.lim.SuspectAfterMisses) * h.lim.HeartbeatInterval
	for _, n := range nodes {
		if n.Status != protocol.NodeOnline || n.LastSeenAt == nil {
			continue
		}
		if h.now().Sub(*n.LastSeenAt) > suspectAfter {
			_ = h.do(ctx, func(t *txn) error {
				if err := store.SetNodeStatus(ctx, t.tx, n.ID, protocol.NodeSuspect); err != nil {
					return err
				}
				return h.emitNode(ctx, t, n.ID)
			})
		}
	}
}
