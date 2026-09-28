<script lang="ts">
  import { app, type Toast } from '../lib/state/app.svelte';
  import type { Tone } from '../lib/util/labels';
  import Icon from './Icon.svelte';

  // Errors and confirmations lead with their tone's shape; info is plain text.
  const LEAD: Record<Exclude<Toast['tone'], 'info'>, { icon: string; tone: Tone }> = {
    error: { icon: 'alertCircle', tone: 'danger' },
    success: { icon: 'check', tone: 'success' },
  };
</script>

<div class="toasts" aria-live="polite" aria-relevant="additions">
  {#each app.toasts as t (t.id)}
    <div class="toast">
      {#if t.tone !== 'info'}
        {@const lead = LEAD[t.tone]}
        <Icon name={lead.icon} size={16} class="tone-{lead.tone}" />
      {/if}
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
