<script lang="ts">
  // A finished piece of work, posted like an attachment in a chat: one line
  // that says what changed, whether the checks on the exact revision passed,
  // and who approved which revision. The full evidence (revision, every
  // check, earlier verdicts, the PR's separate facts, the machine) is one
  // click away under Details, and anything missing is always shown.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { jobShape, jobStateLabel, jobTone, reviewShape, reviewTone, verdictPhrase } from '../lib/util/labels';
  import { currentReviewRound } from '../lib/util/reviews';
  import { shortSha } from '../lib/util/time';
  import type { Review } from '../lib/api/types.gen';
  import StateIcon from './StateIcon.svelte';
  import Icon from './Icon.svelte';
  import PRFacts from './PRFacts.svelte';

  interface Props {
    jobId: string;
  }
  let { jobId }: Props = $props();

  $effect(() => {
    const t = app.data.touched.jobs[jobId] ?? 0;
    untrack(() => details.ensureJob(jobId, t));
  });

  const entry = $derived(details.jobs[jobId]);
  const d = $derived(entry?.data);
  const job = $derived(app.data.jobs[jobId] ?? d?.job);
  const head = $derived(job?.revision?.head ?? '');
  // Revision stats come from the hub's revision records (no diff download here).
  const revision = $derived(d ? (d.revisions.find((r) => r.head === head) ?? d.revisions[d.revisions.length - 1]) : undefined);
  const earlierRevisions = $derived(d && revision ? d.revisions.filter((r) => r.head !== revision.head).length : 0);
  const checks = $derived(d ? d.checks.filter((c) => !head || c.revision === head) : []);
  const earlierChecks = $derived(d ? d.checks.length - checks.length : 0);
  const docs = $derived(d ? d.artifacts.filter((a) => a.kind === 'document' || a.kind === 'file') : []);

  function roundFor(r: Review) {
    const rounds = [...r.rounds].sort((a, b) => a.number - b.number);
    return { current: rounds[rounds.length - 1], earlier: rounds.slice(0, -1) };
  }

  function inspect(tab = 'evidence') {
    app.openPanel({ kind: 'job', id: jobId }, tab);
  }

  let open = $state(false);
  const uid = `rc-${Math.random().toString(36).slice(2, 8)}`;
  const failed = $derived(checks.filter((c) => !c.passed));
  const verdicts = $derived(
    (d?.reviews ?? [])
      .map((r) => ({ r, cur: roundFor(r).current }))
      // A withdrawn or superseded request is not a verdict.
      .filter((x) => x.cur && x.cur.state !== 'cancelled')
      .map(({ r, cur }) => {
        const who = app.engineerName(r.reviewerId);
        const id = r.id;
        if (job && !currentReviewRound(job, r, d?.artifacts ?? [])) return { id, text: `${who}'s review applies to an earlier version`, tone: 'neutral' };
        switch (cur!.state) {
          case 'approved':
            return { id, text: `approved by ${who}`, tone: 'success' };
          case 'changes_requested':
            return { id, text: `changes requested by ${who}`, tone: 'neutral' };
          case 'comments_only':
            return { id, text: `comments from ${who}`, tone: 'neutral' };
          case 'unable_to_review':
            return { id, text: `${who} couldn't review`, tone: 'attention' };
          default:
            return { id, text: `${who} reviewing`, tone: 'neutral' };
        }
      }),
  );
</script>

<section class="result" aria-label="Result: {job?.title ?? 'work'}">
  {#if !job}
    <p class="meta">{entry?.missing ? 'This work is no longer available to you.' : 'Loading the result…'}</p>
  {:else}
    <header class="head">
      <StateIcon shape={jobShape(job.state)} tone={jobTone(job.state)} />
      <span class="title truncate">{job.title}</span>
      <span class="state tone-{jobTone(job.state)}">{jobStateLabel(job)}</span>
    </header>

    {#if d}
      <p class="summary">
        <!-- Each claim opens its evidence (§8: evidence adjacent to the claim). -->
        {#if revision}
          <button class="item claim" title="View the diff" onclick={() => inspect('evidence')}
            >{revision.filesChanged} {revision.filesChanged === 1 ? 'file' : 'files'} <span class="add">+{revision.insertions}</span> <span class="del">−{revision.deletions}</span></button
          >
        {/if}
        {#if checks.length === 1}
          <button class="item claim" title="View the command, exit status and log" onclick={() => inspect('evidence')}
            ><span class="mono">{checks[0].command}</span> <span class={checks[0].passed ? 'tone-success' : 'tone-danger'}>{checks[0].passed ? 'passed' : 'failed'}</span></button
          >
        {:else if checks.length}
          <button class="item claim {failed.length ? 'tone-danger' : 'tone-success'}" title="View the checks and their logs" onclick={() => inspect('evidence')}
            >{failed.length ? `${failed.length} of ${checks.length} checks failed` : `${checks.length} checks passed`}</button
          >
        {/if}
        {#each verdicts as v (v.id)}
          <button class="item claim tone-{v.tone}" title="View the review" onclick={() => app.openPanel({ kind: 'review', id: v.id })}>{v.text}</button>
        {/each}
        {#if docs.length}
          <button class="item claim" title="View the files" onclick={() => inspect('evidence')}>{docs.length} {docs.length === 1 ? 'document' : 'documents'}</button>
        {/if}
        {#if d.pullRequests.length}
          <button class="item claim" title="View the pull request's facts" onclick={() => app.openPanel({ kind: 'pr', id: d.pullRequests[0].id })}>PR #{d.pullRequests[0].number}</button>
        {/if}
      </p>
    {/if}

    {#if d?.missing.length}
      <p class="notice attention">Still needs {d.missing.join('; ')}.</p>
    {/if}
    {#if job.requiresHumanReview && job.state === 'review_ready'}
      <p class="needs">Your review is required before this completes.</p>
    {/if}

    <footer class="foot">
      <button class="btn btn-sm" onclick={() => inspect('evidence')}><Icon name="eye" size={15} />Inspect the work</button>
      {#if d?.reviews.length}
        <button class="btn btn-sm btn-quiet" onclick={() => inspect('review')}>View review</button>
      {/if}
      <button class="btn btn-sm btn-quiet more" aria-expanded={open} aria-controls={uid} onclick={() => (open = !open)}>
        Details<Icon name={open ? 'chevronDown' : 'chevronRight'} size={14} />
      </button>
    </footer>

    <div id={uid} hidden={!open}>
    <dl class="facts">
      {#if revision}
        <div class="fact">
          <dt>Changed</dt>
          <dd>
            <span>{revision.filesChanged} {revision.filesChanged === 1 ? 'file' : 'files'}</span>
            <span class="add">+{revision.insertions}</span> <span class="del">−{revision.deletions}</span>
            {#if revision.summary}<p class="rev-summary">{revision.summary}</p>{/if}
            {#if earlierRevisions}<span class="meta">after {earlierRevisions} earlier {earlierRevisions === 1 ? 'revision' : 'revisions'}</span>{/if}
          </dd>
        </div>
      {/if}
      {#if d && (checks.length || head)}
        <div class="fact">
          <dt>Checks</dt>
          <dd>
            {#if checks.length === 0}
              <span class="meta">No checks recorded on this revision.</span>
            {:else}
              <ul class="checks">
                {#each checks as c (c.id)}
                  <li>
                    <button class="linkish" onclick={() => inspect('evidence')}>
                      <StateIcon shape={c.passed ? 'check-filled' : 'triangle'} tone={c.passed ? 'success' : 'danger'} size={13} />
                      <span class="mono">{c.command}</span>
                      <span class={c.passed ? 'tone-success' : 'tone-danger'}>{c.passed ? 'passed' : `failed (exit ${c.exitCode})`}</span>
                    </button>
                  </li>
                {/each}
              </ul>
              {#if earlierChecks}<span class="meta">{earlierChecks} earlier {earlierChecks === 1 ? 'run' : 'runs'} on previous revisions</span>{/if}
            {/if}
          </dd>
        </div>
      {/if}
      {#if d?.reviews.length}
        <div class="fact">
          <dt>Review</dt>
          <dd>
            <ul class="reviews">
              {#each d.reviews as r (r.id)}
                {@const rr = roundFor(r)}
                <li>
                  {#if rr.current}
                    <StateIcon shape={reviewShape(rr.current.state)} tone={reviewTone(rr.current.state)} size={13} />
                    <span
                      >{app.engineerName(r.reviewerId)} {verdictPhrase(rr.current.state)}
                      <span class="mono">{shortSha(rr.current.target.head || rr.current.target.hash) || 'the work'}</span></span
                    >
                    {#each rr.earlier as e (e.id)}
                      <span class="meta">· {verdictPhrase(e.state)} <span class="mono">{shortSha(e.target.head || e.target.hash)}</span> first</span>
                    {/each}
                  {:else}
                    <span>{app.engineerName(r.reviewerId)} was asked to review</span>
                  {/if}
                </li>
              {/each}
            </ul>
          </dd>
        </div>
      {/if}
      {#if job.revision?.head}
        <div class="fact">
          <dt>Revision</dt>
          <dd><span class="mono">{shortSha(job.revision.head)}</span>{#if job.revision.branch}{' '}<span class="meta">on {job.revision.branch}</span>{/if}</dd>
        </div>
      {/if}
      {#if docs.length}
        <div class="fact">
          <dt>Documents</dt>
          <dd>
            <ul class="files">
              {#each docs as a (a.id)}<li><a href="/v1/artifacts/{a.id}" target="_blank" rel="noopener">{a.name}</a></li>{/each}
            </ul>
          </dd>
        </div>
      {/if}
      {#if d?.pullRequests.length}
        <div class="fact">
          <dt>Pull request</dt>
          <dd>
            {#each d.pullRequests as pr (pr.id)}<PRFacts {pr} compact />{/each}
          </dd>
        </div>
      {/if}
      {#if job.nodeId}
        <div class="fact">
          <dt>Ran on</dt>
          <dd>{app.nodeName(job.nodeId) || 'a paired machine'}</dd>
        </div>
      {/if}
        </dl>
    </div>
  {/if}
</section>

<style>
  .result {
    display: grid;
    gap: 6px;
    margin-top: 6px;
    max-width: 560px;
    padding: 10px 12px;
    border: 1px solid var(--line-strong);
    border-radius: var(--r-artifact);
    background: var(--surface);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .title {
    flex: 1;
    font-weight: 600;
    font-size: 14px;
  }
  .state {
    flex: none;
    font-size: 12px;
    font-weight: 500;
  }
  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 0;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .summary .item::before {
    display: inline-block;
    content: '·';
    margin: 0 7px;
    color: color-mix(in srgb, var(--ink) 35%, transparent);
  }
  .summary > :first-child::before {
    content: none;
  }
  .claim {
    padding: 0;
    border: 0;
    background: none;
    font: inherit;
    color: inherit;
    text-align: left;
    cursor: pointer;
    text-decoration: underline;
    text-decoration-color: transparent;
    text-underline-offset: 3px;
    transition: text-decoration-color var(--t-fast) var(--ease);
  }
  .claim:hover,
  .claim:focus-visible {
    text-decoration-color: currentColor;
  }
  .needs {
    font-size: 13px;
    color: var(--attention-ink);
  }
  .foot {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
    margin: 2px -4px 0;
  }
  .more {
    margin-left: auto;
    color: var(--ink-secondary);
  }
  .facts {
    margin: 4px 0 0;
    padding: 4px 0 0;
    border-top: 1px solid var(--line);
  }
  .fact {
    display: grid;
    grid-template-columns: 88px minmax(0, 1fr);
    gap: 12px;
    padding: 6px 0;
    font-size: 13px;
  }
  .fact + .fact {
    border-top: 1px solid var(--line-soft);
  }
  dt {
    color: var(--ink-secondary);
    font-size: 12px;
    padding-top: 1px;
  }
  dd {
    margin: 0;
    min-width: 0;
  }
  .files,
  .checks,
  .reviews {
    list-style: none;
    margin: 2px 0 0;
    padding: 0;
    display: grid;
    gap: 3px;
  }
  .reviews li,
  .checks li {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }
  .rev-summary {
    margin-top: 2px;
  }
  .add {
    color: var(--success);
    font-weight: 600;
  }
  .del {
    color: var(--danger);
    font-weight: 600;
  }
  .linkish {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    padding: 0;
    border: 0;
    background: none;
    text-align: left;
    cursor: pointer;
    color: inherit;
  }
  .linkish:hover .mono {
    text-decoration: underline;
  }
  @media (max-width: 480px) {
    .fact {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }
</style>
