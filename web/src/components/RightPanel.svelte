<script lang="ts" module>
  export type PanelMode = 'inline' | 'overlay' | 'full';
</script>

<script lang="ts">
  // The single right-hand panel shell for threads and detail drawers.
  // - inline (≥1200px): sits beside the conversation, split by a hairline;
  //   resizable; does not trap focus (it's an ordinary panel).
  // - overlay (760–1199px): floats over the conversation; modal with a focus trap.
  // - full (<760px): a separate full-screen view with a Back button.
  // Escape closes it in every mode and focus returns to the invoking control.
  import { onMount, type Snippet } from 'svelte';
  import { pushLayer, trapFocus } from '../lib/ui/layers';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    mode: PanelMode;
    onclose: () => void;
    children: Snippet;
    subtitle?: Snippet;
    actions?: Snippet;
    wide?: boolean;
  }
  let { title, mode, onclose, children, subtitle, actions, wide = false }: Props = $props();

  const KEY = 'yip.panelWidth';
  const MIN = 320;
  let width = $state(readWidth());
  let el: HTMLElement | undefined = $state();
  let headingEl: HTMLHeadingElement | undefined = $state();
  const id = `panel-${Math.random().toString(36).slice(2, 8)}`;

  function readWidth(): number {
    try {
      const v = Number(localStorage.getItem(KEY));
      if (v >= MIN) return v;
    } catch {
      /* ignore */
    }
    return 336;
  }
  function maxWidth() {
    return Math.max(MIN, Math.min(820, window.innerWidth - 232 - 420));
  }
  function setWidth(v: number) {
    width = Math.round(Math.max(MIN, Math.min(maxWidth(), v)));
    try {
      localStorage.setItem(KEY, String(width));
    } catch {
      /* ignore */
    }
  }

  onMount(() => {
    const release = pushLayer(() => onclose());
    queueMicrotask(() => headingEl?.focus({ preventScroll: true }));
    return () => release();
  });

  $effect(() => {
    if (mode === 'inline' || !el) return;
    return trapFocus(el);
  });

  function startDrag(e: PointerEvent) {
    e.preventDefault();
    const startX = e.clientX;
    const startW = width;
    const move = (ev: PointerEvent) => setWidth(startW + (startX - ev.clientX));
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  function onSepKey(e: KeyboardEvent) {
    if (e.key === 'ArrowLeft') {
      e.preventDefault();
      setWidth(width + 24);
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      setWidth(width - 24);
    } else if (e.key === 'Home') {
      e.preventDefault();
      setWidth(maxWidth());
    } else if (e.key === 'End') {
      e.preventDefault();
      setWidth(MIN);
    }
  }
</script>

{#if mode === 'overlay'}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="veil" onclick={onclose}></div>
{/if}

<aside
  bind:this={el}
  class="panel {mode}"
  class:wide
  style:--pw="{wide && mode === 'inline' ? Math.max(width, 480) : width}px"
  role={mode === 'inline' ? 'complementary' : 'dialog'}
  aria-modal={mode === 'inline' ? undefined : 'true'}
  aria-labelledby="{id}-title"
>
  {#if mode === 'inline'}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <div
      class="resize"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize panel"
      aria-valuenow={width}
      aria-valuemin={MIN}
      aria-valuemax={820}
      tabindex="0"
      onpointerdown={startDrag}
      onkeydown={onSepKey}
      ondblclick={() => setWidth(336)}
    ></div>
  {/if}
  <header class="head">
    {#if mode === 'full'}
      <button class="icon-btn" aria-label="Back" onclick={onclose}><Icon name="back" /></button>
    {/if}
    <div class="titles">
      <h2 id="{id}-title" bind:this={headingEl} tabindex="-1" {title}>{title}</h2>
      {#if subtitle}<div class="sub">{@render subtitle()}</div>{/if}
    </div>
    {#if actions}{@render actions()}{/if}
    {#if mode !== 'full'}
      <button class="icon-btn" aria-label="Close panel" onclick={onclose}><Icon name="x" /></button>
    {/if}
  </header>
  <div class="body">
    {@render children()}
  </div>
</aside>

<style>
  .panel {
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--surface);
  }
  .panel.inline {
    position: relative;
    width: var(--pw);
    flex: none;
    border-left: 1px solid var(--line);
    animation: slide-in var(--t-slow) var(--ease);
  }
  .panel.overlay {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 40;
    width: min(var(--pw), calc(100% - 48px));
    min-width: min(360px, 100%);
    box-shadow: var(--shadow-panel);
    border-radius: 0 var(--r-surface) var(--r-surface) 0;
    animation: slide-in var(--t-slow) var(--ease);
  }
  .panel.overlay.wide {
    width: min(max(var(--pw), 560px), calc(100% - 48px));
  }
  .panel.full {
    position: absolute;
    inset: 0;
    z-index: 40;
    width: 100%;
  }
  .veil {
    position: absolute;
    inset: 0;
    z-index: 39;
    background: color-mix(in srgb, var(--surface) 55%, transparent);
    border-radius: inherit;
    animation: fade var(--t-slow) var(--ease);
  }
  .resize {
    position: absolute;
    left: -6px;
    top: 0;
    bottom: 0;
    width: 12px;
    cursor: col-resize;
    z-index: 2;
  }
  .resize:hover::after,
  .resize:focus-visible::after {
    content: '';
    position: absolute;
    left: 5px;
    top: 0;
    bottom: 0;
    width: 2px;
    background: var(--accent);
  }
  .resize:focus-visible {
    outline: none;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 52px;
    padding: 8px 10px 8px 18px;
    border-bottom: 1px solid var(--line);
    flex: none;
  }
  .full .head {
    padding-left: 6px;
  }
  .titles {
    flex: 1;
    min-width: 0;
  }
  h2 {
    font-size: 16px;
    font-weight: 650;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  h2:focus-visible {
    outline-offset: 1px;
  }
  .sub {
    font-size: 13px;
    color: var(--ink-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .body {
    flex: 1;
    min-height: 0;
    overflow: auto;
    overscroll-behavior: contain;
    display: flex;
    flex-direction: column;
  }
  @keyframes slide-in {
    from {
      opacity: 0.4;
      transform: translateX(16px);
    }
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
</style>
