<script lang="ts">
  // A narrow strip of the room's live work: state word + shape, objective,
  // owner and reviewer handoff, last confirmed activity, and the machine.
  // Each row opens the job drawer; "Add to" scopes the composer to that job.
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { isLiveJob, roomWorkJobs } from '../lib/state/data';
  import { jobShape, jobStateLabel, jobTone, waitingReasonLabel } from '../lib/util/labels';
  import { relative } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    roomId: string;
  }
  let { roomId }: Props = $props();

  const jobs = $derived(roomWorkJobs(app.data, roomId, app.now));
  let expanded = $state(false);
  const visible = $derived(expanded ? jobs : jobs.slice(0, 3));
  const scoped = $derived(app.steer[receiptKey(roomId)] ?? null);

  function handoff(j: (typeof jobs)[number]): string {
    const owner = app.engineerName(j.ownerId);
    const reviewers = j.reviewerIds.map((id) => app.engineerName(id));
    const verb =
      j.state === 'running' ? (j.kind === 'code' ? 'building' : 'working') : j.state === 'queued' ? 'up next' : j.state === 'waiting' ? 'waiting' : '';
    let s = verb ? `${owner} ${verb}` : owner;
    if (reviewers.length) {
      const rv = j.state === 'completed' || j.state === 'review_ready' ? 'reviewed' : 'reviewing next';
      s += ` · ${reviewers.join(', ')} ${rv}`;
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
        <li class="row" class:scoped={scoped === j.id}>
          <button class="open" onclick={() => app.openPanel({ kind: 'job', id: j.id })} aria-label="{j.title}: {jobStateLabel(j)}. Open details">
            <span class="state tone-{jobTone(j.state)}">
              <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />
              <span class="word">{j.state === 'waiting' ? waitingReasonLabel(j.waitingReason) : jobStateLabel(j)}</span>
            </span>
            <span class="title truncate">{j.title}</span>
            <span class="who truncate">{handoff(j)}</span>
            <span class="last truncate">
              {#if (j.state === 'waiting' || j.state === 'failed') && j.stateDetail}
                {j.stateDetail}
              {:else if j.lastActivity}
                {j.lastActivity}{#if j.lastActivityAt}<span class="when"> · {relative(j.lastActivityAt, app.now)}</span>{/if}
              {/if}
              {#if j.nodeId && app.nodeName(j.nodeId)}<span class="when"> · on {app.nodeName(j.nodeId)}</span>{/if}
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
  .strip {
    flex: none;
    border-bottom: 1px solid var(--line);
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
    min-height: 38px;
    padding: 4px 8px;
    border: 0;
    border-radius: 10px;
    background: none;
    color: var(--ink);
    font-size: 14px;
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
    font-size: 13px;
    font-weight: 650;
    white-space: nowrap;
  }
  .title {
    font-weight: 600;
  }
  .who,
  .last {
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .when {
    color: var(--ink-secondary);
  }
  .steer {
    flex: none;
  }
  .steer[aria-pressed='true'] {
    color: var(--accent);
    font-weight: 650;
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
        'who who'
        'last last';
      gap: 2px 10px;
      padding: 6px 8px;
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
</style>
