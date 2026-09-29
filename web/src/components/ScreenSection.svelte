<script lang="ts">
  // A titled section of a screen: an h2 with optional trailing content (a
  // count, a filter, an action), then the section body.
  import type { Snippet } from 'svelte';
  import { Heading } from '@astryx-svelte/core';

  interface Props {
    title: string;
    /** Id for the heading, so the section is labelled by it. */
    id?: string;
    end?: Snippet;
    children: Snippet;
    class?: string;
  }
  let { title, id = `sec-${Math.random().toString(36).slice(2, 8)}`, end, children, class: cls = '' }: Props = $props();
</script>

<section class="section {cls}" aria-labelledby={id}>
  <div class="head">
    <Heading level={2} {id}>{title}</Heading>
    {#if end}<div class="end">{@render end()}</div>{/if}
  </div>
  {@render children()}
</section>

<style>
  .section {
    margin-top: var(--spacing-8);
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--spacing-2) var(--spacing-3);
    margin-bottom: var(--spacing-2);
  }
  .head :global(h2) {
    font-size: var(--font-size-lg);
  }
  .end {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-2);
  }
</style>
