<script lang="ts">
  // Compact, clickable references attached to a message: work, reviews,
  // pull requests, decisions and files. Each opens its actual source.
  // They open a panel, so they are pill-shaped buttons rather than tokens.
  import { untrack, type Snippet } from 'svelte';
  import { BookOpen, Eye, File, GitPullRequest } from '@lucide/svelte';
  import { Button, Icon } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { artifactUrl } from '../lib/api/endpoints';
  import type { Ref } from '../lib/api/types.gen';
  import { jobShape, jobStateLabel, jobTone, reviewShape, reviewStateLabel, reviewTone } from '../lib/util/labels';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    refs: Ref[];
    /** Kinds rendered elsewhere (e.g. the result card's job). */
    skip?: string[];
    /** Specific references (kind:id) already shown nearby. */
    hide?: string[];
    /** The message these belong to: a message added to work says so. */
    messageId?: string;
  }
  let { refs, skip = [], hide = [], messageId }: Props = $props();
  // The input this message became for a piece of work, if any.
  const inputFor = (jobId: string) => (messageId ? Object.values(app.data.inputs).find((i) => i.messageId === messageId && i.jobId === jobId) : undefined);
  const deliveryWord = (d: string) => (d === 'immediate' ? 'received' : d === 'queued' ? 'queued for the next step' : 'delivering');

  // A work chip that records this message being added to that work is new
  // information, so it shows even when the work appeared just above.
  const shown = $derived(
    refs.filter(
      (r) =>
        !skip.includes(r.kind) &&
        (!hide.includes(`${r.kind}:${r.id}`) || (r.kind === 'job' && !!inputFor(r.id))) &&
        ['job', 'review', 'pr', 'decision', 'artifact'].includes(r.kind),
    ),
  );

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

<!-- One chip. `label` is its accessible name; the title truncates while the
     trailing state stays visible. -->
{#snippet chip(label: string, title: string, meta: string | null, onclick: () => void, icon: Snippet)}
  {#snippet state()}<span class="meta">· {meta}</span>{/snippet}
  <Button class="chip ref" variant="secondary" size="sm" {label} {onclick} {icon} endContent={meta ? state : undefined}>{title}</Button>
{/snippet}

{#if shown.length}
  <div class="refs">
    {#each shown as r (r.kind + r.id)}
      {#if r.kind === 'job'}
        {@const j = app.data.jobs[r.id]}
        {#if j && j.kind !== 'reply'}
          {@const input = inputFor(r.id)}
          {@const title = input ? `Added to ${app.engineerName(j.ownerId)}'s ${j.title}` : j.title}
          {@const meta = input ? deliveryWord(input.delivery) : jobStateLabel(j)}
          {#snippet jobIcon()}<StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} size={13} live={j.state === 'running'} />{/snippet}
          {@render chip(`${title} · ${meta}`, title, meta, () => app.openPanel({ kind: 'job', id: r.id }), jobIcon)}
        {/if}
      {:else if r.kind === 'review'}
        {@const rv = app.data.reviews[r.id]}
        {@const meta = rv ? `${app.engineerName(rv.reviewerId)} · ${reviewStateLabel(rv.state)}` : null}
        {#snippet reviewIcon()}
          {#if rv}<StateIcon shape={reviewShape(rv.state)} tone={reviewTone(rv.state)} size={13} />{:else}<Icon icon={Eye} size="sm" />{/if}
        {/snippet}
        {@render chip(meta ? `View review · ${meta}` : 'View review', 'View review', meta, () => app.openPanel({ kind: 'review', id: r.id }), reviewIcon)}
      {:else if r.kind === 'pr'}
        {@const pr = app.data.prs[r.id]}
        {@const title = pr ? `PR #${pr.number}` : 'Pull request'}
        {#snippet prIcon()}<Icon icon={GitPullRequest} size="sm" />{/snippet}
        {@render chip(title, title, null, () => app.openPanel({ kind: 'pr', id: r.id }), prIcon)}
      {:else if r.kind === 'decision'}
        {@const dcs = app.data.decisions[r.id]}
        {@const title = dcs ? dcs.title : 'Decision'}
        {#snippet decisionIcon()}<Icon icon={BookOpen} size="sm" />{/snippet}
        {@render chip(title, title, null, () => app.openPanel({ kind: 'decision', id: r.id }), decisionIcon)}
      {:else if r.kind === 'artifact'}
        <Button class="chip ref" variant="secondary" size="sm" label="File" href={artifactUrl(r.id)} target="_blank" rel="noopener">
          {#snippet icon()}<Icon icon={File} size="sm" />{/snippet}
        </Button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .refs {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
    margin-top: var(--spacing-1-5);
  }
  .refs :global(.chip.ref) {
    max-width: min(100%, 460px);
    border-radius: var(--radius-full);
    font-size: var(--font-size-sm);
  }
  .meta {
    color: var(--color-text-secondary);
    white-space: nowrap;
  }
</style>
