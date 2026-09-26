<script lang="ts">
  // State is a word plus a shape: hollow circle queued, bar running, pause
  // waiting, check ready/completed, triangle failed. Colour only reinforces it.
  import type { Shape, Tone } from '../lib/util/labels';

  interface Props {
    shape: Shape;
    tone?: Tone;
    size?: number;
    /** Gentle motion on "running" bars (disabled under reduced motion). */
    live?: boolean;
  }
  let { shape, tone = 'neutral', size = 14, live = false }: Props = $props();
</script>

<svg class="state tone-{tone}" class:live width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" focusable="false">
  {#if shape === 'circle'}
    <circle cx="8" cy="8" r="5.25" fill="none" stroke="currentColor" stroke-width="1.6" />
  {:else if shape === 'bar'}
    <rect x="1.5" y="6" width="13" height="4" rx="2" fill="currentColor" opacity="0.28" />
    <rect class="fill" x="1.5" y="6" width="7.5" height="4" rx="2" fill="currentColor" />
  {:else if shape === 'pause'}
    <rect x="3.75" y="3" width="3" height="10" rx="1.2" fill="currentColor" />
    <rect x="9.25" y="3" width="3" height="10" rx="1.2" fill="currentColor" />
  {:else if shape === 'check'}
    <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="m5.2 8.2 1.9 1.9 3.8-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
  {:else if shape === 'check-filled'}
    <circle cx="8" cy="8" r="6.5" fill="currentColor" />
    <path d="m5.2 8.2 1.9 1.9 3.8-4" fill="none" stroke="var(--surface)" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" />
  {:else if shape === 'triangle'}
    <path d="M8 2.2 14.3 13.3H1.7Z" fill="currentColor" />
    <path d="M8 6.4v3.2M8 11.4v.1" stroke="var(--surface)" stroke-width="1.6" stroke-linecap="round" />
  {:else if shape === 'slash'}
    <circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="m4.3 11.7 7.4-7.4" stroke="currentColor" stroke-width="1.5" />
  {:else}
    <circle cx="8" cy="8" r="5.75" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="2.2 1.8" />
    <path d="M6.6 6.6a1.5 1.5 0 1 1 2 1.4c-.4.2-.6.5-.6.9v.3M8 11v.1" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
  {/if}
</svg>

<style>
  .state {
    flex: none;
    display: inline-block;
    vertical-align: -2px;
  }
  /* Running is shown by the bar shape and its word, not by looping motion. */
</style>
