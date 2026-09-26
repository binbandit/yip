<script lang="ts">
  // A narrow strip of the room's live work: state word + shape, objective,
  // owner and reviewer handoff, last confirmed activity, and the machine.
  // Each row opens the job drawer; "Add to" scopes the composer to that job.
  import { untrack } from 'svelte';
  import { details } from '../lib/state/details.svelte';
  import { currentReviewRound } from '../lib/util/reviews';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { isLiveJob, jobRunState, roomWorkJobs } from '../lib/state/data';
  import { jobShape, jobStateLabel, jobTone, runStateNote, waitingReasonLabel } from '../lib/util/labels';
  import { relative } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';
  import Icon from './Icon.svelte';

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
            <button
              class="btn btn-sm btn-quiet steer"
              aria-pressed={scoped === j.id}
              onclick={() => steer(j.id)}
              title="Send your next message to this work"
            >
              {scoped === j.id ? 'Adding to this' : 'Add to this'}
            </button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if jobs.length > 3}
      <button class="more link-btn" onclick={() => (expanded = !expanded)} aria-expanded={expanded}>
        {expanded ? 'Show less' : `Show all ${jobs.length}`}
        <Icon name={expanded ? 'chevronDown' : 'chevronRight'} size={14} />
      </button>
    {/if}
  </section>
{/if}

<style>
  .where {
    color: var(--ink);
  }
  .strip {
    flex: none;
    border-bottom: 1px solid color-mix(in srgb, var(--line) 80%, transparent);
    padding: 4px 8px;
    background: var(--surface);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 4px;
    border-radius: 10px;
  }
  .row.scoped {
    background: var(--accent-subtle);
  }
  .open {
    flex: 1;
    min-width: 0;
    display: grid;
    grid-template-columns: auto minmax(120px, 1.2fr) minmax(90px, 1fr) minmax(0, 1.4fr);
    align-items: center;
    gap: 12px;
    min-height: 36px;
    padding: 4px 10px;
    border: 0;
    border-radius: var(--r-row);
    background: none;
    color: var(--ink);
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  .open:hover {
    background: var(--hover);
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
  }
  .title {
    font-weight: 600;
  }
  .who,
  .last {
    font-size: 12px;
    color: var(--ink-secondary);
  }
  .when {
    color: var(--ink-secondary);
  }
  .steer {
    flex: none;
  }
  .steer[aria-pressed='true'] {
    color: var(--ink);
    font-weight: 600;
  }
  .more {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin: 2px 8px 4px;
    font-size: 13px;
  }
  @container room (max-width: 860px) {
    .open {
      grid-template-columns: auto minmax(0, 1fr);
      grid-template-areas:
        'state title'
        'who last';
      gap: 1px 12px;
      padding: 6px 10px;
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
