<script lang="ts">
  // The branching-y mark with the lowercase wordmark, drawn monochrome in the
  // current ink so it follows the neutral palette in day and night.
  interface Props {
    size?: number;
    showText?: boolean;
  }
  let { size = 24, showText = true }: Props = $props();
</script>

<span class="wordmark" style:--size="{size}px">
  <svg class="mark" viewBox="0 0 256 256" width={size} height={size} aria-hidden="true" focusable="false">
    <rect width="256" height="256" rx="64" class="tile" />
    <path d="M72 64 128 124" fill="none" class="arm" stroke-width="28" stroke-linecap="round" />
    <path d="M184 64 72 196" fill="none" class="stem" stroke-width="28" stroke-linecap="round" />
  </svg>
  {#if showText}<span class="text">yip</span>{:else}<span class="vh">yip</span>{/if}
</span>

<style>
  .wordmark {
    display: inline-flex;
    align-items: center;
    gap: calc(var(--size) * 0.34);
    color: var(--ink);
  }
  .mark {
    display: block;
    flex: none;
  }
  .tile {
    fill: var(--ink);
  }
  .arm {
    stroke: color-mix(in srgb, var(--surface) 62%, var(--ink));
  }
  .stem {
    stroke: var(--surface);
  }
  .text {
    font-family: var(--font-brand);
    font-weight: 650;
    font-size: calc(var(--size) * 0.86);
    letter-spacing: -0.035em;
    line-height: 1;
    transform: translateY(-1px);
  }
</style>
