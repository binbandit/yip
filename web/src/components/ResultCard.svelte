<script lang="ts">
  // A finished piece of work, posted like an attachment in a chat: one line
  // that says what changed, whether the checks on the exact revision passed,
  // and who approved which revision. The full evidence (revision, every
  // check, earlier verdicts, the PR's separate facts, the machine) is one
  // click away under Details, and anything missing is always shown.
  import { untrack } from 'svelte';
  import { Button, Code, HStack, Icon, Link, MetadataList, MetadataListItem, Text, Tooltip } from '@astryx-svelte/core';
  import { ChevronDown, ChevronRight, Eye } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { artifactUrl } from '../lib/api/endpoints';
  import { jobShape, jobStateLabel, jobTone, reviewShape, reviewTone, verdictPhrase } from '../lib/util/labels';
  import { currentReviewRound } from '../lib/util/reviews';
  import { shortSha } from '../lib/util/time';
  import type { Review } from '../lib/api/types.gen';
  import StateIcon from './StateIcon.svelte';
  import Notice from './Notice.svelte';
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
      .filter((x) => x.cur)
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
    <Text as="p" type="supporting">{entry?.missing ? 'This work is no longer available to you.' : 'Loading the result…'}</Text>
  {:else}
    <header class="head">
      <StateIcon shape={jobShape(job.state)} tone={jobTone(job.state)} />
      <span class="title truncate">{job.title}</span>
      <span class="state tone-{jobTone(job.state)}">{jobStateLabel(job)}</span>
    </header>

    {#if d}
      <div class="summary">
        <!-- Each claim opens its evidence (§8: evidence adjacent to the claim). -->
        {#if revision}
          <Tooltip content="View the diff">
            <button class="claim" onclick={() => inspect('evidence')}
              >{revision.filesChanged} {revision.filesChanged === 1 ? 'file' : 'files'} <span class="add">+{revision.insertions}</span> <span class="del">−{revision.deletions}</span></button
            >
          </Tooltip>
        {/if}
        {#if checks.length === 1}
          <Tooltip content="View the command, exit status and log">
            <button class="claim" onclick={() => inspect('evidence')}
              ><span class="command">{checks[0].command}</span> <span class={checks[0].passed ? 'tone-success' : 'tone-danger'}>{checks[0].passed ? 'passed' : 'failed'}</span></button
            >
          </Tooltip>
        {:else if checks.length}
          <Tooltip content="View the checks and their logs">
            <button class="claim {failed.length ? 'tone-danger' : 'tone-success'}" onclick={() => inspect('evidence')}
              >{failed.length ? `${failed.length} of ${checks.length} checks failed` : `${checks.length} checks passed`}</button
            >
          </Tooltip>
        {/if}
        {#each verdicts as v (v.id)}
          <Tooltip content="View the review">
            <button class="claim tone-{v.tone}" onclick={() => app.openPanel({ kind: 'review', id: v.id })}>{v.text}</button>
          </Tooltip>
        {/each}
        {#if docs.length}
          <Tooltip content="View the files">
            <button class="claim" onclick={() => inspect('evidence')}>{docs.length} {docs.length === 1 ? 'document' : 'documents'}</button>
          </Tooltip>
        {/if}
        {#if d.pullRequests.length}
          <Tooltip content="View the pull request's facts">
            <button class="claim" onclick={() => app.openPanel({ kind: 'pr', id: d.pullRequests[0].id })}>PR #{d.pullRequests[0].number}</button>
          </Tooltip>
        {/if}
      </div>
    {/if}

    {#if d?.missing.length}
      <Notice tone="warning">Still needs {d.missing.join('; ')}.</Notice>
    {/if}
    {#if job.requiresHumanReview && job.state === 'review_ready'}
      <p class="needs">Your review is required before this completes.</p>
    {/if}

    <HStack as="footer" gap={1} wrap="wrap" align="center">
      <Button label="Inspect the work" size="sm" onclick={() => inspect('evidence')}>
        {#snippet icon()}<Icon icon={Eye} size="sm" />{/snippet}
      </Button>
      {#if d?.reviews.length}
        <Button label="View review" variant="ghost" size="sm" onclick={() => inspect('review')} />
      {/if}
      <Button class="more" label="Details" variant="ghost" size="sm" aria-expanded={open} aria-controls={uid} onclick={() => (open = !open)}>
        {#snippet endContent()}<Icon icon={open ? ChevronDown : ChevronRight} size="sm" />{/snippet}
      </Button>
    </HStack>

    <div id={uid} class="details" hidden={!open}>
      <MetadataList label={{ position: 'start', width: 88 }}>
        {#if revision}
          <MetadataListItem label="Changed">
            <span>{revision.filesChanged} {revision.filesChanged === 1 ? 'file' : 'files'}</span>
            <span class="add">+{revision.insertions}</span> <span class="del">−{revision.deletions}</span>
            {#if revision.summary}<p class="rev-summary">{revision.summary}</p>{/if}
            {#if earlierRevisions}<Text type="supporting">after {earlierRevisions} earlier {earlierRevisions === 1 ? 'revision' : 'revisions'}</Text>{/if}
          </MetadataListItem>
        {/if}
        {#if d && (checks.length || head)}
          <MetadataListItem label="Checks">
            {#if checks.length === 0}
              <Text type="supporting">No checks recorded on this revision.</Text>
            {:else}
              <ul class="checks">
                {#each checks as c (c.id)}
                  <li>
                    <button class="linkish" onclick={() => inspect('evidence')}>
                      <StateIcon shape={c.passed ? 'check-filled' : 'triangle'} tone={c.passed ? 'success' : 'danger'} size={13} />
                      <Code>{c.command}</Code>
                      <span class={c.passed ? 'tone-success' : 'tone-danger'}>{c.passed ? 'passed' : `failed (exit ${c.exitCode})`}</span>
                    </button>
                  </li>
                {/each}
              </ul>
              {#if earlierChecks}<Text type="supporting">{earlierChecks} earlier {earlierChecks === 1 ? 'run' : 'runs'} on previous revisions</Text>{/if}
            {/if}
          </MetadataListItem>
        {/if}
        {#if d?.reviews.length}
          <MetadataListItem label="Review">
            <ul class="reviews">
              {#each d.reviews as r (r.id)}
                {@const rr = roundFor(r)}
                <li>
                  {#if rr.current}
                    <StateIcon shape={reviewShape(rr.current.state)} tone={reviewTone(rr.current.state)} size={13} />
                    <span
                      >{app.engineerName(r.reviewerId)} {verdictPhrase(rr.current.state)}
                      <Code>{shortSha(rr.current.target.head || rr.current.target.hash) || 'the work'}</Code></span
                    >
                    {#each rr.earlier as e (e.id)}
                      <Text type="supporting">· {verdictPhrase(e.state)} <Code>{shortSha(e.target.head || e.target.hash)}</Code> first</Text>
                    {/each}
                  {:else}
                    <span>{app.engineerName(r.reviewerId)} was asked to review</span>
                  {/if}
                </li>
              {/each}
            </ul>
          </MetadataListItem>
        {/if}
        {#if job.revision?.head}
          <MetadataListItem label="Revision">
            <Code>{shortSha(job.revision.head)}</Code>{#if job.revision.branch}{' '}<Text type="supporting">on {job.revision.branch}</Text>{/if}
          </MetadataListItem>
        {/if}
        {#if docs.length}
          <MetadataListItem label="Documents">
            <ul class="files">
              {#each docs as a (a.id)}<li><Link hasUnderline href={artifactUrl(a.id)} target="_blank" rel="noopener">{a.name}</Link></li>{/each}
            </ul>
          </MetadataListItem>
        {/if}
        {#if d?.pullRequests.length}
          <MetadataListItem label="Pull request">
            {#each d.pullRequests as pr (pr.id)}<PRFacts {pr} compact />{/each}
          </MetadataListItem>
        {/if}
        {#if job.nodeId}
          <MetadataListItem label="Ran on">{app.nodeName(job.nodeId) || 'a paired machine'}</MetadataListItem>
        {/if}
      </MetadataList>
    </div>
  {/if}
</section>

<style>
  .result {
    display: grid;
    gap: var(--spacing-1-5);
    margin-top: var(--spacing-1-5);
    max-width: 560px;
    padding: var(--spacing-3);
    border: 1px solid var(--color-border-emphasized);
    border-radius: var(--radius-container);
    background: var(--color-background-card);
  }
  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .head {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    min-width: 0;
  }
  .title {
    flex: 1;
    font-size: var(--text-body-size);
    font-weight: var(--font-weight-semibold);
  }
  .state {
    flex: none;
    font-size: var(--text-supporting-size);
    font-weight: var(--font-weight-medium);
  }

  .summary {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-0-5) 0;
    font-size: var(--text-supporting-size);
    line-height: var(--text-supporting-leading);
    color: var(--color-text-secondary);
  }
  /* Claims read as one line joined by middots; each Tooltip wraps its claim in a display: contents box. */
  .claim::before {
    display: inline-block;
    content: '·';
    margin: 0 var(--spacing-2);
    color: color-mix(in srgb, var(--color-text-primary) 35%, transparent);
  }
  .summary > :global(:first-child > .claim::before) {
    content: none;
  }
  .claim {
    padding: 0;
    border: 0;
    background: none;
    font: inherit;
    text-align: left;
    cursor: pointer;
    text-decoration: underline;
    text-decoration-color: transparent;
    text-underline-offset: 3px;
    transition: text-decoration-color var(--duration-fast) var(--ease-standard);
  }
  .claim:hover,
  .claim:focus-visible {
    text-decoration-color: currentColor;
  }
  .claim:focus-visible,
  .linkish:focus-visible {
    outline: var(--focus-outline-width) var(--focus-outline-style) var(--focus-outline-color);
    outline-offset: 2px;
    border-radius: var(--radius-inner);
  }
  .command {
    font-family: var(--font-family-code);
  }

  .needs {
    font-size: var(--text-supporting-size);
    color: var(--color-warning);
  }

  .result :global(.more) {
    margin-inline-start: auto;
    color: var(--color-text-secondary);
  }
  .details {
    margin-top: var(--spacing-1);
    padding-top: var(--spacing-2);
    border-top: 1px solid var(--color-border);
  }
  .details :global(dd) {
    min-width: 0;
  }
  /* On a phone the facts stack under their labels. */
  @media (max-width: 480px) {
    .details :global(dl) {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .files,
  .checks,
  .reviews {
    display: grid;
    gap: var(--spacing-1);
    margin: var(--spacing-0-5) 0 0;
  }
  .reviews li,
  .checks li {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
  }
  .rev-summary {
    margin-top: var(--spacing-0-5);
  }
  .add {
    color: var(--color-success);
    font-weight: var(--font-weight-semibold);
  }
  .del {
    color: var(--color-error);
    font-weight: var(--font-weight-semibold);
  }
  .linkish {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
    padding: 0;
    border: 0;
    background: none;
    text-align: left;
    cursor: pointer;
    color: inherit;
  }
  .linkish:hover :global(code) {
    text-decoration: underline;
  }
</style>
