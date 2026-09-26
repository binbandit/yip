<script lang="ts">
  // Everything needed to inspect one piece of work: state and blockers in
  // plain words, evidence (diff, checks, files), review history, activity and
  // tool logs, and each attempt. Stop, Retry and Accept are explicit.
  import { untrack } from 'svelte';
  import { app, receiptKey } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { api, fetchArtifactText } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import type { Artifact, RunActivity } from '../../lib/api/types.gen';
  import { parseUnifiedDiff, type DiffFile } from '../../lib/util/diff';
  import { isLiveJob } from '../../lib/state/data';
  import {
    billingLabel,
    deliveryReceipt,
    jobShape,
    jobStateLabel,
    jobTone,
    providerLabel,
    runShape,
    runStateLabel,
    runTone,
    waitingReasonLabel,
    workId,
  } from '../../lib/util/labels';
  import { atTime, bytes, clock, duration, fullTime, relative, shortSha } from '../../lib/util/time';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import StateIcon from '../StateIcon.svelte';
  import Avatar from '../Avatar.svelte';
  import Icon from '../Icon.svelte';
  import DiffView from '../DiffView.svelte';
  import ReviewDetail from '../ReviewDetail.svelte';
  import PRFacts from '../PRFacts.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import MessageBody from '../MessageBody.svelte';

  interface Props {
    jobId: string;
    mode: PanelMode;
  }
  let { jobId, mode }: Props = $props();

  $effect(() => {
    const t = app.data.touched.jobs[jobId] ?? 0;
    untrack(() => details.ensureJob(jobId, t));
  });

  const entry = $derived(details.jobs[jobId]);
  const d = $derived(entry?.data);
  const job = $derived(app.data.jobs[jobId] ?? d?.job);
  const runs = $derived(d ? [...d.runs].sort((a, b) => b.attempt - a.attempt) : []);
  const unknownRun = $derived(runs.find((r) => r.state === 'unknown'));
  // A stop is only confirmed once the machine reports the attempt ended.
  const stoppingRun = $derived(runs.find((r) => r.state === 'stopping'));
  const project = $derived(job?.projectId ? app.data.projects[job.projectId] : undefined);
  const repo = $derived(project?.repos.find((r) => r.id === job?.repoId));
  // Waits a person fixes outside the work itself get a link to where to fix them.
  const setupLink = $derived.by(() => {
    if (job?.state !== 'waiting') return null;
    switch (job.waitingReason) {
      case 'provider_sign_in':
      case 'provider_allowance':
      case 'machine_availability':
        return { href: '/machines', label: 'View machines and sign-ins' };
      case 'engineer_capacity':
        return { href: `/engineers/${job.ownerId}`, label: `View ${app.engineerName(job.ownerId)}'s setup` };
    }
    return null;
  });
  const room = $derived(job ? app.data.rooms[job.source.roomId] : undefined);
  const live = $derived(!!job && isLiveJob(job));
  const canRetry = $derived(
    !!job &&
      !stoppingRun &&
      (job.state === 'failed' || job.state === 'cancelled' || (job.state === 'waiting' && (job.waitingReason === 'recovery' || job.waitingReason === 'stalled')) || !!unknownRun),
  );
  const canAccept = $derived(!!job && job.requiresHumanReview && job.state === 'review_ready' && !!job.revision?.head);

  const TABS = ['evidence', 'review', 'activity', 'runs'] as const;
  type Tab = (typeof TABS)[number];
  const tab: Tab = $derived((TABS as readonly string[]).includes(app.loc.tab ?? '') ? (app.loc.tab as Tab) : 'evidence');

  // ---- evidence: diff per revision ----
  // Revision records (file and line counts) drive the picker and stats; the
  // diff artifact is still parsed to render hunks.
  interface RevOption {
    head: string;
    artifactId: string;
    at: string;
    stats?: { files: number; ins: number; del: number; summary: string; branch: string };
  }
  const revOptions: RevOption[] = $derived.by(() => {
    if (!d) return [];
    const byId = new Map(d.artifacts.map((a) => [a.id, a]));
    if (d.revisions.length) {
      return [...d.revisions].reverse().map((r) => ({
        head: r.head,
        artifactId: r.diffArtifactId,
        at: byId.get(r.diffArtifactId)?.createdAt ?? '',
        stats: { files: r.filesChanged, ins: r.insertions, del: r.deletions, summary: r.summary, branch: r.branch },
      }));
    }
    return d.artifacts
      .filter((a) => a.kind === 'diff')
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      .map((a) => ({ head: a.revision ?? '', artifactId: a.id, at: a.createdAt }));
  });
  let chosenRev = $state<string | null>(null);
  // A finding's location opens the diff of the revision that was reviewed.
  $effect(() => {
    const f = app.diffFocus;
    if (!f || f.jobId !== jobId || !f.head) return;
    const o = revOptions.find((x) => x.head === f.head);
    if (o) untrack(() => (chosenRev = o.artifactId));
  });
  const rev = $derived(revOptions.find((o) => o.artifactId === chosenRev) ?? revOptions.find((o) => o.head === job?.revision?.head) ?? revOptions[0]);
  const diff = $derived(rev && d ? (d.artifacts.find((a) => a.id === rev.artifactId) ?? { id: rev.artifactId, revision: rev.head }) : undefined);
  let diffFiles = $state<DiffFile[] | null>(null);
  let diffError = $state('');
  let diffTruncated = $state(false);
  let loadedDiffId = '';
  $effect(() => {
    const a = diff;
    if (!a || tab !== 'evidence' || a.id === loadedDiffId) return;
    untrack(() => {
      loadedDiffId = a.id;
      diffFiles = null;
      diffError = '';
      fetchArtifactText(a.id)
        .then(({ text, truncated }) => {
          diffFiles = parseUnifiedDiff(text);
          diffTruncated = truncated;
        })
        .catch((e) => (diffError = e.message));
    });
  });

  // ---- check logs ----
  let openLog = $state<string | null>(null);
  let logText = $state<Record<string, string>>({});
  async function toggleLog(artifactId: string) {
    if (openLog === artifactId) {
      openLog = null;
      return;
    }
    openLog = artifactId;
    if (!logText[artifactId]) {
      try {
        logText[artifactId] = (await fetchArtifactText(artifactId, 200_000)).text || '(empty log)';
      } catch (e) {
        logText[artifactId] = (e as Error).message;
      }
    }
  }

  // ---- run tool logs ----
  let runLogs = $state<Record<string, RunActivity[] | string>>({});
  async function loadRunLog(runId: string) {
    if (runLogs[runId]) return;
    runLogs[runId] = 'loading';
    try {
      runLogs[runId] = await api.runActivity(jobId, runId);
    } catch (e) {
      runLogs[runId] = errorMessage(e);
    }
  }

  // ---- actions ----
  let confirmStop = $state(false);
  let busy = $state('');
  let actionError = $state('');

  async function stop() {
    const j = await api.cancelJob(jobId, { reason: 'Stopped by the owner', includeChildren: true });
    app.data.jobs[j.id] = j;
    app.announce(`${j.title} stopped.`);
    void details.refreshJob(jobId);
  }

  async function retry() {
    busy = 'retry';
    actionError = '';
    try {
      const j = await api.retryJob(jobId, { fromCheckpoint: true, reason: 'Retried by the owner' });
      app.data.jobs[j.id] = j;
      app.announce('A new attempt is queued.');
      void details.refreshJob(jobId);
    } catch (err) {
      actionError = errorMessage(err);
    } finally {
      busy = '';
    }
  }

  async function accept() {
    if (!job?.revision?.head) return;
    busy = 'accept';
    actionError = '';
    try {
      const j = await api.acceptJob(jobId, { revision: job.revision.head, version: job.version, note: '' });
      app.data.jobs[j.id] = j;
      app.announce(`Accepted revision ${shortSha(job.revision.head)}.`);
      void details.refreshJob(jobId);
    } catch (err) {
      if (err instanceof ApiError && err.conflict) {
        actionError = 'This work changed since you opened it. Review the latest revision before accepting.';
        void details.refreshJob(jobId);
      } else actionError = errorMessage(err);
    } finally {
      busy = '';
    }
  }

  function steer() {
    if (!job) return;
    const key = receiptKey(job.source.roomId, job.source.threadId);
    app.steer[key] = job.id;
    const target = job.source.threadId
      ? `/rooms/${job.source.roomId}?panel=thread%3A${job.source.threadId}`
      : `/rooms/${job.source.roomId}`;
    app.navigate(target);
    queueMicrotask(() => document.querySelector<HTMLTextAreaElement>('.room-composer textarea, .panel textarea')?.focus());
  }

  function tabKey(e: KeyboardEvent) {
    const i = TABS.indexOf(tab);
    let n = i;
    if (e.key === 'ArrowRight') n = (i + 1) % TABS.length;
    else if (e.key === 'ArrowLeft') n = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === 'Home') n = 0;
    else if (e.key === 'End') n = TABS.length - 1;
    else return;
    e.preventDefault();
    app.setTab(TABS[n]);
    queueMicrotask(() => document.getElementById(`jobtab-${TABS[n]}`)?.focus());
  }

  const tabLabel: Record<Tab, string> = { evidence: 'Evidence', review: 'Review', activity: 'Activity', runs: 'Runs' };
  const counts = $derived({
    evidence: d ? d.checks.length + d.artifacts.length : 0,
    review: d ? d.reviews.length : 0,
    activity: d ? d.activity.length : 0,
    runs: d ? d.runs.length : 0,
  });
  const artifactsByKind = (list: Artifact[]) => list.filter((a) => a.kind !== 'diff');
</script>

<RightPanel title={job?.title ?? 'Work'} {mode} wide onclose={() => app.closePanel()}>
  {#snippet subtitle()}
    {#if job}{jobStateLabel(job)} · {app.engineerName(job.ownerId)}{#if project}{' · '}{project.name}{/if}{/if}
  {/snippet}

  {#if !job}
    <div class="pad">
      {#if entry?.error}
        <p class="notice danger" role="alert">{entry.missing ? "This work doesn't exist or isn't visible to you." : entry.error}</p>
      {:else}
        <p class="meta">Loading the work…</p>
      {/if}
    </div>
  {:else}
    <div class="pad head">
      <p class="state tone-{jobTone(job.state)}">
        <StateIcon shape={jobShape(job.state)} tone={jobTone(job.state)} size={16} live={job.state === 'running'} />
        <strong>{job.state === 'cancelled' && stoppingRun ? 'Stopping' : jobStateLabel(job)}</strong>
        {#if job.state === 'waiting'}<span class="muted">· {waitingReasonLabel(job.waitingReason)}</span>{/if}
      </p>
      {#if stoppingRun}
        <p class="notice attention">Waiting for {app.nodeName(stoppingRun.nodeId) || 'its machine'} to confirm the attempt has stopped.</p>
      {/if}

      {#if (job.state === 'waiting' || job.state === 'failed' || job.state === 'review_ready') && job.stateDetail}
        <p class="notice {job.state === 'failed' ? 'danger' : 'attention'}">
          {job.stateDetail}{#if setupLink}{' '}<a href={setupLink.href}>{setupLink.label}</a>{/if}
        </p>
      {:else if setupLink}
        <p class="notice attention">{waitingReasonLabel(job.waitingReason)}. <a href={setupLink.href}>{setupLink.label}</a></p>
      {/if}
      {#if unknownRun}
        <p class="notice attention">
          Last heard from {app.nodeName(unknownRun.nodeId) || 'its machine'}
          {atTime(unknownRun.heartbeatAt ?? unknownRun.lastActivityAt ?? unknownRun.createdAt)}. The run's outcome is not yet confirmed{unknownRun.lastActivity
            ? ` — last confirmed: ${unknownRun.lastActivity.toLowerCase()}`
            : ''}. Check before retrying anything that pushes or publishes.
        </p>
      {/if}
      {#if d?.missing.length}
        <div class="notice attention">
          <Icon name="alert" size={16} />
          <div>{#each d.missing as m (m)}<p>{m}</p>{/each}</div>
        </div>
      {/if}

      <dl class="props">
        <div>
          <dt>Owner</dt>
          <dd>
            <button class="person" onclick={() => app.openPanel({ kind: 'engineer', id: job!.ownerId })}>
              <Avatar actor={{ kind: 'engineer', id: job.ownerId }} size={20} />{app.engineerName(job.ownerId)}
            </button>
          </dd>
        </div>
        {#if job.reviewerIds.length}
          <div>
            <dt>Reviewers</dt>
            <dd class="people">
              {#each job.reviewerIds as r (r)}<span class="person-static"><Avatar actor={{ kind: 'engineer', id: r }} size={20} />{app.engineerName(r)}</span>{/each}
            </dd>
          </div>
        {/if}
        {#if project}
          <div>
            <dt>Project</dt>
            <dd>
              <a href="/projects/{project.id}">{project.name}</a>{#if repo}{' '}<span class="meta"
                  >· <span class="mono">{repo.forgeRepo || repo.name}</span></span
                >{/if}
            </dd>
          </div>
        {/if}
        <div>
          <dt>Machine</dt>
          <dd>{job.nodeId ? app.nodeName(job.nodeId) || 'A paired machine' : 'Not assigned yet'}</dd>
        </div>
        {#if job.revision?.head || job.revision?.branch}
          <div>
            <dt>Revision</dt>
            <dd><span class="mono">{shortSha(job.revision.head) || '—'}</span>{#if job.revision.branch}{' '}<span class="meta">on {job.revision.branch}</span>{/if}</dd>
          </div>
        {/if}
        <div>
          <dt>Last confirmed</dt>
          <dd>
            {job.lastActivity || 'Nothing yet'}{#if job.lastActivityAt}{' '}<span class="meta" title={fullTime(job.lastActivityAt)}>· {relative(job.lastActivityAt, app.now)}</span>{/if}
          </dd>
        </div>
        {#if job.followsId}
          <div>
            <dt>Follows up</dt>
            <dd>
              <button class="link-btn" onclick={() => app.openPanel({ kind: 'job', id: job!.followsId! })}
                >{app.data.jobs[job.followsId]?.title ?? 'the earlier work'}</button
              >
            </dd>
          </div>
        {/if}
        <div>
          <dt>Work ID</dt>
          <dd><span class="mono" title={job.id}>#{workId(job.id)}</span></dd>
        </div>
        <div>
          <dt>From</dt>
          <dd>
            <a href="/rooms/{job.source.roomId}?msg={job.source.messageId ?? ''}{job.source.threadId ? `&panel=thread%3A${job.source.threadId}` : ''}"
              >{room?.name ?? 'the source conversation'}</a
            >
            {#if job.requiresHumanReview}{' '}<span class="meta">· needs your review before it completes</span>{/if}
          </dd>
        </div>
      </dl>

      {#if actionError}<p class="form-error" role="alert">{actionError}</p>{/if}
      <div class="actions">
        {#if canAccept}
          <button class="btn btn-primary btn-sm" disabled={busy === 'accept'} onclick={accept}>
            <Icon name="check" size={15} />Accept revision {shortSha(job.revision?.head)}
          </button>
        {/if}
        {#if live}
          <button class="btn btn-sm" onclick={steer}><Icon name="reply" size={15} />Add to this work</button>
          <button class="btn btn-sm btn-danger" onclick={() => (confirmStop = true)}><Icon name="stop" size={15} />Stop</button>
        {/if}
        {#if canRetry}
          <button class="btn btn-sm" disabled={busy === 'retry'} onclick={retry}><Icon name="refresh" size={15} />{busy === 'retry' ? (job.state === 'cancelled' ? 'Resuming…' : 'Retrying…') : job.state === 'cancelled' ? 'Resume' : 'Retry'}</button>
        {/if}
      </div>
    </div>

    <div class="tabs" role="tablist" aria-label="Work details">
      {#each TABS as t (t)}
        <button
          class="tab"
          role="tab"
          id="jobtab-{t}"
          aria-selected={tab === t}
          aria-controls="jobpanel-{t}"
          tabindex={tab === t ? 0 : -1}
          onclick={() => app.setTab(t)}
          onkeydown={tabKey}
        >
          {tabLabel[t]}{#if counts[t]}<span class="n">{counts[t]}</span>{/if}
        </button>
      {/each}
    </div>

    <div class="pad tabpanel" role="tabpanel" id="jobpanel-{tab}" aria-labelledby="jobtab-{tab}" tabindex="-1">
      {#if !d}
        <p class="meta">Loading evidence…</p>
      {:else if tab === 'evidence'}
        {#if job.summary}
          <section class="block">
            <h3>Summary</h3>
            <MessageBody message={{ body: job.summary, mentions: [] }} />
          </section>
        {/if}
        {#if job.acceptance.length}
          <section class="block">
            <h3>Done when</h3>
            <ul class="plain">{#each job.acceptance as a (a)}<li>{a}</li>{/each}</ul>
          </section>
        {/if}

        <section class="block">
          <div class="block-head">
            <h3>Changes</h3>
            {#if revOptions.length > 1}
              <label class="rev-pick">
                <span class="vh">Revision</span>
                <select class="select" value={rev?.artifactId} onchange={(e) => (chosenRev = (e.target as HTMLSelectElement).value)}>
                  {#each revOptions as o (o.artifactId)}
                    <option value={o.artifactId}
                      >{shortSha(o.head)}{o.head === job.revision?.head ? ' (current)' : ''}{o.stats ? ` · ${o.stats.files} files +${o.stats.ins} −${o.stats.del}` : o.at ? ` · ${clock(o.at)}` : ''}</option
                    >
                  {/each}
                </select>
              </label>
            {/if}
          </div>
          {#if rev?.stats}
            <p class="rev-stats">
              <span class="mono">{shortSha(rev.head)}</span>
              · {rev.stats.files} {rev.stats.files === 1 ? 'file' : 'files'} <span class="add">+{rev.stats.ins}</span> <span class="del">−{rev.stats.del}</span>
              {#if rev.stats.summary}· {rev.stats.summary}{/if}
            </p>
          {/if}
          {#if !diff}
            <p class="meta">No diff recorded{job.kind === 'code' ? ' yet' : ''}.</p>
          {:else if diffError}
            <p class="notice danger">{diffError}</p>
          {:else if !diffFiles}
            <p class="meta">Loading the diff…</p>
          {:else}
            {#if diff.revision !== job.revision?.head && job.revision?.head}
              <p class="notice attention">This diff is for an earlier revision ({shortSha(diff.revision)}).</p>
            {/if}
            <DiffView files={diffFiles} focus={app.diffFocus?.jobId === jobId ? app.diffFocus : null} />
            {#if diffTruncated}<p class="meta">The diff is long; <a href="/v1/artifacts/{diff.id}" target="_blank" rel="noopener">open the full file</a>.</p>{/if}
          {/if}
        </section>

        <section class="block">
          <h3>Checks</h3>
          {#if d.checks.length === 0}
            <p class="meta">No checks recorded.{job.kind === 'code' ? ' A result without checks is unverified.' : ''}</p>
          {:else}
            <ul class="checks">
              {#each [...d.checks].reverse() as c (c.id)}
                <li class="check-row" class:stale={!!job.revision?.head && c.revision !== job.revision.head}>
                  <div class="check-main">
                    <StateIcon shape={c.passed ? 'check-filled' : 'triangle'} tone={c.passed ? 'success' : 'danger'} />
                    <div class="check-text">
                      <p><code class="mono">{c.command}</code></p>
                      <p class="meta">
                        <span class={c.passed ? 'tone-success' : 'tone-danger'}>{c.passed ? 'Passed' : 'Failed'}</span> · exit {c.exitCode} · on
                        <span class="mono">{shortSha(c.revision)}</span>{c.revision !== job.revision?.head && job.revision?.head ? ' (earlier revision)' : ''}
                        · {duration(c.durationMs)} · {app.nodeName(c.nodeId) || 'machine'} · {atTime(c.createdAt)}
                      </p>
                      {#if c.summary}<pre class="summary-out">{c.summary}</pre>{/if}
                    </div>
                  </div>
                  {#if c.logArtifactId}
                    <button class="btn btn-sm btn-quiet" aria-expanded={openLog === c.logArtifactId} onclick={() => toggleLog(c.logArtifactId!)}>
                      {openLog === c.logArtifactId ? 'Hide log' : 'Show log'}
                    </button>
                  {/if}
                  {#if openLog === c.logArtifactId && c.logArtifactId}
                    <pre class="log">{logText[c.logArtifactId] ?? 'Loading…'}</pre>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        {#if artifactsByKind(d.artifacts).length}
          <section class="block">
            <h3>Files</h3>
            <ul class="plain files">
              {#each artifactsByKind(d.artifacts) as a (a.id)}
                <li>
                  <Icon name="file" size={15} />
                  <a href="/v1/artifacts/{a.id}" target="_blank" rel="noopener">{a.name}</a>
                  <span class="meta">{a.kind} · {bytes(a.size)}{a.revision ? ` · ${shortSha(a.revision)}` : ''}</span>
                  <a class="meta" href="/v1/artifacts/{a.id}?download=1" download aria-label="Download {a.name}"><Icon name="download" size={14} /></a>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.decisions.length}
          <section class="block">
            <h3>Decisions recorded</h3>
            <ul class="plain">
              {#each d.decisions as dc (dc.id)}
                <li><button class="link-btn" onclick={() => app.openPanel({ kind: 'decision', id: dc.id })}>{dc.title}</button> <span class="meta">· {dc.status}</span></li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.inputs.length}
          <section class="block">
            <h3>Your updates</h3>
            <ul class="plain">
              {#each d.inputs as i (i.id)}
                {@const cur = app.data.inputs[i.id] ?? i}
                <li>
                  <p>“{cur.body}”</p>
                  <p class="meta">{deliveryReceipt(cur.delivery, app.engineerName(job.ownerId))} · {atTime(cur.createdAt)}</p>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.questions.length || d.approvals.length}
          <section class="block">
            <h3>Questions and permissions</h3>
            <ul class="plain">
              {#each d.questions as q (q.id)}
                <li>
                  <p>{q.missingFact}</p>
                  <p class="meta">
                    {q.status === 'open' ? 'Asked' : q.status === 'answered' ? 'Answered' : 'No longer needed'} · {app.engineerName(q.askerId)}
                    {#if q.continuingWith}{' · '}meanwhile: {q.continuingWith}{/if}
                    · <a href="/rooms/{q.source.roomId}?msg={q.messageId}">open in conversation</a>
                  </p>
                </li>
              {/each}
              {#each d.approvals as a (a.id)}
                <li>
                  <p>{a.action.summary}</p>
                  <p class="meta">{a.status} · <a href="/rooms/{a.source.roomId}{a.source.messageId ? `?msg=${a.source.messageId}` : ''}">open in conversation</a></p>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.followUps?.length}
          <section class="block">
            <h3>Follow-ups</h3>
            <ul class="plain">
              {#each d.followUps as f (f.id)}
                <li>
                  <button class="link-btn" onclick={() => app.openPanel({ kind: 'job', id: f.id })}>{f.title}</button>
                  <span class="meta">· {jobStateLabel(f)} · {app.engineerName(f.ownerId)}</span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
        {#if d.children.filter((c) => c.kind !== 'review').length}
          <section class="block">
            <h3>Related work</h3>
            <ul class="plain">
              {#each d.children.filter((c) => c.kind !== 'review') as c (c.id)}
                <li>
                  <button class="link-btn" onclick={() => app.openPanel({ kind: 'job', id: c.id })}>{c.title}</button>
                  <span class="meta">· {jobStateLabel(c)} · {app.engineerName(c.ownerId)}</span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {:else if tab === 'review'}
        {#if d.reviews.length === 0 && d.pullRequests.length === 0}
          <p class="meta">{job.requiresPeerReview ? 'No review has been requested yet. The owner asks a colleague when the work is ready.' : 'No review on this work.'}</p>
        {/if}
        {#each d.reviews as r (r.id)}
          <section class="block"><ReviewDetail review={app.data.reviews[r.id] ?? r} currentHead={job.revision?.head} /></section>
        {/each}
        {#each d.pullRequests as pr (pr.id)}
          <section class="block">
            <h3>Pull request</h3>
            <PRFacts pr={app.data.prs[pr.id] ?? pr} reviews={d.reviews} />
          </section>
        {/each}
      {:else if tab === 'activity'}
        {#if d.activity.length === 0}
          <p class="meta">No activity recorded yet.</p>
        {:else}
          <ol class="timeline">
            {#each d.activity as a, i (i)}
              <li>
                <time datetime={a.at} title={fullTime(a.at)}>{clock(a.at)}</time>
                <span>{a.text}</span>
              </li>
            {/each}
          </ol>
        {/if}
        {#if runs.length}
          <h3 class="sub-h">Tool logs</h3>
          {#each runs as r (r.id)}
            <details class="runlog" ontoggle={(e) => (e.currentTarget as HTMLDetailsElement).open && loadRunLog(r.id)}>
              <summary>Attempt {r.attempt} · {app.engineerName(r.engineerId)} · {runStateLabel(r.state)}</summary>
              {#if runLogs[r.id] === 'loading' || !runLogs[r.id]}
                <p class="meta">Loading…</p>
              {:else if typeof runLogs[r.id] === 'string'}
                <p class="notice danger">{runLogs[r.id]}</p>
              {:else}
                {@const acts = runLogs[r.id] as RunActivity[]}
                {#if acts.length === 0}<p class="meta">Nothing recorded.</p>{/if}
                <ol class="tools">
                  {#each acts as a (a.seq)}
                    <li class="k-{a.kind}">
                      <time datetime={a.at}>{clock(a.at)}</time>
                      <span class="tool-text">{a.text}</span>
                      {#if a.tool && !a.tool.startsWith('mcp__')}<span class="meta mono">{a.tool}</span>{/if}
                    </li>
                  {/each}
                </ol>
              {/if}
            </details>
          {/each}
        {/if}
      {:else if tab === 'runs'}
        {#if runs.length === 0}
          <p class="meta">No attempts yet.{job.state === 'queued' || job.state === 'waiting' ? ` ${job.stateDetail ?? ''}` : ''}</p>
        {/if}
        <ul class="plain runs">
          {#each runs as r (r.id)}
            {@const late = (d.quarantined ?? []).filter((q) => q.runId === r.id)}
            <li class="run">
              <p class="run-head tone-{runTone(r.state)}">
                <StateIcon shape={runShape(r.state)} tone={runTone(r.state)} live={r.state === 'running'} />
                <strong>Attempt {r.attempt}</strong> · {runStateLabel(r.state)}
              </p>
              {#if r.state === 'unknown'}
                <p class="notice attention">The outcome is not confirmed. The machine stopped reporting before the attempt finished; it may or may not have made changes.</p>
              {/if}
              <dl class="props small">
                <div><dt>Engineer</dt><dd>{app.engineerName(r.engineerId)}</dd></div>
                <div><dt>Machine</dt><dd>{app.nodeName(r.nodeId) || 'Not assigned'}</dd></div>
                <div>
                  <dt>Provider</dt>
                  <dd>
                    {providerLabel(r.provider)}{r.model ? ` · ${r.model}` : ''} · {billingLabel(r.usage?.billing)}
                    {#if r.usage?.inputTokens != null}{' '}<span class="meta">· {r.usage.inputTokens} in / {r.usage.outputTokens ?? 0} out tokens</span>{/if}
                  </dd>
                </div>
                <div><dt>Mode</dt><dd>{r.mode === 'edit' ? 'Can edit the workspace' : r.mode === 'readonly' ? 'Read-only' : 'Conversation'}</dd></div>
                {#if r.startedAt}<div><dt>Started</dt><dd>{atTime(r.startedAt)}</dd></div>{/if}
                {#if r.endedAt}<div><dt>Ended</dt><dd>{atTime(r.endedAt)}{r.terminalReason && r.terminalReason !== r.state ? ` · ${r.terminalReason}` : ''}</dd></div>{/if}
                {#if r.lastActivity}<div><dt>Last confirmed</dt><dd>{r.lastActivity}{r.lastActivityAt ? ` · ${relative(r.lastActivityAt, app.now)}` : ''}</dd></div>{/if}
                {#if r.resultRev}<div><dt>Result</dt><dd class="mono">{shortSha(r.resultRev)}{r.branch ? ` on ${r.branch}` : ''}</dd></div>{/if}
              </dl>
              {#if late.length}
                <details class="late">
                  <summary>{late.length} late {late.length === 1 ? 'report' : 'reports'} kept for diagnosis</summary>
                  <p class="meta">
                    {app.nodeName(late[0].nodeId) || 'The machine'} sent these after this attempt had lost its lease. They didn't change the work.
                  </p>
                  <ul class="plain">
                    {#each late as q (q.id)}
                      <li><span class="meta">{clock(q.receivedAt)} · {q.kind === 'tool_call' ? 'tool call' : q.kind === 'terminal' ? 'final report' : 'event'} ·</span> <span class="mono">{q.summary}</span></li>
                    {/each}
                  </ul>
                </details>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</RightPanel>

{#if confirmStop && job}
  <ConfirmDialog
    title="Stop this work?"
    body="Stopping ends {job.title} and any work it started. Changes already made stay where they are; nothing is deleted. You can resume it later from its last checkpoint."
    confirmLabel="Stop the work"
    danger
    onconfirm={stop}
    onclose={() => (confirmStop = false)}
  />
{/if}

<style>
  .late {
    margin-top: 8px;
    font-size: 13px;
  }
  .late summary {
    cursor: pointer;
    color: var(--ink-secondary);
  }
  .late .mono {
    overflow-wrap: anywhere;
  }
  .pad {
    padding: 14px 18px;
  }
  .head {
    display: grid;
    gap: 10px;
    border-bottom: 0;
  }
  .state {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 15px;
  }
  .props {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .props > div {
    display: grid;
    grid-template-columns: 110px minmax(0, 1fr);
    gap: 10px;
    font-size: 14px;
  }
  .props.small > div {
    grid-template-columns: 100px minmax(0, 1fr);
    font-size: 13.5px;
  }
  dt {
    color: var(--ink-secondary);
    font-size: 13px;
  }
  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .person,
  .person-static {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--ink);
    font: inherit;
    cursor: pointer;
  }
  .person-static {
    cursor: default;
  }
  .person:hover {
    text-decoration: underline;
  }
  .people {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .tabs {
    padding: 0 12px;
  }
  .tabpanel:focus-visible {
    outline: none;
  }
  .block {
    margin-bottom: 20px;
  }
  .block-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-bottom: 8px;
  }
  .block-head h3 {
    margin: 0;
  }
  h3 {
    font-size: 14px;
    font-weight: 650;
    margin-bottom: 8px;
  }
  .sub-h {
    margin-top: 18px;
  }
  .rev-stats {
    margin: -2px 0 10px;
    font-size: 13.5px;
    color: var(--ink-secondary);
  }
  .rev-stats .add {
    color: var(--success);
    font-weight: 600;
  }
  .rev-stats .del {
    color: var(--danger);
    font-weight: 600;
  }
  .rev-pick .select {
    min-height: 32px;
    padding: 4px 8px;
    font-size: 13px;
  }
  .plain {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
    font-size: 14px;
  }
  ul.plain:not(.files):not(.runs) li {
    padding-left: 0;
  }
  .files li {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .checks {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .check-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 6px 10px;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: var(--r-artifact);
  }
  .check-row.stale {
    background: var(--surface-subtle);
  }
  .check-main {
    display: flex;
    gap: 8px;
    min-width: 0;
  }
  .check-main :global(svg) {
    margin-top: 3px;
  }
  .check-text {
    min-width: 0;
  }
  .summary-out,
  .log {
    grid-column: 1 / -1;
    margin: 4px 0 0;
    padding: 8px 10px;
    border-radius: 6px;
    background: var(--surface-subtle);
    font-size: 12.5px;
    max-height: 280px;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .timeline,
  .tools {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
    font-size: 14px;
  }
  .timeline li,
  .tools li {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) auto;
    gap: 8px;
  }
  time {
    color: var(--ink-secondary);
    font-size: 12.5px;
    font-variant-numeric: tabular-nums;
    padding-top: 1px;
  }
  .runlog {
    border: 1px solid var(--line);
    border-radius: var(--r-artifact);
    padding: 6px 10px;
    margin-bottom: 8px;
  }
  .runlog summary {
    cursor: pointer;
    font-size: 14px;
    font-weight: 560;
    min-height: 28px;
    display: list-item;
  }
  .runlog[open] summary {
    margin-bottom: 6px;
  }
  .tools .tool-text {
    color: var(--ink-secondary);
  }
  .tools li.k-error .tool-text,
  .tools li.k-warning .tool-text {
    color: var(--danger);
    font-weight: 560;
  }
  .tools li.k-status .tool-text,
  .tools li.k-started .tool-text {
    color: var(--ink);
  }
  .runs {
    gap: 14px;
  }
  .run {
    display: grid;
    gap: 8px;
    padding-bottom: 14px;
    border-bottom: 1px solid var(--line-soft);
  }
  .run-head {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 14px;
  }
  @media (max-width: 480px) {
    .props > div {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
</style>
