package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
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
	h.expireEngineerDrafts(ctx)
	h.expireApprovals(ctx)
	h.retryDue(ctx)
	h.nodeHealth(ctx)
	h.schedule(ctx)
	now := h.now()
	h.mu.Lock()
	pollPRs := now.Sub(h.lastPRPoll) > time.Minute
	if pollPRs {
		h.lastPRPoll = now
	}
	h.mu.Unlock()
	if pollPRs {
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
	return h.countsWith(ctx, h.st.R())
}

func (h *Hub) countsWith(ctx context.Context, q store.Q) (schedCounts, error) {
	c := schedCounts{engineer: map[string]int{}, profile: map[string]int{}, node: map[string]int{}}
	runs, err := store.RunsInStates(ctx, q, protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
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
	drafts, err := store.ActiveEngineerDrafts(ctx, q)
	if err != nil {
		return c, err
	}
	for _, d := range drafts {
		c.profile[d.Provider.ProfileID]++
		c.node[d.NodeID]++
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

// profilePausedUntil is when a provider account whose allowance ran out may
// be used again (zero when it isn't paused).
func (h *Hub) profilePausedUntil(ctx context.Context, profileID string) time.Time {
	var v sql.NullString
	if err := h.st.R().QueryRowContext(ctx, `SELECT paused_until FROM provider_profiles WHERE id = ?`, profileID).Scan(&v); err != nil || !v.Valid {
		return time.Time{}
	}
	return store.ParseTS(v.String)
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
	// The engineer's current preference applies to queued work too.
	allowAPI, pin, name := false, r.ProfileID, "This engineer"
	if eng, err := store.GetEngineer(ctx, h.st.R(), r.EngineerID); err == nil {
		allowAPI, name = eng.Provider.AllowAPIBilling, eng.Name
		if eng.Provider.ProfileID != "" {
			pin = eng.Provider.ProfileID
		}
	}
	if _, ok := knownProviders[r.Provider]; !ok {
		return nil, fmt.Sprintf("%s is set to a provider this hub doesn't support (%q). Choose Codex, Claude Code, or Cursor in their profile.", name, r.Provider), true
	}
	// Workspace affinity: edit work continues where its worktree lives unless
	// a verified checkpoint of its published revision exists (it is then
	// portable, but still prefers the same machine).
	affinity, portable := "", true
	if r.Mode == protocol.ModeEdit && r.PreviousRunID != "" {
		if prev, err := store.GetRun(ctx, h.st.R(), r.PreviousRunID); err == nil && prev.NodeID != "" {
			affinity = prev.NodeID
			portable = j.Revision != nil && j.Revision.Head != "" && h.bundleFor(ctx, h.st.R(), j.Revision.Head) != nil
			if !portable {
				if prevNode, err := store.GetNode(ctx, h.st.R(), prev.NodeID); err == nil && prevNode.RevokedAt != nil {
					return nil, "The machine holding this work's workspace was revoked and no verified checkpoint exists. Retry explicitly to start a fresh attempt.", true
				}
			}
		}
	}

	var requires []string
	if j.ProjectID != "" {
		if p, err := store.GetProject(ctx, h.st.R(), j.ProjectID); err == nil {
			requires = p.Policy.Requires
		}
	}

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
		// An engineer pinned to one account never runs on another (A23).
		if pin != "" && inst.ProfileID != pin {
			reasons = append(reasons, provider+" on "+n.Name+" is signed in to a different account ("+firstNonEmpty(inst.Account, inst.ProfileID)+") than this engineer uses.")
			continue
		}
		// No silent fallback to paid API usage (brief §7).
		if inst.Billing == protocol.BillingAPI && !allowAPI {
			reasons = append(reasons, provider+" on "+n.Name+" is billed to an API key; allow API billing for this engineer to use it.")
			continue
		}
		if (r.Mode == protocol.ModeReadOnly || r.Mode == protocol.ModeConversation) && !inst.Capabilities.ReadOnly {
			purpose := "review"
			if r.Mode == protocol.ModeConversation {
				purpose = "conversation"
			}
			reasons = append(reasons, provider+" on "+n.Name+" can't enforce a read-only "+purpose+".")
			continue
		}
		profileOK := slices.ContainsFunc(n.Profiles, func(p protocol.ExecutionProfile) bool {
			return p.Name == r.ExecutionProfile && p.Available
		})
		if !profileOK {
			reasons = append(reasons, n.Name+" has no available "+r.ExecutionProfile+" execution profile.")
			continue
		}
		if missing := missingRequirements(n.Node, requires); missing != "" {
			reasons = append(reasons, "This project needs "+missing+", which "+n.Name+" doesn't have.")
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
		if until := h.profilePausedUntil(ctx, inst.ProfileID); h.now().Before(until) {
			reasons = append(reasons, "The "+provider+" account ("+firstNonEmpty(inst.Account, inst.ProfileID)+") ran out of allowance; it's paused until "+until.Local().Format("15:04")+".")
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

// missingRequirements names what a machine lacks from a project's needs:
// reported toolchains, or os:<goos>.
func missingRequirements(n protocol.Node, requires []string) string {
	var miss []string
	for _, req := range requires {
		req = strings.ToLower(strings.TrimSpace(req))
		if req == "" {
			continue
		}
		if osName, ok := strings.CutPrefix(req, "os:"); ok {
			if !strings.EqualFold(n.OS, osName) {
				miss = append(miss, osLabel(osName))
			}
			continue
		}
		if _, ok := n.Toolchains[req]; !ok {
			miss = append(miss, req)
		}
	}
	return strings.Join(miss, " and ")
}

func osLabel(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	}
	return goos
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
	return strings.Join(out, " ")
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
	if strings.Contains(detail, "needs sign-in") {
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

// offer assigns the run to a node under a fresh lease epoch and queues the
// offer command in the same transaction.
func (h *Hub) offer(ctx context.Context, r store.RunRow, j store.JobRow, p placement) error {
	return h.do(ctx, func(t *txn) error {
		cur, err := store.GetRun(ctx, t.tx, r.ID)
		if err != nil || cur.State != protocol.RunCreated {
			return err
		}
		counts, err := h.countsWith(ctx, t.tx)
		if err != nil {
			return err
		}
		if counts.org >= h.lim.ActiveRunsPerOrg || counts.node[p.node.ID] >= max(1, p.node.Capacity.Slots) || counts.profile[p.inst.ProfileID] >= h.profileMax(ctx, p.inst.ProfileID) {
			return nil
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
		latest, _ := store.GetJob(ctx, t.tx, job.ID)
		if _, err := store.SetJobState(ctx, t.tx, job.ID, latest.Version, latest.State, "", detail); err != nil {
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

// retryDue resumes work that waited for a provider allowance, or for an
// automatic retry after a provider crash.
func (h *Hub) retryDue(ctx context.Context) {
	jobs, err := store.ListJobs(ctx, h.st.R(), store.JobFilter{States: []protocol.JobState{protocol.JobWaiting}, IncludeReply: true})
	if err != nil {
		return
	}
	now := h.now()
	for _, j := range jobs {
		if (j.WaitingReason != protocol.WaitProviderLimit && j.WaitingReason != protocol.WaitRecovery) || j.RetryAt == nil || now.Before(*j.RetryAt) {
			continue
		}
		purpose, note := "continue", "Resuming after the provider allowance wait."
		if j.WaitingReason == protocol.WaitRecovery {
			purpose, note = "retry", "Retrying automatically: the previous attempt stopped unexpectedly. Your workspace is as it was left."
		}
		_ = h.do(ctx, func(t *txn) error {
			cur, err := store.GetJob(ctx, t.tx, j.ID)
			if err != nil {
				return err
			}
			if cur.State != protocol.JobWaiting || cur.WaitingReason != j.WaitingReason || cur.RetryAt == nil || now.Before(*cur.RetryAt) {
				return nil // someone acted meanwhile (an explicit retry or stop)
			}
			if err := store.SetJobRetry(ctx, t.tx, j.ID, nil, cur.AutoRetries); err != nil {
				return err
			}
			cur.RetryAt = nil
			_, err = h.enqueueRun(ctx, t, cur, runReason{Purpose: purpose, Cause: j.ID, Note: note})
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
