<script lang="ts">
  // Page chrome for a screen inside the work card: a scrolling column with the
  // screen's h1 (focused after navigation, see Shell), a subtitle, and actions
  // on the trailing edge. Profiles add a crumb above and an avatar before it.
  import type { Snippet } from 'svelte';
  import { Heading, HStack, Text } from '@astryx-svelte/core';

  interface Props {
    title: string;
    subtitle?: string | Snippet;
    actions?: Snippet;
    /** Above the header, e.g. a link back to the list. */
    crumb?: Snippet;
    /** Before the titles, e.g. a profile avatar. */
    leading?: Snippet;
    children?: Snippet;
    /** Content column width in px. */
    width?: number;
    class?: string;
  }
  let { title, subtitle, actions, crumb, leading, children, width = 1040, class: cls = '' }: Props = $props();
</script>

<div class="screen {cls}">
  <div class="inner" style:max-width="{width}px">
    {#if crumb}<div class="crumb">{@render crumb()}</div>{/if}
    <header class="head">
      {#if leading}<div class="leading">{@render leading()}</div>{/if}
      <div class="titles">
        <Heading level={1} data-screen-title tabindex={-1}>{title}</Heading>
        {#if typeof subtitle === 'string'}
          <Text as="p" color="secondary">{subtitle}</Text>
        {:else if subtitle}
          <Text as="p" color="secondary">{@render subtitle()}</Text>
        {/if}
      </div>
      {#if actions}
        <HStack gap={2} wrap="wrap">{@render actions()}</HStack>
      {/if}
    </header>
    {@render children?.()}
  </div>
</div>

<style>
  .screen {
    height: 100%;
    overflow: auto;
    overscroll-behavior: contain;
  }
  .inner {
    padding: var(--spacing-7) var(--spacing-8) var(--spacing-12);
  }
  .head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: var(--spacing-4);
    flex-wrap: wrap;
    margin-bottom: var(--spacing-6);
  }
  .crumb {
    margin-bottom: var(--spacing-3);
  }
  .leading {
    flex: none;
    align-self: center;
  }
  .titles {
    flex: 1;
    display: grid;
    gap: var(--spacing-1);
    min-width: 0;
    overflow-wrap: anywhere;
  }
  /* The title takes focus after navigation so screen readers start there; it isn't a control. */
  .titles :global(h1:focus) {
    outline: none;
  }
  @media (max-width: 768px) {
    .inner {
      padding: var(--spacing-4) var(--spacing-4) var(--spacing-10);
    }
  }
</style>
