<script lang="ts">
  // A pull request's facts, kept separate: internal peer approval, remote
  // reviews, remote checks, and merge state. Nothing here implies a merge
  // that the forge has not reported.
  import { Card, Code, Link, MetadataList, MetadataListItem, Text } from '@astryx-svelte/core';
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
    <Link hasUnderline href={pr.url} target="_blank" rel="noopener noreferrer">#{pr.number}</Link>
    <span>{prState}</span>
    <Text type="supporting">· {checksText} · {mergeText}</Text>
  </span>
{:else}
  <div class="pr">
    <p class="title">
      <Link hasUnderline href={pr.url} target="_blank" rel="noopener noreferrer" weight="semibold">{pr.owner}/{pr.name} #{pr.number}</Link>
      <Text type="supporting">{pr.title}</Text>
    </p>
    <Text as="p" type="supporting">{prState} · {pr.head} → {pr.base}{#if pr.lastSyncedAt}{' · '}synced {relative(pr.lastSyncedAt, app.now)}{/if}</Text>
    <div class="facts">
      <Card padding={3}>
        <MetadataList label={{ position: 'start', width: 150 }}>
          <MetadataListItem label="Peer review (in yip)">
            <span class="lines">
              {#if peer.length === 0}
                <Text type="supporting">No internal review recorded.</Text>
              {:else}
                {#each peer as p, i (i)}
                  <span>{p.reviewer}: {p.state === 'approved' ? 'approved' : p.state.replace('_', ' ')} <Code size="inherit">{shortSha(p.head)}</Code></span>
                {/each}
                <Text type="supporting">Internal approval is not a forge approval.</Text>
              {/if}
            </span>
          </MetadataListItem>
          <MetadataListItem label="Remote reviews">
            <span class="lines">
              {#if remote.length === 0}
                <Text type="supporting">None on the forge.</Text>
              {:else}
                {#each remote as r (r.externalId)}
                  <span>
                    {r.actor}: {r.state.toLowerCase().replace('_', ' ')} <Code size="inherit">{shortSha(r.commitId)}</Code>
                    {#if r.publishedByEngineerId}{' '}<Text type="supporting">— published by yip for {app.engineerName(r.publishedByEngineerId)}</Text>{/if}
                  </span>
                {/each}
              {/if}
            </span>
          </MetadataListItem>
          <MetadataListItem label="Remote checks">
            <span class="checks-line">
              <StateIcon
                shape={checks.state === 'success' ? 'check-filled' : checks.state === 'failure' ? 'triangle' : checks.state === 'pending' ? 'bar' : 'circle'}
                tone={checks.state === 'success' ? 'success' : checks.state === 'failure' ? 'danger' : 'neutral'}
                size={13}
              />
              {checksText}
            </span>
          </MetadataListItem>
          <MetadataListItem label="Merge">
            <span class="lines">
              <span>{mergeText}</span>
              {#if merge.reasons?.length}
                <ul class="reasons">{#each merge.reasons as r (r)}<li>{r}</li>{/each}</ul>
              {/if}
            </span>
          </MetadataListItem>
        </MetadataList>
      </Card>
    </div>
  </div>
{/if}

<style>
  .compact {
    display: inline-flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
    align-items: baseline;
  }
  .pr {
    display: grid;
    gap: var(--spacing-1-5);
  }
  .title {
    display: flex;
    gap: var(--spacing-2);
    flex-wrap: wrap;
    align-items: baseline;
  }
  .facts {
    margin-top: var(--spacing-1-5);
    container-type: inline-size;
  }
  .lines {
    display: flex;
    flex-direction: column;
    gap: var(--spacing-0-5);
  }
  .checks-line {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
  }
  .reasons {
    margin: var(--spacing-0-5) 0 0;
    padding-left: var(--spacing-4);
    list-style: disc;
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  /* In a narrow panel the labels sit above their facts. */
  @container (max-width: 420px) {
    .facts :global(dl) {
      grid-template-columns: minmax(0, 1fr);
      gap: 0;
    }
    .facts :global(dd + dt) {
      margin-top: var(--spacing-2);
    }
  }
</style>
