<script lang="ts">
  // A narrow strip of the room's live work: state word + shape, objective,
  // owner and reviewer handoff, last confirmed activity, and the machine.
  // Each row opens the job drawer; "Add to" scopes the composer to that job.
  import { untrack } from 'svelte';
  import { Button, Icon } from '@astryx-svelte/core';
  import { ChevronDown, ChevronRight } from '@lucide/svelte';
  import { details } from '../lib/state/details.svelte';
  import { currentReviewRound } from '../lib/util/reviews';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { isLiveJob, jobRunState, roomWorkJobs } from '../lib/state/data';
  import { jobShape, jobStateLabel, jobTone, runStateNote, waitingReasonLabel } from '../lib/util/labels';
  import { relative } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    roomId: string;
  }
  let { roomId }: Props = $props();

  // Only work that is still going (or failed) sits above the conversation;
  // finished work is announced by its result card in the conversation itself.
  const jobs = $derived(roomWorkJobs(app.data, roomId, app.now).filter((j) => j.state !== 'completed'));
  let expanded = $state(false);
  const visible = $derived(expanded ? jobs : jobs.slice(0, 3));
  const scoped = $derived(app.steer[receiptKey(roomId)] ?? null);

  $effect(() => {
    const work = visible.map((j) => ({ id: j.id, touch: app.data.touched.jobs[j.id] ?? 0, reviewers: j.reviewerIds.length }));
    untrack(() => {
      for (const j of work) if (j.reviewers) details.ensureJob(j.id, j.touch);
    });
  });

  function handoff(j: (typeof jobs)[number]): string {
    const owner = app.engineerName(j.ownerId);
    const reviewers = j.reviewerIds.map((id) => {
      const name = app.engineerName(id);
      const review = Object.values(app.data.reviews).find((r) => r.jobId === j.id && r.reviewerId === id);
      if (!review) return `${name} · review status unavailable`;
      const round = currentReviewRound(j, review, Object.values(app.data.artifacts));
      if (!round) return `${name} · review on an earlier revision`;
      switch (round.state) {
        case 'requested':
        case 'queued': return `${name} · review requested`;
        case 'reviewing': return `${name} reviewing`;
        case 'approved': return `${name} approved`;
        case 'changes_requested': return `${name} requested changes`;
        case 'comments_only': return `${name} commented`;
        case 'unable_to_review': return `${name} couldn't review`;
        case 'cancelled': return `${name} · review cancelled`;
      }
    });
    const verb =
      j.state === 'running' ? (j.kind === 'code' ? 'building' : 'working') : j.state === 'queued' ? 'up next' : j.state === 'waiting' ? 'waiting' : '';
    let s = verb ? `${owner} ${verb}` : owner;
    if (reviewers.length) {
      s += ` · ${reviewers.join(', ')}`;
    }
    return s;
  }

  function steer(id: string) {
    const k = receiptKey(roomId);
    app.steer[k] = app.steer[k] === id ? null : id;
    delete app.receipts[k];
    queueMicrotask(() => document.querySelector<HTMLTextAreaElement>('.room-composer textarea')?.focus());
  }
</script>

{#if jobs.length}
  <section class="strip" aria-label="Work in this room">
    <ul>
      {#each visible as j (j.id)}
        {@const live = isLiveJob(j)}
        {@const rs = jobRunState(app.data, j)}
        {@const unconfirmed = rs === 'unknown'}
        {@const note = runStateNote(rs)}
        <li class="row" class:scoped={scoped === j.id}>
          <button
            class="open"
            onclick={() => app.openPanel({ kind: 'job', id: j.id })}
            aria-label="{j.title}: {unconfirmed ? 'outcome not confirmed' : jobStateLabel(j)}. Open details"
          >
            <span class="state tone-{unconfirmed ? 'attention' : jobTone(j.state)}">
              <StateIcon shape={unconfirmed ? 'question' : jobShape(j.state)} tone={unconfirmed ? 'attention' : jobTone(j.state)} live={j.state === 'running' && !unconfirmed} />
              <span class="word">{unconfirmed ? 'Not confirmed' : j.state === 'waiting' ? waitingReasonLabel(j.waitingReason) : jobStateLabel(j)}</span>
            </span>
            <span class="title truncate">{j.title}</span>
            <span class="who truncate" title={handoff(j)}>{handoff(j)}</span>
            <span class="last truncate">
              <!-- The machine comes first so truncation never hides where work runs. -->
              {#if j.nodeId && app.nodeName(j.nodeId)}<span class="where">{j.state === 'running' ? 'Running on' : 'On'} {app.nodeName(j.nodeId)} ·</span>{' '}{/if}
              {#if unconfirmed}
                {app.nodeName(j.nodeId) || 'The machine'} stopped reporting; the last attempt's outcome is not confirmed{j.lastActivity ? ` — last confirmed: ${j.lastActivity.toLowerCase()}` : ''}
              {:else if note && live}
                {note}{#if j.lastActivity}{' · '}{j.lastActivity}{/if}
              {:else if (j.state === 'waiting' || j.state === 'failed') && j.stateDetail}
                {j.stateDetail}
              {:else if j.lastActivity}
                {j.lastActivity}{#if j.lastActivityAt}{' '}<span class="when">· {relative(j.lastActivityAt, app.now)}</span>{/if}
              {/if}

            </span>
          </button>
          {#if live}
            <Button
              class="steer"
              label={scoped === j.id ? 'Adding to this' : 'Add to this'}
              tooltip="Send your next message to this work"
              variant="ghost"
              size="sm"
              aria-pressed={scoped === j.id}
              onclick={() => steer(j.id)}
            />
          {/if}
        </li>
      {/each}
    </ul>
    {#if jobs.length > 3}
      <Button
        class="more"
        label={expanded ? 'Show less' : `Show all ${jobs.length}`}
        variant="ghost"
        size="sm"
        aria-expanded={expanded}
        onclick={() => (expanded = !expanded)}
      >
        {#snippet endContent()}<Icon icon={expanded ? ChevronDown : ChevronRight} size="sm" />{/snippet}
      </Button>
    {/if}
  </section>
{/if}

<style>
  .strip {
    flex: none;
    padding: var(--spacing-1) var(--spacing-2);
    border-bottom: 1px solid var(--color-border);
    background: var(--color-background-surface);
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--spacing-1);
    border-radius: var(--radius-element);
  }
  .row.scoped {
    background: var(--color-accent-muted);
  }
  .open {
    flex: 1;
    min-width: 0;
    display: grid;
    grid-template-columns: auto minmax(120px, 1.2fr) minmax(90px, 1fr) minmax(0, 1.4fr);
    align-items: center;
    gap: var(--spacing-3);
    min-height: 36px;
    padding: var(--spacing-1) var(--spacing-2);
    border-radius: var(--radius-element);
    color: var(--color-text-primary);
    font-size: var(--text-supporting-size);
    line-height: var(--text-supporting-leading);
    text-align: left;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .open:hover {
    background: var(--color-overlay-hover);
  }
  .open:focus-visible {
    outline: var(--focus-outline-width) var(--focus-outline-style) var(--focus-outline-color);
    outline-offset: -2px;
  }
  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    font-weight: var(--font-weight-semibold);
    white-space: nowrap;
  }
  .title {
    font-size: var(--text-body-size);
    font-weight: var(--font-weight-semibold);
  }
  .who,
  .last,
  .when {
    color: var(--color-text-secondary);
  }
  .where {
    color: var(--color-text-primary);
  }
  .row :global(.steer) {
    flex: none;
  }
  /* The row's wash already marks the selected work; the pressed button only firms up. */
  .row :global(.steer[aria-pressed='true']) {
    font-weight: var(--font-weight-semibold);
  }
  .strip :global(.more) {
    margin: var(--spacing-0-5) var(--spacing-1) var(--spacing-1);
  }
  @container room (max-width: 860px) {
    .open {
      grid-template-columns: auto minmax(0, 1fr);
      grid-template-areas:
        'state title'
        'who last';
      gap: 1px var(--spacing-3);
      padding: var(--spacing-1-5) var(--spacing-2);
    }
    .state {
      grid-area: state;
    }
    .title {
      grid-area: title;
    }
    .who {
      grid-area: who;
    }
    .last {
      grid-area: last;
    }
  }
  @container room (max-width: 520px) {
    .open {
      grid-template-areas:
        'state title'
        'who who'
        'last last';
    }
  }
</style>
