<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import Wordmark from './Wordmark.svelte';
  import Icon from './Icon.svelte';
  import Avatar from './Avatar.svelte';
  import Menu from './Menu.svelte';

  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  const shown = $derived(app.effectiveTheme());
  const connectionText = $derived(
    app.connection === 'offline' || !app.online
      ? "Can't reach your workspace"
      : app.connection === 'reconnecting'
        ? 'Reconnecting…'
        : app.connection === 'connecting'
          ? 'Connecting…'
          : '',
  );

  function toggleAppearance() {
    void app.setPreferences({ theme: shown === 'night' ? 'day' : 'night' });
  }
</script>

<div class="topbar">
  {#if app.narrow}
    <button class="icon-btn" aria-label="Rooms and navigation" aria-expanded={app.sidebarOpen} onclick={() => (app.sidebarOpen = true)}>
      <Icon name="menu" />
    </button>
  {/if}
  <a class="brand" href="/overview" aria-label="yip — Overview"><Wordmark size={24} showText={!app.narrow} /></a>

  <button class="search" onclick={() => (app.searchOpen = true)} aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}>
    <Icon name="search" size={16} />
    <span class="search-text">{app.narrow ? 'Search' : 'Search rooms, people and work'}</span>
    {#if !app.narrow}<kbd aria-hidden="true">{isMac ? '⌘' : 'Ctrl'} K</kbd>{/if}
  </button>

  <div class="right">
    {#if connectionText}
      <span class="conn" role="status">
        <Icon name={app.connection === 'offline' || !app.online ? 'wifiOff' : 'refresh'} size={15} />
        <span class="conn-text">{connectionText}</span>
      </span>
    {/if}
    <button
      class="icon-btn"
      aria-label={shown === 'night' ? 'Use day appearance' : 'Use night appearance'}
      title={shown === 'night' ? 'Day appearance' : 'Night appearance'}
      onclick={toggleAppearance}
    >
      <Icon name={shown === 'night' ? 'sun' : 'moon'} />
    </button>
    <Menu
      label="Your profile"
      buttonClass="profile-btn"
      items={[
        { label: 'Settings', icon: 'settings', href: '/settings' },
        { label: 'Sign out', icon: 'logout', onselect: () => void app.signOut() },
      ]}
    >
      {#snippet trigger()}
        {#if app.me}<Avatar actor={{ kind: 'user', id: app.me.id }} size={28} />{/if}
      {/snippet}
      {#snippet header()}
        <div class="who">
          <strong>{app.me?.name}</strong>
          <span class="meta">@{app.me?.handle} · {app.data.org?.name}</span>
        </div>
      {/snippet}
    </Menu>
  </div>
</div>

<style>
  .topbar {
    display: flex;
    align-items: center;
    gap: 12px;
    height: var(--topbar-h);
    padding: 0 12px 0 16px;
  }
  .brand {
    display: inline-flex;
    align-items: center;
    width: calc(var(--sidebar-w) - 28px);
    text-decoration: none;
    color: var(--ink);
    border-radius: 6px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    max-width: 560px;
    min-width: 0;
    height: 34px;
    padding: 0 8px 0 12px;
    border: 1px solid color-mix(in srgb, var(--line) 80%, transparent);
    border-radius: var(--r-control);
    background: color-mix(in srgb, var(--surface) 70%, transparent);
    color: var(--ink-secondary);
    font-size: 14px;
    cursor: pointer;
    transition: background-color var(--t-fast) var(--ease);
  }
  .search:hover {
    background: var(--surface);
  }
  .search-text {
    flex: 1;
    text-align: left;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .right {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-left: auto;
  }
  .conn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--ink-secondary);
    padding: 0 8px;
  }
  :global(.profile-btn) {
    display: inline-grid;
    place-items: center;
    width: 36px;
    height: 36px;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: none;
    cursor: pointer;
  }
  .who {
    display: grid;
    gap: 2px;
  }
  @media (max-width: 760px) {
    .topbar {
      gap: 6px;
      padding: 0 8px;
    }
    .brand {
      width: auto;
    }
    .conn-text {
      display: none;
    }
    .search {
      height: 40px;
    }
  }
  @media (pointer: coarse) {
    :global(.profile-btn) {
      width: 44px;
      height: 44px;
    }
  }
</style>
