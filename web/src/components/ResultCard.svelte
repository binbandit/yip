<script lang="ts">
  // A finished piece of work, with its evidence adjacent to the claim: what
  // changed, the checks that ran on the exact revision, who reviewed which
  // revision, the PR's separate facts, and where it ran.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { jobShape, jobStateLabel, jobTone, reviewShape, reviewTone, verdictPhrase } from '../lib/util/labels';
  import { shortSha } from '../lib/util/time';
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

  function roundFor(r: import('../lib/api/types.gen').Review) {
    const rounds = [...r.rounds].sort((a, b) => a.number - b.number);
    return { current: rounds[rounds.length - 1], earlier: rounds.slice(0, -1) };
  }

  function inspect(tab = 'evidence') {
    app.openPanel({ kind: 'job', id: jobId }, tab);
  }
</script>

<section class="result panel-box" aria-label="Result: {job?.title ?? 'work'}">
  {#if !job}
    <p class="meta pad">{entry?.missing ? 'This work is no longer available to you.' : 'Loading the result…'}</p>
  {:else}
    <header class="pad head">
      <span class="state tone-{jobTone(job.state)}"><StateIcon shape={jobShape(job.state)} tone={jobTone(job.state)} />{jobStateLabel(job)}</span>
      <span class="title truncate">{job.title}</span>
    </header>

    {#if d?.missing.length}
      <div class="pad">
        <div class="notice attention">
          <Icon name="alert" size={16} />
          <div>
            {#each d.missing as m (m)}<p>{m}</p>{/each}
          </div>
        </div>
      </div>
    {/if}

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
                      <span class="mono">{shortSha(rr.current.target.head) || 'the work'}</span></span
                    >
                    {#each rr.earlier as e (e.id)}
                      <span class="meta">· {verdictPhrase(e.state)} <span class="mono">{shortSha(e.target.head)}</span> first</span>
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

    <footer class="pad foot">
      <button class="btn btn-sm" onclick={() => inspect('evidence')}><Icon name="eye" size={15} />Inspect the work</button>
      {#if d?.reviews.length}
        <button class="btn btn-sm btn-quiet" onclick={() => inspect('review')}>View review</button>
      {/if}
      {#if job.requiresHumanReview && job.state === 'review_ready'}
        <span class="meta">Your review is required before this completes.</span>
      {/if}
    </footer>
  {/if}
</section>

<style>
  .result {
    margin-top: 8px;
    max-width: 640px;
    overflow: hidden;
  }
  .pad {
    padding: 10px 14px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 10px;
    border-bottom: 1px solid var(--line);
    background: var(--surface-subtle);
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 650;
    flex: none;
  }
  .title {
    font-weight: 600;
    font-size: 14px;
  }
  .facts {
    margin: 0;
    padding: 4px 14px;
  }
  .fact {
    display: grid;
    grid-template-columns: 96px minmax(0, 1fr);
    gap: 12px;
    padding: 7px 0;
    font-size: 14px;
  }
  .fact + .fact {
    border-top: 1px solid var(--line-soft);
  }
  dt {
    color: var(--ink-secondary);
    font-size: 13px;
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
  .foot {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    border-top: 1px solid var(--line);
  }
  @media (max-width: 480px) {
    .fact {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }
</style>
