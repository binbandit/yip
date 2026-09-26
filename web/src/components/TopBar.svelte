<script lang="ts">
  // Phone-width chrome only: on wider screens the sidebar carries the brand,
  // search, and profile.
  import { app } from '../lib/state/app.svelte';
  import Wordmark from './Wordmark.svelte';
  import Icon from './Icon.svelte';
</script>

<div class="topbar">
  <button class="icon-btn" aria-label="Rooms and navigation" aria-expanded={app.sidebarOpen} onclick={() => (app.sidebarOpen = true)}>
    <Icon name="menu" />
  </button>
  <a class="brand" href="/overview" aria-label="yip — Overview"><Wordmark size={22} showText={false} /></a>
  <button class="search" onclick={() => (app.searchOpen = true)}>
    <Icon name="search" size={15} />
    <span>Search</span>
  </button>
</div>

<style>
  .topbar {
    display: flex;
    align-items: center;
    gap: 6px;
    height: calc(52px + env(safe-area-inset-top));
    padding: env(safe-area-inset-top) max(8px, env(safe-area-inset-right)) 0 max(8px, env(safe-area-inset-left));
  }
  .brand {
    display: inline-flex;
    padding: 0 4px;
    border-radius: 6px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    height: 40px;
    padding: 0 12px;
    border: 0;
    border-radius: var(--r-control);
    background: var(--hover);
    color: color-mix(in srgb, var(--ink) 72%, transparent);
    font-size: 14px;
    cursor: pointer;
  }
</style>
