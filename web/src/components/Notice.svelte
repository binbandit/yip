<script lang="ts">
  // A notice or a form error: a sentence led by its tone's status shape (a
  // circled i, a triangle, a circled !), never a tinted box or an edge stripe.
  // The shape carries the hue; danger text is red as well (docs/design).
  //
  // It is not a live region unless asked: pass role="alert" for an error from
  // an action, role="status" for a notice that appears in response to one, and
  // nothing for a standing fact a view shows each time it opens.
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { Icon } from '@astryx-svelte/core';
  import { CircleAlert, Info, TriangleAlert } from '@lucide/svelte';

  interface Props extends Omit<HTMLAttributes<HTMLDivElement>, 'title'> {
    tone?: 'info' | 'warning' | 'danger';
    /** A short lead, set in semibold, when the notice has a detail to follow. */
    title?: string | Snippet;
    description?: string | Snippet;
    /** The sentence itself, or anything that follows it, such as a confirmation's buttons. */
    children?: Snippet;
    /** Actions on the trailing edge. */
    end?: Snippet;
  }
  let { tone = 'info', title, description, children, end, class: cls = '', ...rest }: Props = $props();

  const SHAPES = {
    info: { icon: Info, color: 'secondary' },
    warning: { icon: TriangleAlert, color: 'warning' },
    danger: { icon: CircleAlert, color: 'inherit' },
  } as const;
  const shape = $derived(SHAPES[tone]);
</script>

{#snippet text(value: string | Snippet)}{#if typeof value === 'string'}{value}{:else}{@render value()}{/if}{/snippet}

<div class="notice {tone} {cls}" {...rest}>
  <span class="icon"><Icon icon={shape.icon} size="sm" color={shape.color} /></span>
  <div class="body">
    {#if title}<p class="title">{@render text(title)}</p>{/if}
    {#if description}<p>{@render text(description)}</p>{/if}
    {@render children?.()}
  </div>
  {#if end}<div class="end">{@render end()}</div>{/if}
</div>

<style>
  .notice {
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-2);
    color: var(--color-text-primary);
    font-size: var(--text-body-size);
    line-height: var(--text-body-leading);
  }
  .danger {
    color: var(--color-error);
  }
  /* Centre the 16px shape on the first line of text. */
  .icon {
    display: inline-flex;
    flex: none;
    margin-top: calc((1lh - 16px) / 2);
  }
  .body {
    display: grid;
    gap: var(--spacing-1);
    flex: 1;
    min-width: 0;
  }
  /* Astryx's reset gives every paragraph the primary colour; here it follows the tone. */
  .body :global(p) {
    color: inherit;
  }
  .title {
    font-weight: var(--font-weight-semibold);
  }
  .end {
    flex: none;
    align-self: center;
  }
</style>
