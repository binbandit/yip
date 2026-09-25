<script lang="ts">
  // The branching-y mark with the lowercase wordmark (Bricolage Grotesque is
  // used only here and in the occasional onboarding title).
  interface Props {
    size?: number;
    showText?: boolean;
  }
  let { size = 24, showText = true }: Props = $props();
</script>

<span class="wordmark" style:--size="{size}px">
  <svg class="mark" viewBox="0 0 256 256" width={size} height={size} aria-hidden="true" focusable="false">
    <rect width="256" height="256" rx="60" fill="#183A43" />
    <path d="M72 64 128 124" fill="none" stroke="#93D2DC" stroke-width="28" stroke-linecap="round" />
    <path d="M184 64 72 196" fill="none" stroke="#FFFFFF" stroke-width="28" stroke-linecap="round" />
    <circle cx="72" cy="196" r="14" fill="#F6DB7C" />
  </svg>
  {#if showText}<span class="text">yip</span>{:else}<span class="vh">yip</span>{/if}
</span>

<style>
  .wordmark {
    display: inline-flex;
    align-items: center;
    gap: calc(var(--size) * 0.36);
    color: var(--ink);
  }
  .mark {
    display: block;
    flex: none;
  }
  .text {
    font-family: var(--font-brand);
    font-weight: 600;
    font-size: calc(var(--size) * 0.92);
    letter-spacing: -0.03em;
    line-height: 1;
    transform: translateY(-1px);
  }
</style>
