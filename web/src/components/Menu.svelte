<script lang="ts" module>
  export interface MenuItem {
    label: string;
    icon?: string;
    href?: string;
    danger?: boolean;
    disabled?: boolean;
    onselect?: () => void;
  }
</script>

<script lang="ts">
  // Menu button pattern: arrow keys move, Enter/Space select, Escape and Tab close.
  import type { Snippet } from 'svelte';
  import { pushLayer } from '../lib/ui/layers';
  import Icon from './Icon.svelte';

  interface Props {
    label: string;
    items: MenuItem[];
    trigger: Snippet;
    header?: Snippet;
    align?: 'start' | 'end';
    buttonClass?: string;
    placement?: 'below' | 'above';
  }
  let { label, items, trigger, header, align = 'end', buttonClass = 'icon-btn', placement = 'below' }: Props = $props();

  let open = $state(false);
  let btn: HTMLButtonElement | undefined = $state();
  let menu: HTMLDivElement | undefined = $state();
  const id = `menu-${Math.random().toString(36).slice(2, 8)}`;

  function itemsEls(): HTMLElement[] {
    return menu ? [...menu.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])')] : [];
  }

  $effect(() => {
    if (!open) return;
    const release = pushLayer(() => (open = false), { returnFocus: btn });
    queueMicrotask(() => itemsEls()[0]?.focus());
    const onDoc = (e: MouseEvent) => {
      if (!menu?.contains(e.target as Node) && !btn?.contains(e.target as Node)) open = false;
    };
    document.addEventListener('mousedown', onDoc);
    return () => {
      document.removeEventListener('mousedown', onDoc);
      release(false);
    };
  });

  function close(focusButton = true) {
    open = false;
    if (focusButton) btn?.focus();
  }

  function onKey(e: KeyboardEvent) {
    const els = itemsEls();
    const i = els.indexOf(document.activeElement as HTMLElement);
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      els[(i + 1) % els.length]?.focus();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      els[(i - 1 + els.length) % els.length]?.focus();
    } else if (e.key === 'Home') {
      e.preventDefault();
      els[0]?.focus();
    } else if (e.key === 'End') {
      e.preventDefault();
      els[els.length - 1]?.focus();
    } else if (e.key === 'Tab') {
      open = false;
    }
  }

  function select(item: MenuItem) {
    if (item.disabled) return;
    close(!item.href);
    item.onselect?.();
  }
</script>

<div class="menu-wrap">
  <button
    bind:this={btn}
    class={buttonClass}
    aria-haspopup="menu"
    aria-expanded={open}
    aria-controls={open ? id : undefined}
    aria-label={label}
    onclick={() => (open = !open)}
    onkeydown={(e) => {
      if (e.key === 'ArrowDown' && !open) {
        e.preventDefault();
        open = true;
      }
    }}
  >
    {@render trigger()}
  </button>
  {#if open}
    <!-- svelte-ignore a11y_interactive_supports_focus -->
    <div bind:this={menu} class="menu {align} {placement}" role="menu" id={id} aria-label={label} tabindex="-1" onkeydown={onKey}>
      {#if header}<div class="menu-header">{@render header()}</div>{/if}
      {#each items as item (item.label)}
        {#if item.href}
          <a class="item" class:danger={item.danger} role="menuitem" tabindex="-1" href={item.href} onclick={() => select(item)}>
            {#if item.icon}<Icon name={item.icon} size={16} />{/if}{item.label}
          </a>
        {:else}
          <button
            class="item"
            class:danger={item.danger}
            role="menuitem"
            tabindex="-1"
            aria-disabled={item.disabled}
            onclick={() => select(item)}
          >
            {#if item.icon}<Icon name={item.icon} size={16} />{/if}{item.label}
          </button>
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .menu-wrap {
    position: relative;
    display: inline-flex;
  }
  .menu {
    position: absolute;
    z-index: 500;
    min-width: 200px;
    padding: 6px;
    border-radius: 12px;
    border: 1px solid var(--line);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
    animation: fade var(--t-fast) var(--ease);
  }
  .menu.below {
    top: calc(100% + 6px);
  }
  .menu.above {
    bottom: calc(100% + 6px);
  }
  .menu.end {
    right: 0;
  }
  .menu.start {
    left: 0;
  }
  .menu-header {
    padding: 8px 10px 10px;
    margin-bottom: 4px;
    border-bottom: 1px solid var(--line);
  }
  .item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 36px;
    padding: 0 10px;
    border: 0;
    border-radius: 8px;
    background: none;
    color: var(--ink);
    font-size: 14px;
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .item:hover,
  .item:focus-visible {
    background: var(--hover);
    outline: none;
  }
  .item:focus-visible {
    background: var(--selected);
  }
  .item.danger {
    color: var(--danger);
  }
  .item[aria-disabled='true'] {
    opacity: 0.5;
    cursor: not-allowed;
  }
  @media (pointer: coarse) {
    .item {
      min-height: 44px;
    }
  }
  @keyframes fade {
    from {
      opacity: 0;
      transform: translateY(-2px);
    }
  }
</style>
