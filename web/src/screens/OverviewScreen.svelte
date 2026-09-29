<script lang="ts">
  // Cross-project catch-up and the factual work ledger. The visit is recorded
  // only after the page has rendered, and nothing here marks a room read.
  import { onMount, tick, untrack } from 'svelte';
  import { Button, Heading, Icon, Link, List, ListItem, Spinner, Text } from '@astryx-svelte/core';
  import { Book, MessageSquareText } from '@lucide/svelte';
  import Notice from '../components/Notice.svelte';
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
  import Screen from '../components/Screen.svelte';
  import ScreenSection from '../components/ScreenSection.svelte';
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

  const subtitle = $derived(ov?.since ? `Since you were here ${atTime(ov.since)}` : 'Across your rooms and projects');

  function catchupHref(c: Overview['catchup'][number]): string | null {
    if (!c.roomId) return null;
    return conversationHref({ roomId: c.roomId, messageId: c.messageId, threadId: c.threadId });
  }
</script>

{#snippet summaryLink()}
  {#if overviewRoom}
    <Button label="Open workspace summary" href="/rooms/{overviewRoom.id}">
      {#snippet icon()}<Icon icon={MessageSquareText} size="sm" />{/snippet}
    </Button>
  {/if}
{/snippet}

{#snippet emptyLine(text: string)}
  <p class="empty-line">{text}</p>
{/snippet}

<div class="overview" class:wide>
  <Screen title="Overview" {subtitle} actions={overviewRoom && !wide ? summaryLink : undefined} class="main">
    <GettingStarted />

    {#if error}
      <div class="alert">
        <Notice tone="danger" role="alert"><p>{error} <Link onclick={() => load(false)} type="inherit" color="inherit" hasUnderline>Retry</Link></p></Notice>
      </div>
    {/if}
    {#if loading}
      <!-- The text says what is happening; the spinner's own "Loading" status would repeat it. -->
      <p class="loading" aria-busy="true"><Spinner size="sm" aria-hidden="true" /><Text type="supporting">Gathering what changed…</Text></p>
    {:else if ov}
      <ScreenSection title="Since you were here" id="ov-catchup" class="ov-first">
        {#snippet end()}<Text type="supporting">What changed, with current open work below. Conversations stay unread.</Text>{/snippet}
        {#if ov.catchup.length === 0}
          {@render emptyLine('Nothing new since your last visit.')}
        {:else}
          <ul class="catchup">
            {#each ov.catchup as c (c.eventSeq + c.kind)}
              {@const h = catchupHref(c)}
              <li>
                <span class="kind tone-{catchupTone(c.kind)}"><StateIcon shape={catchupShape(c.kind)} tone={catchupTone(c.kind)} />{catchupKindLabel(c.kind)}</span>
                <div class="c-body">
                  <Text as="p" display="block" weight="semibold">{c.title}</Text>
                  {#if c.detail}<Text as="p" display="block">{c.detail}</Text>{/if}
                  <Text as="p" display="block" type="supporting">
                    <time datetime={c.at} title={fullTime(c.at)}>{relative(c.at, app.now)}</time>
                    {#if h && c.roomId}· <Link href={h} type="inherit" hasUnderline>in {app.data.rooms[c.roomId]?.name ?? 'the conversation'}</Link>{/if}
                    {#each c.refs ?? [] as ref}
                      {#if ref.kind === 'job' || ref.kind === 'review' || ref.kind === 'decision'}
                        {' · '}<Link type="inherit" hasUnderline onclick={() => app.openPanel({ kind: ref.kind as 'job' | 'review' | 'decision', id: ref.id })}>{ref.kind === 'job' ? 'open the work' : `view ${ref.kind}`}</Link>
                      {:else if ref.kind === 'question' && app.data.questions[ref.id]}
                        {@const question = app.data.questions[ref.id]}
                        {' · '}<Link href={conversationHref({ ...question.source, messageId: question.messageId })} type="inherit" hasUnderline>view question</Link>
                      {:else if ref.kind === 'approval'}
                        {@const work = c.refs?.find((r) => r.kind === 'job')}
                        {#if work}{' · '}<Link type="inherit" hasUnderline onclick={() => app.openPanel({ kind: 'job', id: work.id })}>view permission request</Link>{/if}
                      {/if}
                    {/each}
                  </Text>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </ScreenSection>

      <ScreenSection title="Recently completed" id="ov-done">
        {#snippet end()}<Text type="supporting">last 7 days</Text>{/snippet}
        {#if done.length === 0}
          {@render emptyLine('Nothing completed recently.')}
        {:else}
          <ul class="rows">{#each done.slice(0, 12) as r (r.job.id)}<WorkRowItem {...r} />{/each}</ul>
        {/if}
      </ScreenSection>

      <ScreenSection title="Active" id="ov-active">
        {#snippet end()}<Text type="supporting">{active.length || ''}</Text>{/snippet}
        {#if active.length === 0}
          {@render emptyLine('No work in progress. Ask an engineer in a room to start something.')}
        {:else}
          <ul class="rows">{#each active as r (r.job.id)}<WorkRowItem {...r} />{/each}</ul>
        {/if}
      </ScreenSection>

      <ScreenSection title="Needs a look" id="ov-needs">
        {#snippet end()}<Text type="supporting">{needs.length || ''}</Text>{/snippet}
        {#if needs.length === 0}
          {@render emptyLine('Nothing is blocked or failed.')}
        {:else}
          <ul class="rows">
            {#each needs as r (r.job.id)}<WorkRowItem {...r} unknownOutcome={unknownJobs.has(r.job.id)} />{/each}
          </ul>
        {/if}
      </ScreenSection>

      <ScreenSection title="Worth remembering" id="ov-dec">
        {#if ov.decisions.length === 0}
          {@render emptyLine('No accepted decisions yet. Engineers record them with their sources as work is reviewed.')}
        {:else}
          <List density="compact">
            {#each ov.decisions as d (d.id)}
              <!-- A snippet label wraps; a string label would be clipped to one line. -->
              <ListItem onclick={() => app.openPanel({ kind: 'decision', id: d.id })}>
                {#snippet label()}{d.title}{/snippet}
                {#snippet startContent()}<Icon icon={Book} size="sm" color="secondary" />{/snippet}
                {#snippet description()}
                  <Text type="supporting">
                    {d.scope.kind === 'project' ? app.data.projects[d.scope.id]?.name : d.scope.kind === 'room' ? app.data.rooms[d.scope.id]?.name : 'Workspace'} ·
                    {d.sources.length} {d.sources.length === 1 ? 'source' : 'sources'} · {relative(d.acceptedAt ?? d.createdAt, app.now)}
                  </Text>
                {/snippet}
              </ListItem>
            {/each}
          </List>
        {/if}
      </ScreenSection>
    {/if}
  </Screen>

  {#if wide && overviewRoom}
    <aside class="convo" aria-labelledby="ov-convo">
      <header class="convo-head">
        <Heading level={2} id="ov-convo">Workspace summary</Heading>
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
  .overview > :global(.main) {
    flex: 1;
    min-width: 0;
    container-type: inline-size;
    container-name: overview-main;
  }
  .overview :global(.ov-first) {
    margin-top: 0;
  }
  .alert {
    margin-bottom: var(--spacing-6);
  }
  .loading {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
  }
  .empty-line {
    color: var(--color-text-secondary);
    padding: var(--spacing-1-5) 0;
  }
  .catchup li {
    display: grid;
    grid-template-columns: 140px minmax(0, 1fr);
    gap: var(--spacing-3);
    padding: var(--spacing-2) var(--spacing-2);
    border-top: 1px solid var(--color-border);
  }
  .catchup li:first-child {
    border-top: 0;
  }
  .kind {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    height: 22px;
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
  }
  .rows {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    padding: var(--spacing-0-5) var(--spacing-1-5);
  }
  .convo {
    width: 400px;
    flex: none;
    display: flex;
    flex-direction: column;
    min-height: 0;
    border-left: 1px solid var(--color-border);
  }
  .convo-head {
    padding: var(--spacing-4) var(--spacing-5) var(--spacing-2);
    border-bottom: 1px solid var(--color-border);
  }
  .convo-head :global(h2) {
    font-size: var(--font-size-lg);
  }
  @container overview-main (max-width: 560px) {
    .catchup li {
      grid-template-columns: 1fr;
      gap: var(--spacing-0-5);
    }
  }
</style>
