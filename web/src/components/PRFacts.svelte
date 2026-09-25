<script lang="ts">
  // A pull request's facts, kept separate: internal peer approval, remote
  // reviews, remote checks, and merge state. Nothing here implies a merge
  // that the forge has not reported.
  import { app } from '../lib/state/app.svelte';
  import type { PullRequest, Review } from '../lib/api/types.gen';
  import { relative, shortSha } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    pr: PullRequest;
    compact?: boolean;
    /** Internal reviews for the same work (peer approval). */
    reviews?: Review[];
  }
  let { pr, compact = false, reviews = [] }: Props = $props();

  const checks = $derived(pr.checks ?? { state: 'unknown', passed: 0, failed: 0, pending: 0, total: 0 });
  const merge = $derived(pr.merge ?? { merged: false, mergeable: 'unknown', reasons: [] });
  const remote = $derived(pr.remoteReviews ?? []);
  const prState = $derived(merge.merged || pr.state === 'merged' ? 'Merged' : pr.state === 'closed' ? 'Closed' : 'Open');
  const checksText = $derived(
    checks.state === 'none' || checks.total === 0
      ? 'No remote checks reported'
      : checks.state === 'success'
        ? `${checks.passed} of ${checks.total} remote checks passed`
        : checks.state === 'failure'
          ? `${checks.failed} of ${checks.total} remote checks failed`
          : checks.state === 'pending'
            ? `${checks.pending} of ${checks.total} remote checks still running`
            : 'Remote check state unknown',
  );
  const mergeText = $derived(
    merge.merged
      ? 'Merged'
      : merge.mergeable === 'clean'
        ? 'Not merged · mergeable'
        : merge.mergeable === 'blocked'
          ? 'Not merged · blocked by the forge'
          : merge.mergeable === 'dirty'
            ? 'Not merged · has conflicts'
            : merge.mergeable === 'behind'
              ? 'Not merged · behind the base branch'
              : merge.mergeable === 'unstable'
                ? 'Not merged · checks unstable'
                : 'Not merged · mergeability unknown',
  );
  const peer = $derived(
    reviews.map((r) => {
      const rounds = [...r.rounds].sort((a, b) => a.number - b.number);
      const cur = rounds[rounds.length - 1];
      return { reviewer: app.engineerName(r.reviewerId), state: cur?.state ?? r.state, head: cur?.target.head };
    }),
  );
</script>

{#if compact}
  <span class="compact">
    <a href={pr.url} target="_blank" rel="noopener noreferrer">#{pr.number}</a>
    <span>{prState}</span>
    <span class="meta">· {checksText} · {mergeText}</span>
  </span>
{:else}
  <div class="pr">
    <p class="title">
      <a href={pr.url} target="_blank" rel="noopener noreferrer">{pr.owner}/{pr.name} #{pr.number}</a>
      <span class="meta">{pr.title}</span>
    </p>
    <p class="meta">{prState} · {pr.head} → {pr.base}{#if pr.lastSyncedAt} · synced {relative(pr.lastSyncedAt, app.now)}{/if}</p>
    <dl>
      <div>
        <dt>Peer review (in yip)</dt>
        <dd>
          {#if peer.length === 0}
            <span class="meta">No internal review recorded.</span>
          {:else}
            {#each peer as p, i (i)}
              <p>{p.reviewer}: {p.state === 'approved' ? 'approved' : p.state.replace('_', ' ')} <span class="mono">{shortSha(p.head)}</span></p>
            {/each}
            <p class="meta">Internal approval is not a forge approval.</p>
          {/if}
        </dd>
      </div>
      <div>
        <dt>Remote reviews</dt>
        <dd>
          {#if remote.length === 0}
            <span class="meta">None on the forge.</span>
          {:else}
            {#each remote as r (r.externalId)}
              <p>
                {r.actor}: {r.state.toLowerCase().replace('_', ' ')} <span class="mono">{shortSha(r.commitId)}</span>
                {#if r.publishedByEngineerId}{' '}<span class="meta">— published by yip for {app.engineerName(r.publishedByEngineerId)}</span>{/if}
              </p>
            {/each}
          {/if}
        </dd>
      </div>
      <div>
        <dt>Remote checks</dt>
        <dd>
          <StateIcon
            shape={checks.state === 'success' ? 'check-filled' : checks.state === 'failure' ? 'triangle' : checks.state === 'pending' ? 'bar' : 'circle'}
            tone={checks.state === 'success' ? 'success' : checks.state === 'failure' ? 'danger' : 'neutral'}
            size={13}
          />
          {checksText}
        </dd>
      </div>
      <div>
        <dt>Merge</dt>
        <dd>
          {mergeText}
          {#if merge.reasons?.length}
            <ul class="reasons">{#each merge.reasons as r (r)}<li>{r}</li>{/each}</ul>
          {/if}
        </dd>
      </div>
    </dl>
  </div>
{/if}

<style>
  .compact {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: baseline;
  }
  .pr {
    display: grid;
    gap: 6px;
  }
  .title {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: baseline;
    font-weight: 600;
  }
  dl {
    margin: 6px 0 0;
    display: grid;
    gap: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-artifact);
  }
  dl > div {
    display: grid;
    grid-template-columns: 150px minmax(0, 1fr);
    gap: 12px;
    padding: 9px 12px;
    font-size: 14px;
  }
  dl > div + div {
    border-top: 1px solid var(--line-soft);
  }
  dt {
    color: var(--ink-secondary);
    font-size: 13px;
  }
  dd {
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  dd :global(svg) {
    display: inline-block;
  }
  .reasons {
    margin: 2px 0 0;
    padding-left: 18px;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  @media (max-width: 480px) {
    dl > div {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }
</style>
