<script lang="ts">
  // The frame: one tinted gradient behind the sidebar, with one opaque work
  // card inset beside it. Panes inside the card are split by hairlines; the
  // right panel is the one contextual drawer (inline at ≥1200px, overlaid
  // below, full screen <760px).
  import { app } from '../lib/state/app.svelte';
  import TopBar from '../components/TopBar.svelte';
  import Icon from '../components/Icon.svelte';
  import Sidebar from '../components/Sidebar.svelte';
  import Sheet from '../components/Sheet.svelte';
  import PanelHost from '../components/PanelHost.svelte';
  import SearchDialog from '../components/SearchDialog.svelte';
  import CreateRoomDialog from '../components/CreateRoomDialog.svelte';
  import OverviewScreen from './OverviewScreen.svelte';
  import RoomScreen from './RoomScreen.svelte';
  import EngineersScreen from './EngineersScreen.svelte';
  import EngineerScreen from './EngineerScreen.svelte';
  import ProjectsScreen from './ProjectsScreen.svelte';
  import ProjectScreen from './ProjectScreen.svelte';
  import MachinesScreen from './MachinesScreen.svelte';
  import SettingsScreen from './SettingsScreen.svelte';
  import type { PanelMode } from '../components/RightPanel.svelte';

  const route = $derived(app.loc.route);
  const panel = $derived(app.loc.panel);
  const mode: PanelMode = $derived(app.narrow ? 'full' : app.viewport >= 1200 ? 'inline' : 'overlay');
  const modal = $derived(!!panel && mode !== 'inline');
  const connectionText = $derived(
    app.connection === 'offline' || !app.online
      ? "Can't reach your workspace"
      : app.connection === 'reconnecting'
        ? 'Reconnecting…'
        : app.connection === 'connecting'
          ? 'Connecting…'
          : '',
  );

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
  let mainEl: HTMLElement | undefined = $state();
  let lastPath = '';
  $effect(() => {
    const path = JSON.stringify(route);
    if (lastPath && lastPath !== path && !app.loc.msg && !app.loc.panel) {
      queueMicrotask(() => {
        const h = mainEl?.querySelector<HTMLElement>('[data-screen-title]');
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
        case 'overview':
          return 'Overview';
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
        case 'settings':
          return 'Settings';
      }
      return org;
    };
    document.title = `${titleFor()} · yip`;
  });
</script>

<a class="skip-link" href="#main">Skip to content</a>
<div class="shell" class:narrow={app.narrow}>
  {#if app.narrow}
    <header class="top" inert={modal || app.sidebarOpen}>
      <TopBar />
    </header>
  {/if}

  {#if !app.narrow}
    <nav class="side" aria-label="Workspace" inert={modal}>
      <Sidebar />
    </nav>
  {/if}

  <div class="frame">
    {#if connectionText}
      <p class="conn" role="status" inert={modal}>
        <Icon name={app.connection === 'offline' || !app.online ? 'wifiOff' : 'refresh'} size={14} />
        {connectionText}
        {#if app.connection !== 'connecting'}<button class="btn btn-sm btn-quiet" onclick={() => app.retryConnection()}>Try now</button>{/if}
      </p>
    {/if}
    <main id="main" class="card" bind:this={mainEl} tabindex="-1">
      <div class="content" inert={modal}>
        {#if route.name === 'overview'}
          <OverviewScreen />
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
        {:else if route.name === 'settings'}
          <SettingsScreen />
        {:else}
          <div class="screen">
            <div class="screen-inner">
              <h1 class="screen-title" data-screen-title tabindex="-1">That page doesn't exist</h1>
              <p class="screen-sub">It may have been moved or archived. <a href="/overview">Go to Overview</a>.</p>
            </div>
          </div>
        {/if}
      </div>
      {#if panel}
        {#key panel.kind + ':' + panel.id}
          <PanelHost {panel} {mode} />
        {/key}
      {/if}
    </main>
  </div>
</div>

{#if app.narrow && app.sidebarOpen}
  <Sheet label="Rooms and navigation" onclose={() => (app.sidebarOpen = false)}>
    <Sidebar />
  </Sheet>
{/if}

{#if app.searchOpen}
  <SearchDialog onclose={() => (app.searchOpen = false)} />
{/if}
{#if app.createRoom}
  <CreateRoomDialog kind={app.createRoom.kind} onclose={() => (app.createRoom = null)} />
{/if}

<style>
  .shell {
    display: grid;
    grid-template-columns: var(--sidebar-w) minmax(0, 1fr);
    grid-template-rows: minmax(0, 1fr);
    grid-template-areas: 'side frame';
    height: 100%;
    background: var(--frame);
  }
  .top {
    grid-area: top;
    min-width: 0;
  }
  .side {
    grid-area: side;
    min-height: 0;
  }
  .frame {
    grid-area: frame;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    padding: 8px 8px 8px 0;
  }
  .conn {
    display: flex;
    flex: none;
    align-items: center;
    gap: 8px;
    min-height: 26px;
    padding: 0 8px 6px 6px;
    font-size: 12px;
    margin-left: auto;
    font-weight: 500;
    color: var(--ink);
  }
  .card {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    background: var(--surface);
    border-radius: var(--r-surface);
    box-shadow: var(--shadow-card);
    overflow: hidden;
  }
  .card:focus-visible {
    outline: none;
  }
  .content {
    flex: 1;
    min-width: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .narrow {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto minmax(0, 1fr);
    grid-template-areas:
      'top'
      'frame';
  }
  .narrow .frame {
    padding: 0;
  }
  .narrow .card {
    border-radius: var(--r-surface) var(--r-surface) 0 0;
  }
  .narrow .conn {
    padding: 0 12px 6px;
  }
</style>
