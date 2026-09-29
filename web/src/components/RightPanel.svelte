<script lang="ts" module>
  export type PanelMode = 'inline' | 'overlay' | 'full';
</script>

<script lang="ts">
  // The single right-hand panel shell for threads and detail drawers.
  // - inline (≥1200px): sits beside the conversation, split by a hairline;
  //   resizable; does not trap focus (it's an ordinary panel).
  // - overlay (769–1199px): floats over the conversation; modal with a focus trap.
  // - full (phones): a separate full-screen view with a Back button.
  // Escape closes it in every mode (after any menu or dialog opened inside it)
  // and focus returns to the invoking control.
  import { onMount, type Snippet } from 'svelte';
  import { Heading, Icon, IconButton, ResizeHandle, useFocusTrap, useResizable } from '@astryx-svelte/core';
  import { ArrowLeft, X } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';

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

  const id = `panel-${Math.random().toString(36).slice(2, 8)}`;
  let el: HTMLElement | undefined = $state();

  // The width is remembered across panels and sessions (double-click the handle
  // to reset it). Leave the conversation at least 420px beside the 260px sidebar.
  const DEFAULT_WIDTH = 336;
  const resizable = useResizable(() => ({
    defaultSize: DEFAULT_WIDTH,
    minSizePx: 320,
    maxSizePx: Math.max(320, Math.min(820, app.viewport - 260 - 420)),
    autoSaveId: 'yip.panelWidth',
  }));
  const width = $derived(wide && mode === 'inline' ? Math.max(resizable.size, 480) : resizable.size);

  // Overlaid and full-screen panels are modal: Astryx's trap keeps focus inside
  // and puts the panel on its Escape stack, above whatever opened it.
  const trap = useFocusTrap(() => ({ isActive: mode !== 'inline', onEscape: onclose }));

  // When the window widens past 1200px the panel stops being modal, and the
  // trap's teardown hands focus back to the invoker. The panel is still open,
  // so keep focus where it was. (Pre-effects run before the trap's teardown.)
  let wasModal = false;
  $effect.pre(() => {
    const modal = mode !== 'inline';
    const active = document.activeElement;
    if (wasModal && !modal && active instanceof HTMLElement && el?.contains(active)) {
      queueMicrotask(() => {
        if (active.isConnected && document.activeElement !== active) active.focus({ preventScroll: true });
      });
    }
    wasModal = modal;
  });

  // An inline panel is not modal, so it only takes an Escape nothing else
  // claimed: Astryx's layers (a menu or dialog inside the panel) handle the
  // press first and mark it handled, and an open combobox keeps its own.
  function onWindowKey(e: KeyboardEvent) {
    if (mode !== 'inline' || e.key !== 'Escape' || e.defaultPrevented) return;
    const t = e.target as HTMLElement | null;
    if (t?.getAttribute('role') === 'combobox' && t.getAttribute('aria-expanded') === 'true') return;
    e.preventDefault();
    onclose();
  }

  // Astryx's TabList marks every Escape handled (its list navigation claims the
  // key even with nothing to dismiss), so neither the layer stack nor the
  // handler above would see a press made on a tab. A tab is never a layer of
  // its own: its Escape is the panel's, taken here before the list claims it.
  function onTabEscape(e: KeyboardEvent) {
    if (e.key !== 'Escape' || e.defaultPrevented || e.isComposing) return;
    if (!(e.target instanceof HTMLElement) || e.target.getAttribute('role') !== 'tab') return;
    e.preventDefault();
    onclose();
  }

  onMount(() => {
    const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    queueMicrotask(() => el?.querySelector<HTMLElement>(`#${id}-title`)?.focus({ preventScroll: true }));
    return () => {
      const active = document.activeElement;
      if (!invoker || (active && active !== document.body && !el?.contains(active))) return;
      // Wait for the update that removes the panel: until then the content
      // behind a modal panel is still inert and can't take focus.
      queueMicrotask(() => {
        const now = document.activeElement;
        if (invoker.isConnected && (!now || now === document.body)) invoker.focus({ preventScroll: true });
      });
    };
  });
</script>

<svelte:window onkeydown={onWindowKey} />

{#if mode === 'overlay'}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="veil" onclick={onclose}></div>
{/if}

<aside
  bind:this={el}
  {@attach trap.attachContainer}
  class="panel {mode}"
  class:wide
  style:--pw="{width}px"
  role={mode === 'inline' ? 'complementary' : 'dialog'}
  aria-modal={mode === 'inline' ? undefined : 'true'}
  aria-labelledby="{id}-title"
  onkeydowncapture={onTabEscape}
>
  {#if mode === 'inline'}
    <!-- Astryx's handle only uses double-click to collapse, which this panel can't. -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="resize" ondblclick={() => resizable.resize(DEFAULT_WIDTH)}>
      <ResizeHandle resizable={resizable.props} isReversed hasDivider isAlwaysVisible={false} label="Resize panel" />
    </div>
  {/if}
  <header class="head">
    {#if mode === 'full'}
      <IconButton label="Back" variant="ghost" onclick={onclose}>
        {#snippet icon()}<Icon icon={ArrowLeft} size="md" />{/snippet}
      </IconButton>
    {/if}
    <div class="titles">
      <Heading level={2} id="{id}-title" tabindex={-1} maxLines={1}>{title}</Heading>
      {#if subtitle}<div class="sub">{@render subtitle()}</div>{/if}
    </div>
    {#if actions}{@render actions()}{/if}
    {#if mode !== 'full'}
      <IconButton label="Close panel" variant="ghost" onclick={onclose}>
        {#snippet icon()}<Icon icon={X} size="md" />{/snippet}
      </IconButton>
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
    background: var(--color-background-surface);
  }
  .panel.inline {
    position: relative;
    width: var(--pw);
    flex: none;
    animation: slide-in var(--duration-medium-min) var(--ease-standard);
  }
  .panel.overlay {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 40;
    width: min(var(--pw), calc(100% - 48px));
    min-width: min(360px, 100%);
    box-shadow: var(--shadow-high);
    border-inline-start: 1px solid var(--color-border);
    animation: slide-in var(--duration-medium-min) var(--ease-standard);
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
    background: color-mix(in srgb, var(--color-background-surface) 55%, transparent);
    animation: fade var(--duration-medium-min) var(--ease-standard);
  }
  /* The handle doubles as the hairline between the conversation and the panel. */
  .resize {
    position: absolute;
    inset-block: 0;
    inset-inline-start: 0;
    z-index: 2;
    display: flex;
    transform: translateX(-50%);
  }
  .head {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    min-height: var(--yip-pane-header-h);
    padding: var(--spacing-2) var(--spacing-2) var(--spacing-2) var(--spacing-4);
    border-bottom: 1px solid var(--color-border);
    flex: none;
  }
  .full .head {
    padding-inline-start: var(--spacing-1-5);
  }
  .titles {
    flex: 1;
    min-width: 0;
  }
  .titles :global(h2) {
    font-size: var(--font-size-lg);
  }
  /* The title takes focus so screen readers start at the panel; it isn't a control. */
  .titles :global(h2:focus) {
    outline: none;
  }
  .sub {
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
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
