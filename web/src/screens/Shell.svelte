<script lang="ts">
  // The frame: Astryx's elevated AppShell over yip's tinted gradient, with the
  // sidebar on the frame and one opaque work card beside it. Panes inside the
  // card are split by hairlines; the right panel is the one contextual drawer
  // (inline at ≥1200px, overlaid below, full screen on phones). Below the
  // AppShell breakpoint the sidebar moves into its drawer behind a top bar.
  import { tick, untrack } from 'svelte';
  import { AppShell, Button, Icon, Link } from '@astryx-svelte/core';
  import { RefreshCw, WifiOff } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import Sidebar from '../components/Sidebar.svelte';
  import PanelHost from '../components/PanelHost.svelte';
  import SearchDialog from '../components/SearchDialog.svelte';
  import CreateRoomDialog from '../components/CreateRoomDialog.svelte';
  import RenameRoomDialog from '../components/RenameRoomDialog.svelte';
  import ArchiveRoomDialog from '../components/ArchiveRoomDialog.svelte';
  import type { Room } from '../lib/api/types.gen';
  import StartScreen from './StartScreen.svelte';
  import RoomScreen from './RoomScreen.svelte';
  import EngineersScreen from './EngineersScreen.svelte';
  import EngineerScreen from './EngineerScreen.svelte';
  import ProjectsScreen from './ProjectsScreen.svelte';
  import ProjectScreen from './ProjectScreen.svelte';
  import MachinesScreen from './MachinesScreen.svelte';
  import ConnectionsScreen from './ConnectionsScreen.svelte';
  import WorkspaceSettingsScreen from './WorkspaceSettingsScreen.svelte';
  import SettingsScreen from './SettingsScreen.svelte';
  import Screen from '../components/Screen.svelte';
  import type { PanelMode } from '../components/RightPanel.svelte';

  // The sidebar remounts across responsive layouts; active room actions and
  // their drafts belong to the stable shell, just like the create dialog.
  let renaming = $state<Room | null>(null);
  let archiving = $state<Room | null>(null);

  $effect(() => {
    // A new data snapshot invalidates old room actions; a layout change does not.
    void app.data;
    return () => { renaming = null; archiving = null; };
  });

  const route = $derived(app.loc.route);
  const panel = $derived(app.loc.panel);
  const mode: PanelMode = $derived(app.narrow ? 'full' : app.viewport >= 1200 ? 'inline' : 'overlay');
  const modal = $derived(!!panel && mode !== 'inline');
  const offline = $derived(app.connection === 'offline' || !app.online);
  const connectionText = $derived(
    offline
      ? "Can't reach your workspace"
      : app.connection === 'reconnecting'
        ? 'Reconnecting…'
        : app.connection === 'connecting'
          ? 'Connecting…'
          : '',
  );

  $effect(() => {
    if (route.name === 'home') untrack(() => app.navigate(app.homePath(), { replace: true }));
  });

  $effect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        app.searchOpen = true;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  // Move focus to the new screen after navigation (not on panel/tab changes).
  let workEl: HTMLElement | undefined = $state();
  async function focusAfterRoomAction() {
    await tick();
    // The invoker may have left the DOM during a resize or after archiving.
    if (document.activeElement === document.body) workEl?.querySelector<HTMLElement>('[data-screen-title]')?.focus({ preventScroll: true });
  }
  let lastPath = '';
  $effect(() => {
    const path = JSON.stringify(route);
    if (lastPath && lastPath !== path && !app.loc.msg && !app.loc.panel) {
      queueMicrotask(() => {
        const h = workEl?.querySelector<HTMLElement>('[data-screen-title]');
        h?.focus({ preventScroll: true });
      });
    }
    lastPath = path;
  });

  $effect(() => {
    const titleFor = () => {
      const org = app.data.org?.name ?? 'yip';
      switch (route.name) {
        case 'room':
          return app.data.rooms[route.roomId]?.name ?? 'Room';
        case 'start':
          return 'Getting started';
        case 'engineers':
          return 'Engineers';
        case 'engineer':
          return app.data.engineers[route.id]?.name ?? 'Engineer';
        case 'projects':
          return 'Projects';
        case 'project':
          return app.data.projects[route.id]?.name ?? 'Project';
        case 'machines':
          return 'Machines';
        case 'connections':
          return 'Connections';
        case 'settings':
          return 'Settings';
        case 'workspace-settings':
          return 'Workspace settings';
      }
      return org;
    };
    document.title = `${titleFor()} · ${app.data.org?.name ?? 'Workspace'} · yip`;
  });
</script>

<!--
  Behind an overlaid panel the inline sidebar is inert. On phones the sidebar
  lives in its own modal drawer instead, which must never be inert, and while a
  full-screen panel is open the top bar (and with it the drawer's toggle) steps
  aside so the panel's Back button is the one way out.
-->
{#snippet sideNav()}
  <Sidebar inert={modal && !app.narrow} onRenameRoom={(room) => { renaming = room; }} onArchiveRoom={(room) => { archiving = room; }} />
{/snippet}

{#snippet banner()}
  <p class="conn" role="status" inert={modal}>
    <Icon icon={offline ? WifiOff : RefreshCw} size="xsm" />
    {connectionText}
    {#if app.connection !== 'connecting'}<Button label="Try now" variant="ghost" size="sm" onclick={() => app.retryConnection()} />{/if}
  </p>
{/snippet}

<AppShell
  class="yip-shell yip-frame {modal ? 'modal' : ''}"
  variant="elevated"
  {sideNav}
  banner={connectionText ? banner : undefined}
  mobileNav={{ isOpen: app.sidebarOpen, onOpenChange: (open) => (app.sidebarOpen = open), hasToggle: !modal }}
>
  <div class="work" bind:this={workEl}>
    <div class="content" inert={modal}>
      {#if route.name === 'start'}
        <StartScreen />
      {:else if route.name === 'room'}
        {#key route.roomId}
          <RoomScreen roomId={route.roomId} />
        {/key}
      {:else if route.name === 'engineers'}
        <EngineersScreen />
      {:else if route.name === 'engineer'}
        {#key route.id}<EngineerScreen id={route.id} />{/key}
      {:else if route.name === 'projects'}
        <ProjectsScreen />
      {:else if route.name === 'project'}
        {#key route.id}<ProjectScreen id={route.id} />{/key}
      {:else if route.name === 'machines'}
        <MachinesScreen />
      {:else if route.name === 'connections'}
        <ConnectionsScreen provider={route.provider} />
      {:else if route.name === 'workspace-settings'}
        <WorkspaceSettingsScreen />
      {:else if route.name === 'settings'}
        <SettingsScreen />
      {:else if route.name !== 'home'}
        <Screen title="That page doesn't exist">
          {#snippet subtitle()}It may have been moved or archived. <Link hasUnderline href={workspaceUrl('/')}>Go to your workspace</Link>.{/snippet}
        </Screen>
      {/if}
    </div>
    {#if panel}
      {#key panel.kind + ':' + panel.id}
        <PanelHost {panel} {mode} />
      {/key}
    {/if}
  </div>
</AppShell>

{#if app.searchOpen}
  <SearchDialog onclose={() => (app.searchOpen = false)} />
{/if}
{#if app.createRoom}
  <CreateRoomDialog kind={app.createRoom.kind} onclose={() => (app.createRoom = null)} />
{/if}
{#if renaming}
  {#key renaming.id}<RenameRoomDialog room={renaming} onclose={() => { renaming = null; void focusAfterRoomAction(); }} />{/key}
{/if}
{#if archiving}
  {#key archiving.id}<ArchiveRoomDialog room={archiving} onclose={() => { archiving = null; void focusAfterRoomAction(); }} />{/key}
{/if}

<style>
  /* The frame shows through the navigation areas; only the work card is opaque. */
  :global(.yip-shell .astryx-app-shell-header),
  :global(.yip-shell .astryx-app-shell-header div:has(> .conn)),
  :global(.yip-shell .astryx-app-shell-sidenav) {
    background: transparent;
  }
  /*
   * The one opaque work card, inset from the frame. (AppShell's elevated card
   * needs a TopNav; yip has only the sidebar, so the inset is drawn here.)
   */
  :global(.yip-shell [role='main']) {
    margin: var(--spacing-2) var(--spacing-2) var(--spacing-2) 0;
    height: calc(100% - 2 * var(--spacing-2));
    border-radius: var(--radius-container);
    box-shadow: var(--shadow-low);
    overflow: hidden;
  }
  @media (max-width: 768px) {
    :global(.yip-shell [role='main']) {
      margin: 0;
      height: 100%;
      border-radius: var(--radius-container) var(--radius-container) 0 0;
    }
    /* A full-screen panel is the whole screen: no top bar, so no card edge. */
    :global(.yip-shell.modal [role='main']) {
      border-radius: 0;
    }
  }
  /* Keep the chrome clear of a phone's notch (the page uses viewport-fit=cover). */
  :global(.yip-shell) {
    padding: env(safe-area-inset-top) env(safe-area-inset-right) 0 env(safe-area-inset-left);
  }
  .conn {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: var(--spacing-2);
    min-height: 26px;
    padding: var(--spacing-1) var(--spacing-3) 0;
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-medium);
    color: var(--color-text-primary);
  }
  .work {
    position: relative;
    display: flex;
    height: 100%;
    min-height: 0;
  }
  .content {
    flex: 1;
    min-width: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
</style>
