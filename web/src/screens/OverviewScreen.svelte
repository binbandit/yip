<script lang="ts">
  // Cross-project catch-up and the factual work ledger. The visit is recorded
  // only after the page has rendered, and nothing here marks a room read.
  import { onMount, tick, untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Job, Overview, Question } from '../lib/api/types.gen';
  import { catchupKindLabel, catchupShape, catchupTone } from '../lib/util/labels';
  import { jobRunState, mergeWorkRows, newer } from '../lib/state/data';
  import { atTime, fullTime, relative } from '../lib/util/time';
  import { conversationHref } from '../lib/util/conversation';
  import StateIcon from '../components/StateIcon.svelte';
  import WorkRowItem from '../components/WorkRowItem.svelte';
  import MessageList from '../components/MessageList.svelte';
  import OverviewSummary from '../components/OverviewSummary.svelte';
  import Icon from '../components/Icon.svelte';
  import GettingStarted from '../components/GettingStarted.svelte';

  let ov = $state<Overview | null>(null);
  let error = $state('');
  let loading = $state(true);
  let visitSince: string | undefined;
  let mounted = false;
  let fetching = false;
  let refreshAgain = false;
  let refreshTimer: ReturnType<typeof setTimeout> | undefined;
  let observedKey: string | undefined;
  // Detail counters change for durable work events, never streamed token text.
  const refreshKey = $derived(JSON.stringify([
    app.resetEpoch,
    app.data.touched.jobs,
    Object.values(app.data.decisions).map((d) => [d.id, d.version]),
  ]));

  function refreshSoon() {
    if (refreshTimer) return;
    refreshTimer = setTimeout(() => {
      refreshTimer = undefined;
      void load(false);
    }, 150);
  }

  async function load(markSeen: boolean) {
    if (!mounted) return;
    if (fetching) {
      refreshAgain = true;
      return;
    }
    fetching = true;
    error = '';
    try {
      const o = await api.overview(visitSince);
      if (!mounted) return;
      // Marking the visit must not shorten this page's catch-up on refresh.
      visitSince ??= o.since ?? new Date(Date.now() - 24 * 3600_000).toISOString();
      o.since = visitSince;
      o.catchup = o.catchup ?? [];
      o.work = o.work ?? [];
      o.decisions = o.decisions ?? [];
      o.questions = o.questions ?? [];
      ov = o;
      mergeWorkRows(app.data, o.work);
      for (const d of o.decisions) if (newer(app.data.decisions[d.id], d)) app.data.decisions[d.id] = d;
      for (const q of o.questions) {
        if (!app.data.questions[q.id] || app.data.questions[q.id].status === 'open') app.data.questions[q.id] = q;
      }
      loading = false;
      if (markSeen) {
        await tick();
        requestAnimationFrame(() => {
          if (!mounted) return;
          api.overviewSeen().catch(() => {
            /* the next visit will record it */
          });
        });
      }
    } catch (err) {
      if (mounted) {
        error = errorMessage(err);
        loading = false;
      }
    } finally {
      fetching = false;
      if (mounted && refreshAgain) {
        refreshAgain = false;
        refreshSoon();
      }
    }
  }

  onMount(() => {
    mounted = true;
    void load(true);
    return () => {
      mounted = false;
      clearTimeout(refreshTimer);
    };
  });
  $effect(() => {
    const key = refreshKey;
    if (observedKey !== undefined && observedKey !== key) untrack(refreshSoon);
    observedKey = key;
  });

  const myRooms = $derived(new Set(app.rooms.map((r) => r.id)));
  // Live ledger: API rows plus work that started since, always at the newest version.
  const ledger = $derived.by(() => {
    const rows = new Map<string, { job: Job; lastConfirmed?: string; lastConfirmedAt?: string | null; blocker?: string; questions?: Question[] }>();
    for (const w of ov?.work ?? []) rows.set(w.job.id, {
      job: app.data.jobs[w.job.id] ?? w.job, lastConfirmed: w.lastConfirmed, lastConfirmedAt: w.lastConfirmedAt, blocker: w.blocker,
      questions: (w.questions ?? []).map((q) => app.data.questions[q.id] ?? q).filter((q) => q.status === 'open' && myRooms.has(q.source.roomId)),
    });
    for (const j of Object.values(app.data.jobs)) {
      if (rows.has(j.id) || j.kind === 'reply' || j.kind === 'review' || j.parentId || j.state === 'cancelled' || !myRooms.has(j.source.roomId)) continue;
      if (j.state === 'completed' && app.now - Date.parse(j.completedAt ?? j.updatedAt) > 7 * 86400_000) continue;
      rows.set(j.id, { job: j });
    }
    return [...rows.values()];
  });
  // runState "unknown": the latest attempt's outcome is not confirmed.
  const unknownJobs = $derived(new Set(ledger.filter((r) => jobRunState(app.data, r.job) === 'unknown').map((r) => r.job.id)));
  const needs = $derived(ledger.filter((r) => r.questions?.length || (r.job.state === 'waiting' && r.job.waitingReason !== 'review') || r.job.state === 'failed' || unknownJobs.has(r.job.id)));
  const active = $derived(ledger.filter((r) => !needs.includes(r) && (r.job.state === 'running' || r.job.state === 'queued' || r.job.state === 'review_ready' || r.job.state === 'waiting')));
  const done = $derived(
    ledger.filter((r) => r.job.state === 'completed' && !needs.includes(r)).sort((a, b) => (b.job.completedAt ?? '').localeCompare(a.job.completedAt ?? '')),
  );

  const overviewRoom = $derived(app.overviewRoom);
  // Evidence uses the same side space as the optional Overview conversation.
  const wide = $derived(app.viewport >= 1180 && !app.loc.panel);
  const tl = $derived(overviewRoom ? app.data.timelines[overviewRoom.id] : undefined);
  const convo = $derived(tl ? tl.ids.map((id) => app.data.messages[id]).filter(Boolean) : []);
  const convoPending = $derived(overviewRoom ? Object.values(app.data.pending).filter((p) => p.roomId === overviewRoom.id && !p.threadId) : []);
  $effect(() => {
    const r = overviewRoom;
    if (r && wide && !app.data.timelines[r.id]?.loaded) untrack(() => void app.loadRoom(r.id).catch(() => {}));
  });

  function catchupHref(c: Overview['catchup'][number]): string | null {
    if (!c.roomId) return null;
    return conversationHref({ roomId: c.roomId, messageId: c.messageId, threadId: c.threadId });
  }
</script>

<div class="overview" class:wide>
  <div class="screen main">
    <div class="screen-inner">
      <header class="screen-head">
        <div>
          <h1 class="screen-title" data-screen-title tabindex="-1">Overview</h1>
          <p class="screen-sub">
            {#if ov?.since}Since you were here {atTime(ov.since)}{:else}Across your rooms and projects{/if}
          </p>
        </div>
        {#if overviewRoom && !wide}
          <a class="btn" href="/rooms/{overviewRoom.id}"><Icon name="reply" size={16} />Open workspace summary</a>
        {/if}
      </header>

      <GettingStarted />

      {#if error}
        <p class="notice danger" role="alert">{error} <button class="link-btn" onclick={() => load(false)}>Retry</button></p>
      {/if}
      {#if loading}
        <p class="meta" aria-busy="true">Gathering what changed…</p>
      {:else if ov}
        <section class="section first" aria-labelledby="ov-catchup">
          <div class="section-head">
            <h2 class="section-title" id="ov-catchup">Since you were here</h2>
            <span class="meta">What changed, with current open work below. Conversations stay unread.</span>
          </div>
          {#if ov.catchup.length === 0}
            <p class="empty-line">Nothing new since your last visit.</p>
          {:else}
            <ul class="catchup">
              {#each ov.catchup as c (c.eventSeq + c.kind)}
                {@const h = catchupHref(c)}
                <li>
                  <span class="kind tone-{catchupTone(c.kind)}"><StateIcon shape={catchupShape(c.kind)} tone={catchupTone(c.kind)} />{catchupKindLabel(c.kind)}</span>
                  <div class="c-body">
                    <p class="c-title">{c.title}</p>
                    {#if c.detail}<p class="c-detail">{c.detail}</p>{/if}
                    <p class="meta">
                      <time datetime={c.at} title={fullTime(c.at)}>{relative(c.at, app.now)}</time>
                      {#if h && c.roomId}· <a href={h}>in {app.data.rooms[c.roomId]?.name ?? 'the conversation'}</a>{/if}
                      {#each c.refs ?? [] as ref}
                        {#if ref.kind === 'job' || ref.kind === 'review' || ref.kind === 'decision'}
                          · <button class="link-btn" onclick={() => app.openPanel({ kind: ref.kind as 'job' | 'review' | 'decision', id: ref.id })}>{ref.kind === 'job' ? 'open the work' : `view ${ref.kind}`}</button>
                        {:else if ref.kind === 'question' && app.data.questions[ref.id]}
                          {@const question = app.data.questions[ref.id]}
                          · <a href={conversationHref({ ...question.source, messageId: question.messageId })}>view question</a>
                        {:else if ref.kind === 'approval'}
                          {@const work = c.refs?.find((r) => r.kind === 'job')}
                          {#if work}· <button class="link-btn" onclick={() => app.openPanel({ kind: 'job', id: work.id })}>view permission request</button>{/if}
                        {/if}
                      {/each}
                    </p>
                  </div>
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <section class="section" aria-labelledby="ov-done">
          <div class="section-head"><h2 class="section-title" id="ov-done">Recently completed</h2><span class="meta">last 7 days</span></div>
          {#if done.length === 0}
            <p class="empty-line">Nothing completed recently.</p>
          {:else}
            <ul class="rows">{#each done.slice(0, 12) as r (r.job.id)}<WorkRowItem {...r} />{/each}</ul>
          {/if}
        </section>

        <section class="section" aria-labelledby="ov-active">
          <div class="section-head"><h2 class="section-title" id="ov-active">Active</h2><span class="meta">{active.length || ''}</span></div>
          {#if active.length === 0}
            <p class="empty-line">No work in progress. Ask an engineer in a room to start something.</p>
          {:else}
            <ul class="rows">{#each active as r (r.job.id)}<WorkRowItem {...r} />{/each}</ul>
          {/if}
        </section>

        <section class="section" aria-labelledby="ov-needs">
          <div class="section-head"><h2 class="section-title" id="ov-needs">Needs a look</h2><span class="meta">{needs.length || ''}</span></div>
          {#if needs.length === 0}
            <p class="empty-line">Nothing is blocked or failed.</p>
          {:else}
            <ul class="rows">
              {#each needs as r (r.job.id)}<WorkRowItem {...r} unknownOutcome={unknownJobs.has(r.job.id)} />{/each}
            </ul>
          {/if}
        </section>

        <section class="section" aria-labelledby="ov-dec">
          <div class="section-head"><h2 class="section-title" id="ov-dec">Worth remembering</h2></div>
          {#if ov.decisions.length === 0}
            <p class="empty-line">No accepted decisions yet. Engineers record them with their sources as work is reviewed.</p>
          {:else}
            <ul class="decisions">
              {#each ov.decisions as d (d.id)}
                <li>
                  <button class="dec" onclick={() => app.openPanel({ kind: 'decision', id: d.id })}>
                    <Icon name="book" size={16} />
                    <span>
                      <span class="d-title">{d.title}</span>
                      <span class="meta">
                        {d.scope.kind === 'project' ? app.data.projects[d.scope.id]?.name : d.scope.kind === 'room' ? app.data.rooms[d.scope.id]?.name : 'Workspace'} ·
                        {d.sources.length} {d.sources.length === 1 ? 'source' : 'sources'} · {relative(d.acceptedAt ?? d.createdAt, app.now)}
                      </span>
                    </span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/if}
    </div>
  </div>

  {#if wide && overviewRoom}
    <aside class="convo" aria-labelledby="ov-convo">
      <header class="convo-head">
        <h2 id="ov-convo">Workspace summary</h2>
      </header>
      <MessageList
        label="Overview conversation"
        items={convo}
        pending={convoPending}
        loaded={!!tl?.loaded}
        hasMore={!!tl?.hasMore}
        onloadolder={() => app.loadOlder(overviewRoom.id)}
      >
      </MessageList>
      <OverviewSummary roomId={overviewRoom.id} />
    </aside>
  {/if}
</div>

<style>
  .overview {
    flex: 1;
    min-width: 0;
    min-height: 0;
    display: flex;
  }
  .main {
    flex: 1;
    min-width: 0;
    container-type: inline-size;
    container-name: overview-main;
  }
  .first {
    margin-top: 0;
  }
  .empty-line {
    color: var(--ink-secondary);
    font-size: 14px;
    padding: 6px 0;
  }
  .catchup,
  .rows,
  .decisions {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .catchup li {
    display: grid;
    grid-template-columns: 140px minmax(0, 1fr);
    gap: 12px;
    padding: 10px 8px;
    border-top: 1px solid var(--line-soft);
  }
  .catchup li:first-child {
    border-top: 0;
  }
  .kind {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 650;
    height: 22px;
  }
  .c-title {
    font-weight: 600;
    font-size: 14.5px;
  }
  .c-detail {
    font-size: 14px;
  }
  .rows {
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 2px 6px;
  }
  .decisions {
    display: grid;
    gap: 2px;
  }
  .dec {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    width: 100%;
    padding: 8px;
    border: 0;
    border-radius: 10px;
    background: none;
    color: var(--ink);
    text-align: left;
    cursor: pointer;
  }
  .dec:hover {
    background: var(--hover);
  }
  .dec :global(svg) {
    margin-top: 3px;
    color: var(--ink-secondary);
  }
  .dec > span {
    display: grid;
  }
  .d-title {
    font-weight: 600;
    font-size: 14.5px;
  }
  .convo {
    width: 400px;
    flex: none;
    display: flex;
    flex-direction: column;
    min-height: 0;
    border-left: 1px solid var(--line);
  }
  .convo-head {
    padding: 16px 18px 10px;
    border-bottom: 1px solid var(--line);
  }
  .convo-head h2 {
    font-size: 16px;
  }
  @container overview-main (max-width: 560px) {
    .catchup li {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }
</style>
