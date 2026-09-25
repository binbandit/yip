<script lang="ts">
  // Compact, clickable references attached to a message: work, reviews,
  // pull requests, decisions and files. Each opens its actual source.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import type { Ref } from '../lib/api/types.gen';
  import { jobShape, jobStateLabel, jobTone, reviewShape, reviewStateLabel, reviewTone } from '../lib/util/labels';
  import StateIcon from './StateIcon.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    refs: Ref[];
    /** Kinds rendered elsewhere (e.g. the result card's job). */
    skip?: string[];
  }
  let { refs, skip = [] }: Props = $props();

  const shown = $derived(refs.filter((r) => !skip.includes(r.kind) && ['job', 'review', 'pr', 'decision', 'artifact'].includes(r.kind)));

  $effect(() => {
    const list = shown;
    untrack(() => {
      // Work chips use jobs already known from the room's work strip or the
      // event stream; conversational replies are deliberately not shown.
      for (const r of list) {
        if (r.kind === 'review') void details.ensureReview(r.id).catch(() => {});
      }
    });
  });
</script>

{#if shown.length}
  <div class="refs">
    {#each shown as r (r.kind + r.id)}
      {#if r.kind === 'job'}
        {@const j = app.data.jobs[r.id]}
        {#if j && j.kind !== 'reply'}
          <button class="chip ref" onclick={() => app.openPanel({ kind: 'job', id: r.id })}>
            <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} size={13} live={j.state === 'running'} />
            <span class="truncate">{j.title}</span>
            <span class="meta">· {jobStateLabel(j)}</span>
          </button>
        {/if}
      {:else if r.kind === 'review'}
        {@const rv = app.data.reviews[r.id]}
        <button class="chip ref" onclick={() => app.openPanel({ kind: 'review', id: r.id })}>
          {#if rv}
            <StateIcon shape={reviewShape(rv.state)} tone={reviewTone(rv.state)} size={13} />
            <span>View review</span>
            <span class="meta">· {app.engineerName(rv.reviewerId)} · {reviewStateLabel(rv.state)}</span>
          {:else}
            <Icon name="eye" size={14} /><span>View review</span>
          {/if}
        </button>
      {:else if r.kind === 'pr'}
        {@const pr = app.data.prs[r.id]}
        <button class="chip ref" onclick={() => app.openPanel({ kind: 'pr', id: r.id })}>
          <Icon name="pr" size={14} /><span>{pr ? `PR #${pr.number}` : 'Pull request'}</span>
        </button>
      {:else if r.kind === 'decision'}
        {@const dcs = app.data.decisions[r.id]}
        <button class="chip ref" onclick={() => app.openPanel({ kind: 'decision', id: r.id })}>
          <Icon name="book" size={14} /><span class="truncate">{dcs ? dcs.title : 'Decision'}</span>
        </button>
      {:else if r.kind === 'artifact'}
        <a class="chip ref" href="/v1/artifacts/{r.id}" target="_blank" rel="noopener"><Icon name="file" size={14} />File</a>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .refs {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 6px;
  }
  .ref {
    max-width: min(100%, 460px);
    min-height: 28px;
  }
  @media (pointer: coarse) {
    .ref {
      min-height: 40px;
    }
  }
</style>
