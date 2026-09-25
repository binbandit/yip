<script lang="ts">
  // Modal dialog on the native <dialog> element: the rest of the page becomes
  // inert, focus is trapped, Escape closes the topmost layer, and focus returns
  // to the control that opened it.
  import { onMount, type Snippet } from 'svelte';
  import { pushLayer, trapFocus, focusFirst } from '../lib/ui/layers';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    description?: string;
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
    width?: number;
    /** Selector of the element to focus first. */
    initialFocus?: string;
    /** Dialogs that must not close by clicking outside (e.g. a shown-once secret). */
    dismissOnBackdrop?: boolean;
  }
  let { title, description, onclose, children, footer, width = 520, initialFocus, dismissOnBackdrop = true }: Props = $props();

  let el: HTMLDialogElement | undefined = $state();
  const id = `dlg-${Math.random().toString(36).slice(2, 8)}`;

  onMount(() => {
    if (!el) return;
    const release = pushLayer(() => onclose());
    try {
      el.showModal();
    } catch {
      el.setAttribute('open', '');
    }
    const untrap = trapFocus(el);
    queueMicrotask(() => el && focusFirst(el, initialFocus));
    return () => {
      untrap();
      if (el?.open) el.close();
      release();
    };
  });

  function onCancel(e: Event) {
    // Escape is handled by the layer stack so only the topmost layer closes.
    e.preventDefault();
  }

  function onBackdrop(e: MouseEvent) {
    if (dismissOnBackdrop && e.target === el) onclose();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
<dialog
  bind:this={el}
  class="dialog"
  style:--w="{width}px"
  aria-labelledby="{id}-title"
  aria-describedby={description ? `${id}-desc` : undefined}
  oncancel={onCancel}
  onclick={onBackdrop}
>
  <div class="inner">
    <header>
      <h2 id="{id}-title">{title}</h2>
      <button class="icon-btn" aria-label="Close" onclick={onclose}><Icon name="x" /></button>
    </header>
    {#if description}<p class="desc" id="{id}-desc">{description}</p>{/if}
    <div class="body">
      {@render children()}
    </div>
    {#if footer}
      <footer>{@render footer()}</footer>
    {/if}
  </div>
</dialog>

<style>
  .dialog {
    width: min(var(--w), calc(100vw - 32px));
    max-height: min(88vh, 900px);
    padding: 0;
    border: 1px solid color-mix(in srgb, var(--line-strong) 60%, transparent);
    border-radius: var(--r-surface);
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-dialog);
    overflow: hidden;
  }
  .dialog[open] {
    display: flex;
    animation: pop var(--t-slow) var(--ease);
  }
  .dialog::backdrop {
    background: var(--veil);
    backdrop-filter: blur(5px);
  }
  .inner {
    display: flex;
    flex-direction: column;
    min-height: 0;
    width: 100%;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 18px 16px 0 24px;
  }
  h2 {
    font-size: 16px;
    letter-spacing: -0.02em;
  }
  .desc {
    padding: 4px 24px 0;
    color: var(--ink-secondary);
    font-size: 14px;
  }
  .body {
    padding: 16px 24px 20px;
    overflow: auto;
    min-height: 0;
  }
  footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    flex-wrap: wrap;
    padding: 14px 24px;
    border-top: 1px solid var(--line);
    background: color-mix(in srgb, var(--surface-subtle) 50%, var(--surface));
  }
  @keyframes pop {
    from {
      opacity: 0;
      transform: translateY(6px) scale(0.985);
    }
  }
  @media (max-width: 760px) {
    .dialog {
      width: 100vw;
      max-width: 100vw;
      max-height: 92vh;
      margin: auto 0 0;
      border-radius: var(--r-surface) var(--r-surface) 0 0;
    }
    header {
      padding: 16px 12px 0 16px;
    }
    .body {
      padding: 12px 16px 16px;
    }
    footer {
      padding: 12px 16px calc(12px + env(safe-area-inset-bottom));
    }
    .desc {
      padding: 4px 16px 0;
    }
  }
</style>
