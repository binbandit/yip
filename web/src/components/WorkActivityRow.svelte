<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { Button } from '@astryx-svelte/core';
  import { api } from '../lib/api/endpoints';
  import type { JobDetail } from '../lib/api/types.gen';
  import { app } from '../lib/state/app.svelte';
  import type { DataState } from '../lib/state/data';
  import { attemptElapsed, currentWorkRun, inConversation, isCurrentWorkRun, permissionRequested, recordedActivity, recordedChecks, workState, type RecordedActivity } from '../lib/util/workActivity';
  import { clock, fullTime } from '../lib/util/time';

  let { jobId, roomId, threadId, now }: { jobId: string; roomId: string; threadId?: string; now: number } = $props();
  const job = $derived(app.data.jobs[jobId]);
  const identity = $derived(JSON.stringify([roomId, threadId ?? '', jobId, job?.currentRunId, job?.ownerId]));
  const selected = $derived(threadId
    ? app.loc.panel?.kind === 'thread' && app.loc.panel.id === threadId
    : app.loc.route.name === 'room' && app.loc.route.roomId === roomId);
  const connected = $derived(app.online && app.connection === 'live');
  let open = $state(false);
  let detail = $state<JobDetail>();
  let records = $state<RecordedActivity[]>([]);
  let capped = $state(false);
  let loading = $state(false);
  let loaded = $state(false);
  let error = $state(false);
  let visible = $state(document.visibilityState !== 'hidden');
  onMount(() => {
    const changed = () => (visible = document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', changed);
    return () => document.removeEventListener('visibilitychange', changed);
  });
  const run = $derived(job ? currentWorkRun(job, app.data.runs[job.currentRunId ?? ''], detail?.runs.find((r) => r.id === job.currentRunId)) : undefined);
  const reporting = $derived(connected && (!run?.nodeId || app.data.nodes[run.nodeId]?.status === 'online'));
  const stateLabel = $derived(job ? workState(job, run, reporting, now) : '');
  const elapsed = $derived(attemptElapsed(run, reporting && !!job && ['queued', 'running', 'waiting'].includes(job.state), now));
  const checks = $derived(job && run && detail ? recordedChecks(job, run, detail.checks ?? []) : '');
  const permission = $derived(job && run && permissionRequested(job, run, [...(detail?.approvals ?? []), ...Object.values(app.data.approvals)], now));

  type Target = { data: DataState; jobId: string; runId: string; ownerId: string; roomId: string; threadId?: string; identity: string; epoch: number };
  let target: Target | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let inFlight = false;
  let pending = false;
  let lastStart = -Infinity;
  let request: AbortController | undefined;

  function current(t: Target): boolean {
    const j = app.data.jobs[t.jobId];
    return target === t && app.data === t.data && app.resetEpoch === t.epoch && open && selected && connected && visible
      && identity === t.identity && !!j && j.currentRunId === t.runId && j.ownerId === t.ownerId
      && inConversation(j, t.roomId, t.threadId);
  }

  function schedule() {
    if (!target || !current(target)) return;
    pending = true;
    if (inFlight || timer) return;
    // Touches can be rapid. Keep one request pair in flight and at most one
    // trailing refresh, with a minimum two seconds between starts.
    timer = setTimeout(() => { timer = undefined; void load(); }, Math.max(0, 2000 - (Date.now() - lastStart)));
  }

  async function load() {
    const t = target;
    if (!t || !current(t)) return;
    pending = false;
    inFlight = true;
    lastStart = Date.now();
    loading = true;
    const controller = new AbortController();
    request = controller;
    const timeout = setTimeout(() => controller.abort(), 15_000);
    try {
      const results = await Promise.allSettled([api.job(t.jobId, controller.signal), api.runActivity(t.jobId, t.runId, controller.signal)]);
      if (!current(t)) return;
      if (controller.signal.aborted) throw new Error('unavailable');
      if (results[0].status !== 'fulfilled' || results[1].status !== 'fulfilled') throw new Error('unavailable');
      const d = results[0].value;
      const events = results[1].value;
      const r = d.runs.find((r) => r.id === t.runId);
      if (d.job.id !== t.jobId || d.job.ownerId !== t.ownerId || d.job.currentRunId !== t.runId
        || !inConversation(d.job, t.roomId, t.threadId) || !isCurrentWorkRun(d.job, r)) throw new Error('scope');
      detail = d;
      records = recordedActivity(events, t.runId);
      capped = events.length >= 2000;
      loaded = true;
      error = false;
    } catch {
      if (current(t)) error = true;
    } finally {
      clearTimeout(timeout);
      if (request === controller) request = undefined;
      inFlight = false;
      if (current(t)) loading = false;
      if (pending) schedule();
    }
  }

  $effect(() => {
    const data = app.data;
    const key = identity;
    const enabled = open && selected && connected && visible;
    const epoch = app.resetEpoch;
    untrack(() => {
      detail = undefined;
      records = [];
      loaded = false;
      error = false;
      capped = false;
      target = enabled && job?.currentRunId && inConversation(job, roomId, threadId)
        ? { data, identity: key, epoch, jobId, runId: job.currentRunId, ownerId: job.ownerId, roomId, threadId } : undefined;
      loading = !!target;
      schedule();
    });
    return () => {
      target = undefined;
      pending = false;
      if (timer) clearTimeout(timer);
      timer = undefined;
      request?.abort();
    };
  });
  $effect(() => {
    const touch = app.data.touched.jobs[jobId] ?? 0;
    untrack(() => { if (touch >= 0) schedule(); });
  });
</script>

{#if job}
  <div class="work-row">
    <button class="summary" aria-expanded={open} onclick={() => (open = !open)}>
      <span class="chevron" aria-hidden="true">{open ? '▾' : '▸'}</span>
      <span class="name">{app.engineerName(job.ownerId)}</span>
      <span class="task" title={job.title}>{job.title}</span>
      <span class="state">{stateLabel}</span>
    </button>
    {#if open}
      <div class="detail">
        <p class="meta">{#if run}Attempt {run.attempt}{#if elapsed} · <span title="Time since this attempt started, including waiting">{elapsed}</span>{/if}{:else}No confirmed attempt details.{/if}</p>
        {#if permission}<p>Permission requested for this attempt.</p>{/if}
        {#if !connected}
          <p>Live updates are unavailable. Reconnect to refresh recorded activity.</p>
        {:else if !job.currentRunId}
          <p>No attempt has started yet.</p>
        {:else if error}
          <p>Recorded activity is unavailable. <button class="retry" onclick={schedule}>Try again</button></p>
        {:else if loading && !loaded}
          <p>Loading recorded activity…</p>
        {:else if loaded}
          {#if capped}<p>Only the first 2,000 records are available. This history is incomplete; the latest activity is unavailable.</p>{/if}
          {#if records.length}
            <ol aria-label="Recorded activity">
              {#each records as record (record.seq)}
                <li><time datetime={record.at} title={fullTime(record.at)}>{clock(record.at)}</time><span>{record.label}</span></li>
              {/each}
            </ol>
          {:else}<p>No tool or progress records for this attempt.</p>{/if}
          {#if checks}<p>{checks}</p>{/if}
          {#if loading}<p class="meta">Refreshing recorded activity…</p>{/if}
        {/if}
        <Button label="Open work details" size="sm" variant="ghost" onclick={() => app.openPanel({ kind: 'job', id: job.id })} />
      </div>
    {/if}
  </div>
{/if}

<style>
  .work-row { border-top: 1px solid var(--color-border); }
  .summary { display: flex; align-items: baseline; gap: var(--spacing-2); width: 100%; padding: var(--spacing-2); text-align: left; color: var(--color-text-primary); font-size: var(--text-supporting-size); }
  .summary:hover { background: var(--color-overlay-hover); }
  .summary:focus-visible, .retry:focus-visible { outline: var(--focus-outline-width) var(--focus-outline-style) var(--focus-outline-color); outline-offset: -2px; }
  .name { font-weight: var(--font-weight-semibold); }
  .task { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .state, .meta, time { color: var(--color-text-secondary); }
  .state { flex: none; }
  .detail { padding: 0 var(--spacing-3) var(--spacing-3); font-size: var(--text-supporting-size); line-height: var(--text-supporting-leading); }
  p { margin: var(--spacing-2) 0; }
  ol { list-style: none; padding: 0; margin: var(--spacing-3) 0; }
  li { display: flex; gap: var(--spacing-3); margin: var(--spacing-1) 0; }
  li span { overflow-wrap: anywhere; }
  time { flex: none; font-variant-numeric: tabular-nums; }
  .retry { text-decoration: underline; }
  @media (max-width: 640px) {
    .summary { flex-wrap: wrap; }
    .task { flex-basis: 50%; }
    .state { margin-left: var(--spacing-4); }
  }
</style>
