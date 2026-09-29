<script lang="ts">
  // One factual ledger row: state, objective, owner, project, last confirmed
  // update with its time, machine, blocker text, and the origin conversation.
  import { Icon, Link } from '@astryx-svelte/core';
  import { Reply } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import type { Job, Question } from '../lib/api/types.gen';
  import { jobShape, jobStateLabel, jobTone, runStateNote, waitingReasonLabel } from '../lib/util/labels';
  import { isLiveJob, jobRunState } from '../lib/state/data';
  import { fullTime, relative } from '../lib/util/time';
  import { conversationHref } from '../lib/util/conversation';
  import { plainText } from '../lib/util/markdown';
  import StateIcon from './StateIcon.svelte';
  import Avatar from './Avatar.svelte';

  interface Props {
    job: Job;
    lastConfirmed?: string;
    lastConfirmedAt?: string | null;
    blocker?: string;
    questions?: Question[];
    /** Overrides the latest attempt's state from the store. */
    unknownOutcome?: boolean;
  }
  let { job, lastConfirmed, lastConfirmedAt, blocker, questions = [], unknownOutcome: forced = false }: Props = $props();
  const runState = $derived(jobRunState(app.data, job));
  const unknownOutcome = $derived(forced || runState === 'unknown');
  const note = $derived(isLiveJob(job) ? runStateNote(runState) : undefined);
  const project = $derived(job.projectId ? app.data.projects[job.projectId] : undefined);
  const room = $derived(app.data.rooms[job.source.roomId]);
  const node = $derived(app.nodeName(job.nodeId));
  const completed = $derived(job.state === 'completed');
  const confirmed = $derived(completed ? '' : job.lastActivity || lastConfirmed || '');
  const confirmedAt = $derived(completed ? job.completedAt : job.lastActivityAt ?? lastConfirmedAt ?? null);
  const blockText = $derived(
    unknownOutcome
      ? `${node || 'The machine'} stopped reporting. The outcome is not yet confirmed.`
      : job.state === 'waiting' || job.state === 'failed' || job.state === 'review_ready'
        ? job.stateDetail || blocker || ''
        : '',
  );
</script>

<li class="row">
  <button class="main" onclick={() => app.openPanel({ kind: 'job', id: job.id })}>
    <span class="state tone-{unknownOutcome ? 'attention' : jobTone(job.state)}">
      <StateIcon shape={unknownOutcome ? 'question' : jobShape(job.state)} tone={unknownOutcome ? 'attention' : jobTone(job.state)} live={job.state === 'running' && !unknownOutcome} />
      <span>{unknownOutcome ? 'Not confirmed' : job.state === 'waiting' ? waitingReasonLabel(job.waitingReason) : jobStateLabel(job)}</span>
    </span>
    <span class="title">{job.title}</span>
    {#if completed && job.summary}<span class="result">{plainText(job.summary)}</span>{/if}
    {#if blockText}<span class="blocker">{blockText}</span>{/if}
    <span class="facts">
      <span class="owner"><Avatar actor={{ kind: 'engineer', id: job.ownerId }} size={20} />{app.engineerName(job.ownerId)}</span>
      {#if project}<span>· {project.name}</span>{/if}
      {#if confirmed}<span>· {confirmed}{#if confirmedAt}<span title={fullTime(confirmedAt)}>, {relative(confirmedAt, app.now)}</span>{/if}</span>{/if}
      {#if completed && confirmedAt}<span>· <time datetime={confirmedAt} title={fullTime(confirmedAt)}>{relative(confirmedAt, app.now)}</time></span>{/if}
      {#if node}<span>· on {node}</span>{/if}
      {#if note && !unknownOutcome}<span>· {note}</span>{/if}
    </span>
  </button>
  {#if room}
    <Link class="origin" href={conversationHref(job.source)} label="Open the conversation in {room.name}" type="supporting" color="inherit">
      {room.kind === 'dm' ? 'Direct' : room.name}
    </Link>
  {/if}
  {#if questions.length}
    <div class="questions">
      {#each questions as question (question.id)}
        <Link href={conversationHref({ ...question.source, messageId: question.messageId })} color="primary">
          <span class="question">
            <span class="shape"><Icon icon={Reply} size="sm" color="warning" /></span>
            <span>{app.engineerName(question.askerId)} asks: {question.missingFact}<span class="answer">Answer in conversation</span></span>
          </span>
        </Link>
      {/each}
    </div>
  {/if}
</li>

<style>
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: var(--spacing-2);
    border-top: 1px solid var(--color-border);
  }
  .row:first-child {
    border-top: 0;
  }
  .main {
    flex: 1;
    min-width: 0;
    display: grid;
    grid-template-columns: 170px minmax(0, 1fr);
    grid-template-areas:
      'state title'
      '. result'
      '. blocker'
      '. facts';
    gap: var(--spacing-0-5) var(--spacing-3);
    padding: calc(var(--spacing-2) + var(--spacing-0-5)) var(--spacing-2);
    border-radius: var(--radius-element);
    color: var(--color-text-primary);
    font-size: var(--text-body-size);
    line-height: var(--text-body-leading);
    text-align: left;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .main:hover {
    background: var(--color-overlay-hover);
  }
  .main:focus-visible {
    outline: var(--focus-outline-width) var(--focus-outline-style) var(--focus-outline-color);
    outline-offset: -2px;
  }
  .state {
    grid-area: state;
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    font-size: var(--text-supporting-size);
    font-weight: var(--font-weight-semibold);
  }
  .title {
    grid-area: title;
    font-weight: var(--font-weight-semibold);
  }
  .result {
    grid-area: result;
    color: var(--color-text-secondary);
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    overflow: hidden;
  }
  .blocker {
    grid-area: blocker;
  }
  .facts {
    grid-area: facts;
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-0-5) var(--spacing-1-5);
    font-size: var(--text-supporting-size);
    color: var(--color-text-secondary);
  }
  .owner {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1);
    color: var(--color-text-primary);
  }
  /* The origin conversation, as a quiet pill link beside the row. */
  .row :global(.origin) {
    flex: none;
    margin-top: var(--spacing-2);
    padding: var(--spacing-0-5) var(--spacing-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-full);
    color: var(--color-text-secondary);
    white-space: nowrap;
    text-decoration: none;
  }
  .row :global(.origin:hover) {
    border-color: var(--color-border-emphasized);
    color: var(--color-text-primary);
  }
  .questions {
    flex-basis: 100%;
    display: grid;
    gap: var(--spacing-1-5);
    padding: 0 var(--spacing-2) var(--spacing-3);
  }
  .questions :global(a) {
    text-decoration: none;
  }
  /* A question asked of you leads with the reply shape, in the attention hue. */
  .question {
    display: flex;
    gap: var(--spacing-2);
  }
  .shape {
    display: inline-flex;
    flex: none;
    margin-top: calc((1lh - 16px) / 2);
  }
  .answer {
    margin-left: var(--spacing-2);
    color: var(--color-text-secondary);
    text-decoration: underline;
    text-underline-offset: 3px;
  }
  @media (max-width: 768px) {
    .main {
      grid-template-columns: minmax(0, 1fr);
      grid-template-areas: 'state' 'title' 'result' 'blocker' 'facts';
    }
  }
</style>
