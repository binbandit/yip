<script lang="ts">
  // Cross-project catch-up and the factual work ledger. The visit is recorded
  // only after the page has rendered, and nothing here marks a room read.
  import { onMount, tick, untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Job, Overview } from '../lib/api/types.gen';
  import { catchupKindLabel, catchupShape, catchupTone } from '../lib/util/labels';
  import { atTime, fullTime, relative } from '../lib/util/time';
  import StateIcon from '../components/StateIcon.svelte';
  import WorkRowItem from '../components/WorkRowItem.svelte';
  import MessageList from '../components/MessageList.svelte';
  import Composer from '../components/Composer.svelte';
  import Icon from '../components/Icon.svelte';

  let ov = $state<Overview | null>(null);
  let error = $state('');
  let loading = $state(true);
  let composer: Composer | undefined = $state();

  async function load(markSeen: boolean) {
    error = '';
    try {
      const o = await api.overview(false);
      o.catchup = o.catchup ?? [];
      o.work = o.work ?? [];
      o.decisions = o.decisions ?? [];
      o.questions = o.questions ?? [];
      ov = o;
      for (const w of o.work) {
        const cur = app.data.jobs[w.job.id];
        if (!cur || w.job.version >= cur.version) app.data.jobs[w.job.id] = w.job;
      }
      for (const d of o.decisions) app.data.decisions[d.id] = d;
      for (const q of o.questions) app.data.questions[q.id] = q;
      loading = false;
      if (markSeen) {
        await tick();
        requestAnimationFrame(() => {
          api.overview(true).catch(() => {
            /* the next visit will record it */
          });
        });
      }
    } catch (err) {
      error = errorMessage(err);
      loading = false;
    }
  }

  onMount(() => void load(true));
  $effect(() => {
    if (app.resetEpoch > 0) untrack(() => void load(false));
  });

  const myRooms = $derived(new Set(app.rooms.map((r) => r.id)));
  // Live ledger: API rows plus work that started since, always at the newest version.
  const ledger = $derived.by(() => {
    const rows = new Map<string, { job: Job; lastConfirmed?: string; lastConfirmedAt?: string | null; blocker?: string }>();
    for (const w of ov?.work ?? []) rows.set(w.job.id, { job: app.data.jobs[w.job.id] ?? w.job, lastConfirmed: w.lastConfirmed, lastConfirmedAt: w.lastConfirmedAt, blocker: w.blocker });
    for (const j of Object.values(app.data.jobs)) {
      if (rows.has(j.id) || j.kind === 'reply' || j.kind === 'review' || j.state === 'cancelled' || !myRooms.has(j.source.roomId)) continue;
      if (j.state === 'completed' && app.now - Date.parse(j.completedAt ?? j.updatedAt) > 7 * 86400_000) continue;
      rows.set(j.id, { job: j });
    }
    return [...rows.values()];
  });
  const unknownJobs = $derived(new Set(ledger.filter((r) => r.job.currentRunId && app.data.runs[r.job.currentRunId]?.state === 'unknown').map((r) => r.job.id)));
  const needs = $derived(ledger.filter((r) => r.job.state === 'waiting' || r.job.state === 'failed' || unknownJobs.has(r.job.id)));
  const active = $derived(ledger.filter((r) => !needs.includes(r) && (r.job.state === 'running' || r.job.state === 'queued' || r.job.state === 'review_ready')));
  const done = $derived(
    ledger.filter((r) => r.job.state === 'completed').sort((a, b) => (b.job.completedAt ?? '').localeCompare(a.job.completedAt ?? '')),
  );

  const overviewRoom = $derived(app.overviewRoom);
  const wide = $derived(app.viewport >= 1180);
  const tl = $derived(overviewRoom ? app.data.timelines[overviewRoom.id] : undefined);
  const convo = $derived(tl ? tl.ids.map((id) => app.data.messages[id]).filter(Boolean) : []);
  const convoPending = $derived(overviewRoom ? Object.values(app.data.pending).filter((p) => p.roomId === overviewRoom.id && !p.threadId) : []);
  $effect(() => {
    const r = overviewRoom;
    if (r && wide && !app.data.timelines[r.id]?.loaded) untrack(() => void app.loadRoom(r.id).catch(() => {}));
  });

  function catchupHref(c: Overview['catchup'][number]): string | null {
    if (!c.roomId) return null;
    const q = new URLSearchParams();
    if (c.messageId) q.set('msg', c.messageId);
    if (c.threadId) q.set('panel', `thread:${c.threadId}`);
    const s = q.toString();
    return `/rooms/${c.roomId}${s ? `?${s}` : ''}`;
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
          <a class="btn" href="/rooms/{overviewRoom.id}"><Icon name="reply" size={16} />Ask where things stand</a>
        {/if}
      </header>

      {#if error}
        <div class="notice danger" role="alert">
          <span>{error}</span>
          <button class="btn btn-sm" onclick={() => load(false)}>Retry</button>
        </div>
      {:else if loading}
        <p class="meta" aria-busy="true">Gathering what changed…</p>
      {:else if ov}
        <section class="section first" aria-labelledby="ov-catchup">
          <div class="section-head">
            <h2 class="section-title" id="ov-catchup">Since you were here</h2>
            <span class="meta">Opening this doesn't mark conversations read.</span>
          </div>
          {#if ov.catchup.length === 0}
            <p class="empty-line">Nothing new since your last visit.</p>
          {:else}
            <ul class="catchup">
              {#each ov.catchup as c (c.eventSeq + c.kind)}
                {@const h = catchupHref(c)}
                {@const jobRef = c.refs?.find((r) => r.kind === 'job')}
                <li>
                  <span class="kind tone-{catchupTone(c.kind)}"><StateIcon shape={catchupShape(c.kind)} tone={catchupTone(c.kind)} />{catchupKindLabel(c.kind)}</span>
                  <div class="c-body">
                    <p class="c-title">{c.title}</p>
                    {#if c.detail}<p class="c-detail">{c.detail}</p>{/if}
                    <p class="meta">
                      <time datetime={c.at} title={fullTime(c.at)}>{relative(c.at, app.now)}</time>
                      {#if h && c.roomId}· <a href={h}>in {app.data.rooms[c.roomId]?.name ?? 'the conversation'}</a>{/if}
                      {#if jobRef}· <button class="link-btn" onclick={() => app.openPanel({ kind: 'job', id: jobRef.id })}>open the work</button>{/if}
                    </p>
                  </div>
                </li>
              {/each}
            </ul>
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

        <section class="section" aria-labelledby="ov-active">
          <div class="section-head"><h2 class="section-title" id="ov-active">Active</h2><span class="meta">{active.length || ''}</span></div>
          {#if active.length === 0}
            <p class="empty-line">No work in progress. Ask an engineer in a room to start something.</p>
          {:else}
            <ul class="rows">{#each active as r (r.job.id)}<WorkRowItem {...r} />{/each}</ul>
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
        <h2 id="ov-convo">Ask about everything</h2>
        <p class="meta">Answers come from the ledger with confirmed times; nobody is woken to report status.</p>
      </header>
      <MessageList
        label="Overview conversation"
        items={convo}
        pending={convoPending}
        loaded={!!tl?.loaded}
        hasMore={!!tl?.hasMore}
        onloadolder={() => app.loadOlder(overviewRoom.id)}
        onreply={(m) => app.go({ name: 'room', roomId: overviewRoom.id }, { panel: { kind: 'thread', id: m.id } })}
      >
        {#snippet empty()}
          <div class="convo-empty">
            <button class="btn btn-sm" onclick={() => composer?.fill('Where are we with everything?')}>“Where are we with everything?”</button>
          </div>
        {/snippet}
      </MessageList>
      <Composer bind:this={composer} roomId={overviewRoom.id} placeholder="Ask where things stand…" compact />
    </aside>
  {/if}
</div>

<style>
  .overview {
    flex: 1;
    min-height: 0;
    display: flex;
  }
  .main {
    flex: 1;
    min-width: 0;
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
  .convo-empty {
    padding: 20px;
  }
  @media (max-width: 760px) {
    .catchup li {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }
</style>
