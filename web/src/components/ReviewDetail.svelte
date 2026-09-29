<script lang="ts">
  // Review truth: which revision each round judged, the findings with
  // file:line and evidence, the author's replies, and superseded rounds. An
  // earlier "requested changes" stays visible beside the later approval.
  import { Badge, Card, Code, CodeBlock, Icon, Link, Text } from '@astryx-svelte/core';
  import { CircleAlert } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { artifactUrl } from '../lib/api/endpoints';
  import type { Review } from '../lib/api/types.gen';
  import { findingStatusLabel, reviewShape, reviewStateLabel, reviewTone, severityLabel, verdictPhrase } from '../lib/util/labels';
  import { atTime, shortSha } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';
  import Notice from './Notice.svelte';
  import Avatar from './Avatar.svelte';
  import MessageBody from './MessageBody.svelte';

  interface Props {
    review: Review;
    /** Head of the work's current revision, to mark approvals of older revisions. */
    currentHead?: string;
  }
  let { review, currentHead = '' }: Props = $props();

  const rounds = $derived([...(review.rounds ?? [])].sort((a, b) => a.number - b.number));
  const reviewer = $derived(app.engineerName(review.reviewerId));
  const author = $derived(app.engineerName(review.authorId));
  const latest = $derived(rounds[rounds.length - 1]);
  const staleApproval = $derived(
    !!latest && latest.state === 'approved' && !!currentHead && !!(latest.target.head || latest.target.hash) && (latest.target.head || latest.target.hash) !== currentHead,
  );

  // Hue only for a confirmed approval or a review that couldn't happen.
  // "Changes requested" is an engineering state, so it stays neutral.
  const verdictBadge = (state: Review['state']): 'green' | 'yellow' | 'neutral' =>
    state === 'approved' ? 'green' : state === 'unable_to_review' ? 'yellow' : 'neutral';
</script>

<article class="review" aria-label="Review by {reviewer}">
  <header class="head">
    <Avatar actor={{ kind: 'engineer', id: review.reviewerId }} size={24} />
    <div class="head-text">
      <Text as="p">
        <strong>{reviewer}</strong> reviewing {author}'s {review.targetKind === 'pr' ? 'pull request' : review.targetKind || 'work'}
      </Text>
      <p class="state">
        <Badge variant={verdictBadge(review.state)} label={reviewStateLabel(review.state)}>
          {#snippet icon()}<StateIcon shape={reviewShape(review.state)} tone={reviewTone(review.state)} size={12} />{/snippet}
        </Badge>
        {#if latest?.target.head || latest?.target.hash}
          <Text type="supporting">on <Code size="inherit">{shortSha(latest.target.head || latest.target.hash)}</Code></Text>
        {/if}
      </p>
    </div>
  </header>
  {#if review.criteria}<Text as="p"><Text type="supporting">Asked to check:</Text> {review.criteria}</Text>{/if}
  {#if staleApproval}
    <Notice tone="warning">
      {#snippet title()}This approval is for <Code size="inherit">{shortSha(latest.target.head || latest.target.hash)}</Code>.{/snippet}
      {#snippet description()}The work has moved to <Code size="inherit">{shortSha(currentHead)}</Code>, so it no longer counts until the new revision is reviewed.{/snippet}
    </Notice>
  {/if}

  <ol class="rounds">
    {#each rounds as r (r.id)}
      <li class="round" class:superseded={!!r.supersededBy}>
        <div class="round-head">
          <span class="node"><StateIcon shape={reviewShape(r.state)} tone={r.supersededBy ? 'neutral' : reviewTone(r.state)} size={13} /></span>
          <p class="round-title">
            <span>
              <strong>Round {r.number}:</strong> {reviewer} {verdictPhrase(r.state)}
              {r.number === 1 && rounds.length > 1 ? 'the first revision' : r === latest && rounds.length > 1 ? 'the updated revision' : 'the revision'}
              {#if r.target.artifactId}<Link hasUnderline href={artifactUrl(r.target.artifactId)} target="_blank" rel="noreferrer"
                  ><Code size="inherit" color="inherit">{shortSha(r.target.hash)} · document</Code></Link
                >{:else if r.target.head}<Code size="inherit">{shortSha(r.target.head)}</Code>{/if}
            </span>
            {#if r.decidedAt}<Text type="supporting">{atTime(r.decidedAt)}</Text>{/if}
            {#if r.supersededBy}<Badge variant="neutral" label="Superseded by a newer revision" />{/if}
          </p>
        </div>
        {#if r.summary}<div class="summary"><MessageBody message={{ body: r.summary, mentions: [] }} /></div>{/if}
        {#if r.findings?.length}
          <ul class="findings">
            {#each r.findings as f (f.id)}
              <li class="finding">
                <Card padding={3}>
                  <div class="f-body">
                    <p class="f-head">
                      {#if f.severity === 'blocking'}
                        <!-- Blocking leads with the danger shape, like every other danger notice. -->
                        <Badge variant="red" label={severityLabel(f.severity)}>
                          {#snippet icon()}<Icon icon={CircleAlert} size="xsm" color="inherit" />{/snippet}
                        </Badge>
                      {:else}
                        <Badge variant="neutral" label={severityLabel(f.severity)} />
                      {/if}
                      {#if f.file}<Link
                          class="loc"
                          tooltip="Show in the diff"
                          hasUnderline
                          onclick={() => app.showInDiff(review.jobId, f.file!, f.line || undefined, r.target.head)}
                          ><span class="path">{f.file}{f.line ? `:${f.line}` : ''}</span></Link
                        >{/if}
                      <span class="f-status"><Text type="supporting">{findingStatusLabel(f.status)}</Text></span>
                    </p>
                    <MessageBody message={{ body: f.body, mentions: [] }} />
                    {#if f.evidence}<CodeBlock code={f.evidence} size="sm" width="100%" container="section" isWrapped />{/if}
                    {#each f.replies ?? [] as rep (rep.id)}
                      <div class="reply">
                        <Text as="p" type="supporting">
                          <strong class="rep-who">{app.actorName(rep.author)}</strong> replied{#if rep.revision}&nbsp;with <Code size="inherit">{shortSha(rep.revision)}</Code>{/if}
                          · {atTime(rep.createdAt)}
                        </Text>
                        <MessageBody message={{ body: rep.body, mentions: [] }} />
                        {#if rep.evidence}<Text as="p" type="supporting">Evidence: {rep.evidence}</Text>{/if}
                      </div>
                    {/each}
                  </div>
                </Card>
              </li>
            {/each}
          </ul>
        {:else if r.state === 'approved'}
          <Text as="p" type="supporting">No findings.</Text>
        {/if}
      </li>
    {/each}
  </ol>
</article>

<style>
  .review {
    display: grid;
    gap: var(--spacing-3);
  }
  .head {
    display: flex;
    gap: var(--spacing-3);
    align-items: flex-start;
  }
  .head-text {
    display: grid;
    gap: var(--spacing-1);
    min-width: 0;
  }
  .state {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-2);
  }
  .rounds {
    margin: 0;
    padding: 0 0 0 var(--spacing-3);
    border-left: 2px solid var(--color-border);
    display: grid;
    gap: var(--spacing-4);
  }
  .round {
    display: grid;
    gap: var(--spacing-2);
    min-width: 0;
  }
  .round-head {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    align-items: start;
    gap: var(--spacing-2);
    /* The state shape sits on the rail; the text wraps beside it. */
    margin-left: calc(-1 * var(--spacing-3) - 10px);
  }
  .round-title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-1) var(--spacing-2);
  }
  .node {
    display: inline-flex;
    margin-top: 1px;
    padding: 2px;
    border-radius: var(--radius-full);
    background: var(--color-background-surface);
  }
  .superseded .summary,
  .superseded .findings {
    opacity: 0.85;
  }
  .summary {
    min-width: 0;
  }
  .findings {
    display: grid;
    gap: var(--spacing-2);
  }
  /* Severity is the badge's job; the evidence is an inset well, not a card within the card. */
  .finding :global(.astryx-code-block) {
    border-radius: var(--radius-inner);
    background: var(--color-background-muted);
  }
  .f-body {
    display: grid;
    gap: var(--spacing-1-5);
    min-width: 0;
  }
  .f-head {
    display: flex;
    gap: var(--spacing-2);
    flex-wrap: wrap;
    align-items: center;
  }
  .path {
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    overflow-wrap: anywhere;
  }
  .f-status {
    margin-left: auto;
  }
  .reply {
    display: grid;
    gap: var(--spacing-1);
    margin-top: var(--spacing-1);
    padding-top: var(--spacing-2);
    border-top: 1px solid var(--color-border);
  }
  .rep-who {
    color: var(--color-text-primary);
  }
</style>
