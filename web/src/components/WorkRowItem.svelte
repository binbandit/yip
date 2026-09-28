<script lang="ts">
  // One factual ledger row: state, objective, owner, project, last confirmed
  // update with its time, machine, blocker text, and the origin conversation.
  import { app } from '../lib/state/app.svelte';
  import type { Job, Question } from '../lib/api/types.gen';
  import { jobShape, jobStateLabel, jobTone, runStateNote, waitingReasonLabel } from '../lib/util/labels';
  import { isLiveJob, jobRunState } from '../lib/state/data';
  import { fullTime, relative } from '../lib/util/time';
  import { conversationHref } from '../lib/util/conversation';
  import { plainText } from '../lib/util/markdown';
  import StateIcon from './StateIcon.svelte';
  import Avatar from './Avatar.svelte';
  import Icon from './Icon.svelte';

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
      <span class="owner"><Avatar actor={{ kind: 'engineer', id: job.ownerId }} size={18} />{app.engineerName(job.ownerId)}</span>
      {#if project}<span>{project.name}</span>{/if}
      {#if confirmed}<span>{confirmed}{#if confirmedAt}<span title={fullTime(confirmedAt)}>, {relative(confirmedAt, app.now)}</span>{/if}</span>{/if}
      {#if completed && confirmedAt}<span>finished <time datetime={confirmedAt} title={fullTime(confirmedAt)}>{relative(confirmedAt, app.now)}</time></span>{/if}
      {#if node}<span>on {node}</span>{/if}
      {#if note && !unknownOutcome}<span>{note}</span>{/if}
    </span>
  </button>
  {#if room}
    <a class="origin" href={conversationHref(job.source)} aria-label="Open the conversation in {room.name}">
      {room.kind === 'dm' ? 'Direct' : room.name}
    </a>
  {/if}
  {#if questions.length}
    <div class="questions">
      {#each questions as question (question.id)}
        <a href={conversationHref({ ...question.source, messageId: question.messageId })}>
          <Icon name="reply" size={15} />
          <span>{app.engineerName(question.askerId)} asks: {question.missingFact}<span class="answer">Answer in conversation</span></span>
        </a>
      {/each}
    </div>
  {/if}
</li>

<style>
  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 8px;
    border-top: 1px solid var(--line-soft);
  }
  .row:first-child {
    border-top: 0;
  }
  .questions {
    flex-basis: 100%;
    display: grid;
    gap: 6px;
    padding: 0 8px 12px;
  }
  .questions a {
    display: flex;
    gap: 8px;
    font-size: 13.5px;
    text-decoration: none;
  }
  .questions a :global(.icon) {
    margin-top: calc((1lh - 15px) / 2);
    color: var(--attention-ink);
  }
  .answer {
    margin-left: 8px;
    color: var(--ink-secondary);
    text-decoration: underline;
    text-underline-offset: 3px;
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
    gap: 2px 12px;
    padding: 10px 8px;
    border: 0;
    border-radius: 10px;
    background: none;
    color: var(--ink);
    text-align: left;
    cursor: pointer;
    font-size: 14px;
  }
  .main:hover {
    background: var(--hover);
  }
  .state {
    grid-area: state;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 650;
  }
  .title {
    grid-area: title;
    font-weight: 600;
  }
  .blocker {
    grid-area: blocker;
    color: var(--ink);
    font-size: 13.5px;
  }
  .result {
    grid-area: result;
    color: var(--ink-secondary);
    font-size: 13.5px;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    overflow: hidden;
  }
  .facts {
    grid-area: facts;
    display: flex;
    flex-wrap: wrap;
    gap: 2px 6px;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  /* Separators end a line rather than start the next one when facts wrap. */
  .facts > :not(:last-child)::after {
    content: ' ·';
  }
  .owner {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--ink);
  }
  .origin {
    flex: none;
    margin-top: 10px;
    padding: 2px 8px;
    border-radius: var(--r-pill);
    border: 1px solid var(--line);
    font-size: 12.5px;
    text-decoration: none;
    color: var(--ink-secondary);
    white-space: nowrap;
  }
  .origin:hover {
    color: var(--ink);
    border-color: var(--control-edge);
  }
  @media (max-width: 760px) {
    .main {
      grid-template-columns: minmax(0, 1fr);
      grid-template-areas: 'state' 'title' 'result' 'blocker' 'facts';
    }
  }
</style>
