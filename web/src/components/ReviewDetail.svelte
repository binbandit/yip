<script lang="ts">
  // Review truth: which revision each round judged, the findings with
  // file:line and evidence, the author's replies, and superseded rounds. An
  // earlier "requested changes" stays visible beside the later approval.
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { artifactUrl } from '../lib/api/endpoints';
  import type { Review } from '../lib/api/types.gen';
  import { findingStatusLabel, reviewShape, reviewStateLabel, reviewTone, severityLabel, verdictPhrase } from '../lib/util/labels';
  import { atTime, shortSha } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';
  import Avatar from './Avatar.svelte';
  import MessageBody from './MessageBody.svelte';
  import Icon from './Icon.svelte';

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
  // A round no machine can pick up says why, rather than sitting "queued".
  const pendingJobId = $derived(latest && ['requested', 'queued'].includes(latest.state) ? latest.reviewJobId : undefined);
  $effect(() => {
    if (pendingJobId) details.ensureJob(pendingJobId, app.data.touched.jobs[pendingJobId] ?? 0);
  });
  const waitingWhy = $derived.by(() => {
    const j = pendingJobId ? app.data.jobs[pendingJobId] : undefined;
    return j?.state === 'waiting' ? j.stateDetail : '';
  });
  const staleApproval = $derived(
    !!latest && latest.state === 'approved' && !!currentHead && !!(latest.target.head || latest.target.hash) && (latest.target.head || latest.target.hash) !== currentHead,
  );
</script>

<article class="review" aria-label="Review by {reviewer}">
  <header class="head">
    <Avatar actor={{ kind: 'engineer', id: review.reviewerId }} size={28} />
    <div>
      <p class="who">
        <strong>{reviewer}</strong> reviewing {author}'s {review.targetKind === 'pr' ? 'pull request' : review.targetKind || 'work'}
      </p>
      <p class="state tone-{reviewTone(review.state)}">
        <StateIcon shape={reviewShape(review.state)} tone={reviewTone(review.state)} size={13} />
        {reviewStateLabel(review.state)}{#if (latest?.target.head || latest?.target.hash)}&nbsp;<span class="meta">on <span class="mono">{shortSha(latest.target.head || latest.target.hash)}</span></span>{/if}
      </p>
    </div>
  </header>
  {#if waitingWhy}<p class="notice attention"><span>{reviewer} can't start this review: {waitingWhy} <a href="/machines">See Machines</a></span></p>{/if}
  {#if review.criteria}<p class="criteria"><span class="meta">Asked to check:</span> {review.criteria}</p>{/if}
  {#if staleApproval}
    <p class="notice attention">This approval is for <span class="mono">{shortSha(latest.target.head || latest.target.hash)}</span>. The work has moved to <span class="mono">{shortSha(currentHead)}</span>, so it no longer counts until the new revision is reviewed.</p>
  {/if}

  <ol class="rounds">
    {#each rounds as r (r.id)}
      <li class="round" class:superseded={!!r.supersededBy}>
        <p class="round-head">
          <StateIcon shape={reviewShape(r.state)} tone={r.supersededBy ? 'neutral' : reviewTone(r.state)} size={13} />
          <span class="round-line">
            <span>
              <strong>Round {r.number}:</strong> {reviewer} {verdictPhrase(r.state)}
              {r.number === 1 && rounds.length > 1 ? 'the first revision' : r === latest && rounds.length > 1 ? 'the updated revision' : 'the revision'}
              {#if r.target.artifactId}<a class="mono" href={artifactUrl(r.target.artifactId)} target="_blank" rel="noreferrer">{shortSha(r.target.hash)} · document</a>{:else if r.target.head}<span class="mono">{shortSha(r.target.head)}</span>{/if}
            </span>
            {#if r.decidedAt}<span class="meta">{atTime(r.decidedAt)}</span>{/if}
            {#if r.supersededBy}<span class="tag">Superseded by a newer revision</span>{/if}
          </span>
        </p>
        {#if r.summary}<div class="summary"><MessageBody message={{ body: r.summary, mentions: [] }} /></div>{/if}
        {#if r.findings?.length}
          <ul class="findings">
            {#each r.findings as f (f.id)}
              <li class="finding sev-{f.severity}">
                <p class="f-head">
                  <span class="sev">{#if f.severity === 'blocking'}<Icon name="alertCircle" size={14} />{/if}{severityLabel(f.severity)}</span>
                  {#if f.file}<button class="link-btn mono loc" onclick={() => app.showInDiff(review.jobId, f.file!, f.line || undefined, r.target.head)} title="Show in the diff"
                      >{f.file}{f.line ? `:${f.line}` : ''}</button
                    >{/if}
                  <span class="f-status">{findingStatusLabel(f.status)}</span>
                </p>
                <MessageBody message={{ body: f.body, mentions: [] }} />
                {#if f.evidence}<pre class="evidence">{f.evidence}</pre>{/if}
                {#each f.replies ?? [] as rep (rep.id)}
                  <div class="reply">
                    <p class="meta">
                      <strong class="rep-who">{app.actorName(rep.author)}</strong> replied{#if rep.revision}&nbsp;with <span class="mono">{shortSha(rep.revision)}</span>{/if} · {atTime(rep.createdAt)}
                    </p>
                    <MessageBody message={{ body: rep.body, mentions: [] }} />
                    {#if rep.evidence}<p class="meta">Evidence: {rep.evidence}</p>{/if}
                  </div>
                {/each}
              </li>
            {/each}
          </ul>
        {:else if r.state === 'approved'}
          <p class="meta">No findings.</p>
        {/if}
      </li>
    {/each}
  </ol>
</article>

<style>
  .review {
    display: grid;
    gap: 10px;
  }
  .head {
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .who {
    font-size: 14px;
  }
  .state {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13.5px;
    font-weight: 650;
  }
  .criteria {
    font-size: 14px;
  }
  .rounds {
    list-style: none;
    margin: 0;
    padding: 0 0 0 14px;
    border-left: 2px solid var(--line);
    display: grid;
    gap: 14px;
  }
  .round {
    display: grid;
    gap: 6px;
  }
  .round-head {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 14px;
    margin-left: -22px;
  }
  /* The shape sits on the rail, centred on the first line; wrapped text stays right of it. */
  .round-head > :global(svg) {
    margin-top: calc((1lh - 15px) / 2);
    background: var(--surface);
    border-radius: 50%;
    padding: 1px;
    box-sizing: content-box;
  }
  .round-line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 8px;
    min-width: 0;
  }
  .superseded .summary,
  .superseded .findings {
    opacity: 0.85;
  }
  .tag {
    font-size: 12px;
    padding: 0 7px;
    border-radius: var(--r-pill);
    border: 1px solid var(--line);
    color: var(--ink-secondary);
  }
  .summary {
    font-size: 14px;
  }
  .findings {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .finding {
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: var(--r-artifact);
    font-size: 14px;
    display: grid;
    gap: 4px;
  }
  .f-head {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: baseline;
    font-size: 13px;
  }
  .sev {
    font-weight: 700;
  }
  .sev :global(.icon) {
    display: inline-block;
    margin-right: 4px;
    vertical-align: -2px;
  }
  .sev-blocking .sev {
    color: var(--danger);
  }
  .loc {
    color: var(--ink);
  }
  .f-status {
    margin-left: auto;
    color: var(--ink-secondary);
  }
  .evidence {
    margin: 2px 0 0;
    padding: 6px 8px;
    border-radius: 6px;
    background: var(--surface-subtle);
    font-size: 12.5px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .reply {
    margin-top: 4px;
    padding-top: 8px;
    border-top: 1px solid var(--line-soft);
  }
  .rep-who {
    color: var(--ink);
  }
</style>
