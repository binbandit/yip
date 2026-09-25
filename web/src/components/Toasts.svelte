<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import Icon from './Icon.svelte';
</script>

<div class="toasts" aria-live="polite" aria-relevant="additions">
  {#each app.toasts as t (t.id)}
    <div class="toast t-{t.tone}">
      <span class="text">{t.text}</span>
      {#if t.action}
        <button
          class="btn btn-sm"
          onclick={() => {
            t.action?.run();
            app.dismissToast(t.id);
          }}>{t.action.label}</button
        >
      {/if}
      <button class="icon-btn" aria-label="Dismiss" onclick={() => app.dismissToast(t.id)}><Icon name="x" size={16} /></button>
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 900;
    display: grid;
    gap: 8px;
    width: min(420px, calc(100vw - 32px));
    pointer-events: none;
  }
  .toast {
    pointer-events: auto;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 8px 10px 14px;
    border-radius: 12px;
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-pop);
    border: 1px solid var(--line);
    font-size: 14px;
    animation: rise var(--t-slow) var(--ease);
  }
  .toast.t-error {
    box-shadow:
      inset 3px 0 0 var(--danger),
      var(--shadow-pop);
  }
  .toast.t-success {
    box-shadow:
      inset 3px 0 0 var(--success),
      var(--shadow-pop);
  }
  .text {
    flex: 1;
    min-width: 0;
  }
  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }
  @media (max-width: 760px) {
    .toasts {
      right: 16px;
      bottom: calc(16px + env(safe-area-inset-bottom));
    }
  }
</style>
