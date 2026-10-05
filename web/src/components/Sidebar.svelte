<script lang="ts">
  // Navigation over the tinted frame (no box of its own). Unread is shown by
  // weight, mentions by a count pill, and an engineer's active run by an
  // elapsed-time "working" pill. Selection is a grey wash, never a colour.
  // Every marker has a visually hidden text equivalent.
  import { Badge, ContextMenu, DropdownMenu, Icon, IconButton, Kbd, SideNav, SideNavItem, SideNavSection, Text, Tooltip, VisuallyHidden, useSideNavRenderMode, type DropdownMenuOption } from '@astryx-svelte/core';
  import { tick } from 'svelte';
  import { BellOff, Ellipsis, Folder, GripVertical, Hash, ListChecks, Lock, LogOut, MessageSquare, Monitor, Moon, Pencil, Plug, Plus, Search, Settings, Sun, Users } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { roomsWithDrafts } from '../lib/state/drafts';
  import { workspaceBase, workspaceUrl } from '../lib/workspace';
  import { moveRoom, type RoomOrderKind } from '../lib/util/room-order';
  import type { DataState } from '../lib/state/data';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';
  import WorkspaceSwitcher from './WorkspaceSwitcher.svelte';
  import type { Room } from '../lib/api/types.gen';

  /** Set while a modal panel covers the work card. */
  let { inert = false, onRenameRoom, onArchiveRoom }: {
    inert?: boolean;
    onRenameRoom: (room: Room) => void;
    onArchiveRoom: (room: Room) => void;
  } = $props();

  // AppShell renders this sidebar inline, or on phones twice: as the top bar
  // (brand and a search icon) and inside the drawer (everything else).
  const renderMode = useSideNavRenderMode();

  // The drawer opens on the page you're on rather than on its own frame.
  $effect(() => {
    if (renderMode() !== 'drawer' || !app.sidebarOpen) return;
    const frame = requestAnimationFrame(() => document.querySelector<HTMLElement>('dialog[open] [aria-current="page"]')?.focus());
    return () => cancelAnimationFrame(frame);
  });

  const route = $derived(app.loc.route);
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  const rooms = $derived(app.orderedRooms('room'));
  const dms = $derived(app.orderedRooms('dm'));
  const instanceId = $props.id();
  const orderHelp = `${instanceId}-order-help`;
  let dragging = $state<{ id: string; kind: RoomOrderKind; data: DataState; base: string } | null>(null);
  let dropTarget = $state<{ id: string; after: boolean } | null>(null);
  // Moving a keyed row can blur its handle while the same DOM node is moved.
  // Restore only that focused handle, never a different control or workspace.
  $effect.pre(() => {
    void rooms; void dms;
    const data = app.data;
    const base = workspaceBase();
    const focused = document.activeElement;
    if (!(focused instanceof HTMLElement) || !focused.matches('.drag-handle') || focused.getAttribute('aria-describedby') !== orderHelp) return;
    void tick().then(() => {
      if (data === app.data && base === workspaceBase() && focused.isConnected && document.activeElement === document.body) focused.focus({ preventScroll: true });
    });
  });
  // Re-read drafts whenever the route changes (drafts are saved as you type).
  const drafts = $derived.by(() => {
    void app.loc;
    return roomsWithDrafts();
  });

  const nodes = $derived(Object.values(app.data.nodes).filter((n) => !n.revokedAt && n.status !== 'revoked'));
  const online = $derived(nodes.filter((n) => n.status === 'online'));
  const shown = $derived(app.effectiveTheme());

  const health = $derived.by(() => {
    if (nodes.length === 0) return { shape: 'circle' as const, tone: 'attention' as const, text: 'No machines paired yet' };
    if (online.length === nodes.length)
      return {
        shape: 'check-filled' as const,
        tone: 'success' as const,
        text: online.length === 1 ? `${online[0].name} connected` : `${online.length} machines connected`,
      };
    if (online.length === 0) return { shape: 'pause' as const, tone: 'attention' as const, text: `No machines connected · ${nodes.length} paired` };
    return { shape: 'pause' as const, tone: 'attention' as const, text: `${online.length} of ${nodes.length} machines connected` };
  });

  function isCurrentRoom(id: string) {
    return route.name === 'room' && route.roomId === id;
  }

  function dmEngineer(roomId: string) {
    const r = app.data.rooms[roomId];
    const m = r?.members.find((x) => x.kind === 'engineer');
    return m ? app.data.engineers[m.id] : undefined;
  }

  // Engineers mid-run in a room, and how long the oldest run has been going.
  function working(roomId: string): { names: string[]; elapsed: string } | null {
    const ids = new Set<string>();
    let since = Infinity;
    for (const r of Object.values(app.data.runs)) {
      if (r.destination?.roomId !== roomId) continue;
      if (r.state !== 'running' && r.state !== 'preparing' && r.state !== 'awaiting_input') continue;
      ids.add(r.engineerId);
      const t = Date.parse(r.startedAt ?? r.createdAt);
      if (!Number.isNaN(t)) since = Math.min(since, t);
    }
    if (!ids.size) return null;
    const mins = Number.isFinite(since) ? Math.max(0, Math.floor((app.now - since) / 60000)) : 0;
    const elapsed = mins < 1 ? 'now' : mins < 60 ? `${mins}m` : `${Math.floor(mins / 60)}h`;
    return { names: [...ids].map((id) => app.engineerName(id)), elapsed: ids.size > 1 ? `${elapsed} (${ids.size})` : elapsed };
  }

  function go() {
    app.sidebarOpen = false;
  }

  function roomActions(room: Room): DropdownMenuOption[] {
    return [
      { label: 'Rename…', onClick: () => onRenameRoom(room) },
      { label: 'Room settings', onClick: () => { go(); app.openPanel({ kind: 'room', id: room.id }); } },
      { label: app.isMuted(room.id) ? 'Unmute notifications' : 'Mute notifications', onClick: () => app.toggleMute(room.id) },
      ...orderActions(room),
      { type: 'divider' },
      { label: 'Archive…', variant: 'destructive', onClick: () => onArchiveRoom(room) },
    ];
  }

  function orderActions(room: Room): DropdownMenuOption[] {
    if (!app.data.roomOrder) return [];
    const kind = room.kind as RoomOrderKind;
    const list = kind === 'room' ? rooms : dms;
    const index = list.findIndex((r) => r.id === room.id);
    const saving = app.roomOrderSaving(kind);
    return [
      { label: 'Move up', isDisabled: saving || index <= 0, onClick: () => stepRoom(room, -1) },
      { label: 'Move down', isDisabled: saving || index < 0 || index >= list.length - 1, onClick: () => stepRoom(room, 1) },
    ];
  }

  function stepRoom(room: Room, direction: -1 | 1) {
    const kind = room.kind as RoomOrderKind;
    const ids = (kind === 'room' ? rooms : dms).map((r) => r.id);
    const index = ids.indexOf(room.id);
    if (index < 0) return;
    const target = ids[index + direction];
    if (target) void app.saveRoomOrder(kind, moveRoom(ids, room.id, target, direction === 1));
  }

  function startDrag(event: DragEvent, room: Room) {
    const kind = room.kind as RoomOrderKind;
    if (!app.data.roomOrder || app.roomOrderSaving(kind) || !event.dataTransfer) { event.preventDefault(); return; }
    dragging = { id: room.id, kind, data: app.data, base: workspaceBase() };
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', room.id);
  }

  function canDrop(room: Room): boolean {
    return !!dragging && dragging.data === app.data && dragging.base === workspaceBase()
      && dragging.kind === room.kind && dragging.id !== room.id && !app.roomOrderSaving(dragging.kind);
  }

  function dragOver(event: DragEvent, room: Room) {
    if (!canDrop(room)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    dropTarget = { id: room.id, after: event.clientY >= rect.top + rect.height / 2 };
  }

  function dropRoom(event: DragEvent, room: Room) {
    if (canDrop(room) && dragging && dropTarget?.id === room.id) {
      event.preventDefault();
      const ids = (dragging.kind === 'room' ? rooms : dms).map((r) => r.id);
      void app.saveRoomOrder(dragging.kind, moveRoom(ids, dragging.id, room.id, dropTarget.after));
    }
    dragging = null;
    dropTarget = null;
  }
</script>

{#snippet dragHandle(room: Room, name: string)}
  {#if app.data.roomOrder}
    <button class="drag-handle" aria-label="Reorder {name}" aria-describedby={orderHelp}
      aria-disabled={app.roomOrderSaving(room.kind as RoomOrderKind)} draggable={!app.roomOrderSaving(room.kind as RoomOrderKind)}
      ondragstart={(e) => startDrag(e, room)} ondragend={() => { dragging = null; dropTarget = null; }}
      onclick={() => { app.announcement = 'Drag to reorder, use the up and down arrow keys, or choose Move up or Move down in Actions.'; }}
      onkeydown={(e) => { if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); stepRoom(room, e.key === 'ArrowUp' ? -1 : 1); } }}>
      <Icon icon={GripVertical} size="sm" />
    </button>
  {/if}
{/snippet}

{#snippet header()}
  <WorkspaceSwitcher compact={renderMode() === 'topbar'} />
{/snippet}

{#snippet search()}
  <button class="search" onclick={() => { go(); app.searchOpen = true; }} aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}>
    <Icon icon={Search} size="sm" />
    <span class="search-text">Search everything</span>
    <span class="shortcut" aria-hidden="true"><Kbd keys="mod+k" /></span>
  </button>
{/snippet}

{#snippet searchIcon()}
  <IconButton label="Search" variant="ghost" onclick={() => { go(); app.searchOpen = true; }}>
    {#snippet icon()}<Icon icon={Search} size="sm" />{/snippet}
  </IconButton>
{/snippet}

{#snippet footer()}
  <div class="foot">
    <a class="health" href={workspaceUrl('/machines')} onclick={go}>
      <StateIcon shape={health.shape} tone={health.tone} size={13} />
      <span>{health.text}</span>
    </a>
    <DropdownMenu
      button={{ label: 'Your profile', variant: 'ghost', size: 'lg', width: '100%', class: 'profile', children: profile }}
      hasChevron={false}
      placement="above"
      alignment="start"
      items={[
        { label: 'Settings', icon: settingsIcon, onClick: () => { go(); app.navigate('/settings'); } },
        { label: 'Getting started', icon: startIcon, onClick: () => { go(); app.navigate('/start'); } },
        {
          label: shown === 'night' ? 'Day appearance' : 'Night appearance',
          icon: shown === 'night' ? sunIcon : moonIcon,
          onClick: () => void app.setPreferences({ theme: shown === 'night' ? 'day' : 'night' }),
        },
        { label: 'Sign out', icon: signOutIcon, onClick: () => void app.signOut() },
      ]}
    />
  </div>
{/snippet}

{#snippet profile()}
  <span class="profile-content">
    {#if app.me}<Avatar actor={{ kind: 'user', id: app.me.id }} size={32} />{/if}
    <span class="who">
      <strong>{app.me?.name ?? 'You'}</strong>
      <span>{app.data.org?.name ?? 'yip'}</span>
    </span>
  </span>
{/snippet}

{#snippet settingsIcon()}<Icon icon={Settings} size="sm" />{/snippet}
{#snippet sunIcon()}<Icon icon={Sun} size="sm" />{/snippet}
{#snippet moonIcon()}<Icon icon={Moon} size="sm" />{/snippet}
{#snippet signOutIcon()}<Icon icon={LogOut} size="sm" />{/snippet}
{#snippet startIcon()}<Icon icon={ListChecks} size="sm" />{/snippet}
{#snippet engineersIcon()}<Icon icon={Users} size="sm" color="secondary" />{/snippet}
{#snippet projectsIcon()}<Icon icon={Folder} size="sm" color="secondary" />{/snippet}
{#snippet machinesIcon()}<Icon icon={Monitor} size="sm" color="secondary" />{/snippet}
{#snippet connectionsIcon()}<Icon icon={Plug} size="sm" color="secondary" />{/snippet}
{#snippet roomIcon()}<Icon icon={Hash} size="sm" color="secondary" />{/snippet}
{#snippet privateRoomIcon()}<Icon icon={Lock} size="sm" color="secondary" />{/snippet}
{#snippet dmIcon()}<Icon icon={MessageSquare} size="sm" color="secondary" />{/snippet}
{#snippet moreIcon()}<Icon icon={Ellipsis} size="sm" />{/snippet}

{#snippet markers(r: (typeof app.rooms)[number], w: ReturnType<typeof working>, current: boolean, workingText: string)}
  {#if r.private}<VisuallyHidden>, private</VisuallyHidden>{/if}
  {#if app.isMuted(r.id)}
    <Tooltip content="Muted"><span class="mark"><Icon icon={BellOff} size="xsm" /></span></Tooltip>
    <VisuallyHidden>, muted</VisuallyHidden>
  {/if}
  {#if r.unreadCount > 0 && !current}<VisuallyHidden>, {r.unreadCount} unread</VisuallyHidden>{/if}
  {#if drafts.has(r.id) && !current}
    <Tooltip content="Draft saved"><span class="mark"><Icon icon={Pencil} size="xsm" /></span></Tooltip>
    <VisuallyHidden>, draft saved</VisuallyHidden>
  {/if}
  {#if w}
    <Tooltip content="{w.names.join(', ')} working"><span class="working" aria-hidden="true">{w.elapsed}</span></Tooltip>
    <VisuallyHidden>, {workingText}</VisuallyHidden>
  {/if}
  {#if r.mentionCount > 0 && !current}
    <span class="count" aria-hidden="true"><Badge label={String(r.mentionCount)} /></span>
    <VisuallyHidden>, {r.mentionCount} {r.mentionCount === 1 ? 'mention' : 'mentions'}</VisuallyHidden>
  {/if}
{/snippet}

<!--
  On phones the header and footer icons form AppShell's top bar (brand, search,
  menu); the drawer it opens shows the full search launcher instead.
-->
<SideNav
  aria-label="Workspace"
  class="side"
  {inert}
  {header}
  topContent={search}
  footerIcons={renderMode() === 'topbar' ? searchIcon : undefined}
  {footer}
>
  <VisuallyHidden id={orderHelp}>Drag to reorder, or use the up and down arrow keys. Move up and Move down are also in Actions.</VisuallyHidden>
  <SideNavSection title="Pages" isHeaderHidden>
    <SideNavItem label="Engineers" icon={engineersIcon} href={workspaceUrl('/engineers')} onclick={go} isSelected={route.name === 'engineers' || route.name === 'engineer'} />
    <SideNavItem label="Projects" icon={projectsIcon} href={workspaceUrl('/projects')} onclick={go} isSelected={route.name === 'projects' || route.name === 'project'} />
    <SideNavItem label="Machines" icon={machinesIcon} href={workspaceUrl('/machines')} onclick={go} isSelected={route.name === 'machines'} />
    <SideNavItem label="Workspace settings" icon={settingsIcon} href={workspaceUrl('/settings/workspace')} onclick={go} isSelected={route.name === 'workspace-settings'} />
    <SideNavItem label="Connections" icon={connectionsIcon} href={workspaceUrl('/connections')} onclick={go} isSelected={route.name === 'connections'} />
  </SideNavSection>

  <SideNavSection title="Rooms">
    {#snippet endContent()}
      <IconButton label="Create a room" variant="ghost" size="sm" onclick={() => { go(); app.createRoom = { kind: 'room' }; }}>
        {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
      </IconButton>
    {/snippet}
    {#if rooms.length === 0}
      <p class="hint"><Text type="supporting">No rooms yet. Create one and bring a couple of engineers in.</Text></p>
    {:else}
      {#each rooms as r (r.id)}
        {@const w = working(r.id)}
        {@const current = isCurrentRoom(r.id)}
        {@const items = roomActions(r)}
        <div role="group" class="order-row" class:drop-before={dropTarget?.id === r.id && !dropTarget.after} class:drop-after={dropTarget?.id === r.id && dropTarget.after}
          ondragover={(e) => dragOver(e, r)} ondrop={(e) => dropRoom(e, r)}>
        <ContextMenu {items} label="Actions for {r.name}" menuWidth={220}>
          <SideNavItem
            label={r.name}
            icon={r.private ? privateRoomIcon : roomIcon}
            href={workspaceUrl(`/rooms/${r.id}`)}
            onclick={go}
            isSelected={current}
            class="yip-room {r.unreadCount > 0 && !current ? 'unread' : ''}"
          >
            {#snippet endContent()}{@render markers(r, w, current, `${w?.names.join(' and ')} working`)}{/snippet}
            {#snippet actions()}
              {@render dragHandle(r, r.name)}
              <DropdownMenu
                {items}
                button={{ label: `Actions for ${r.name}`, icon: moreIcon, isIconOnly: true, variant: 'ghost', size: 'sm' }}
                hasChevron={false}
                menuWidth={220}
              />
            {/snippet}
          </SideNavItem>
        </ContextMenu>
        </div>
      {/each}
    {/if}
  </SideNavSection>

  <SideNavSection title="Direct messages">
    {#snippet endContent()}
      <IconButton label="Message an engineer directly" variant="ghost" size="sm" onclick={() => { go(); app.createRoom = { kind: 'dm' }; }}>
        {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
      </IconButton>
    {/snippet}
    {#if dms.length === 0}
      <p class="hint"><Text type="supporting">Talk one-to-one with an engineer.</Text></p>
    {:else}
      {#each dms as r (r.id)}
        {@const eng = dmEngineer(r.id)}
        {@const w = working(r.id)}
        {@const current = isCurrentRoom(r.id)}
        {@const items = orderActions(r)}
        {@const label = eng?.name ?? r.name}
        {#snippet avatarIcon()}{#if eng}<Avatar actor={{ kind: 'engineer', id: eng.id }} size={20} />{/if}{/snippet}
        <div role="group" class="order-row" class:drop-before={dropTarget?.id === r.id && !dropTarget.after} class:drop-after={dropTarget?.id === r.id && dropTarget.after}
          ondragover={(e) => dragOver(e, r)} ondrop={(e) => dropRoom(e, r)}>
        <ContextMenu {items} label="Actions for {label}" menuWidth={220} isDisabled={!app.data.roomOrder}>
        <SideNavItem
          {label}
          icon={eng ? avatarIcon : dmIcon}
          href={workspaceUrl(`/rooms/${r.id}`)}
          onclick={go}
          isSelected={current}
          class="yip-room {r.unreadCount > 0 && !current ? 'unread' : ''}"
        >
          {#snippet endContent()}{@render markers(r, w, current, 'working')}{/snippet}
          {#snippet actions()}
            {@render dragHandle(r, label)}
            {#if app.data.roomOrder}
              <DropdownMenu {items} button={{ label: `Actions for ${label}`, icon: moreIcon, isIconOnly: true, variant: 'ghost', size: 'sm' }} hasChevron={false} menuWidth={220} />
            {/if}
          {/snippet}
        </SideNavItem>
        </ContextMenu>
        </div>
      {/each}
    {/if}
  </SideNavSection>
</SideNav>

<style>
  .order-row { position: relative; }
  .drop-before::before, .drop-after::after {
    content: ''; position: absolute; inset-inline: var(--spacing-2); height: 2px;
    background: var(--color-text-primary); pointer-events: none; z-index: 1;
  }
  .drop-before::before { top: 0; }
  .drop-after::after { bottom: 0; }
  .drag-handle {
    display: inline-flex; align-items: center; justify-content: center;
    width: var(--size-element-sm); height: var(--size-element-sm);
    color: var(--color-icon-secondary); border-radius: var(--radius-element); cursor: grab;
  }
  .drag-handle:hover { background: var(--color-overlay-hover); }
  .drag-handle:focus-visible { outline: 2px solid var(--color-text-primary); outline-offset: -2px; }
  .drag-handle:active { cursor: grabbing; }
  .drag-handle[aria-disabled='true'] { cursor: wait; opacity: .5; }
  .search {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    width: 100%;
    height: var(--size-element-md);
    padding: 0 var(--spacing-1-5) 0 var(--spacing-2);
    border-radius: var(--radius-element);
    background: var(--color-overlay-hover);
    color: var(--color-text-secondary);
    font-size: var(--font-size-sm);
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .search:hover {
    background: var(--color-overlay-pressed);
  }
  .search-text {
    flex: 1;
    text-align: start;
  }
  .hint {
    padding: var(--spacing-0-5) var(--spacing-2);
  }
  .mark {
    display: inline-flex;
    color: var(--color-icon-secondary);
  }
  .working {
    padding: 1px var(--spacing-1-5);
    border-radius: var(--radius-full);
    background: var(--color-overlay-pressed);
    color: var(--color-text-primary);
    font-size: 11px;
    font-weight: var(--font-weight-medium);
    font-variant-numeric: tabular-nums;
  }
  /* Mentions are a monochrome count pill: ink on the accent. */
  .count :global(.astryx-badge) {
    background: var(--color-accent);
    color: var(--color-on-accent);
    font-variant-numeric: tabular-nums;
  }
  /* Read rooms recede; unread rooms are heavier, never a different colour. */
  :global(.yip-room a) {
    color: color-mix(in srgb, var(--color-text-primary) 82%, transparent);
  }
  :global(.yip-room a[aria-current='page']),
  :global(.yip-room a:hover) {
    color: var(--color-text-primary);
  }
  :global(.yip-room.unread a) {
    color: var(--color-text-primary);
    font-weight: var(--font-weight-semibold);
  }
  .foot {
    display: grid;
    gap: var(--spacing-0-5);
  }
  .health {
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-2);
    padding: var(--spacing-1-5) var(--spacing-2);
    border-radius: var(--radius-element);
    font-size: var(--font-size-sm);
    line-height: 1.35;
    color: var(--color-text-secondary);
    text-decoration: none;
  }
  .health:hover {
    background: var(--color-overlay-hover);
    color: var(--color-text-primary);
  }
  .health :global(svg) {
    flex: none;
    margin-top: 2px;
  }
  .profile-content {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    width: 100%;
    min-width: 0;
    text-align: start;
  }
  .who {
    display: grid;
    flex: 1;
    min-width: 0;
    line-height: 1.25;
  }
  .who strong,
  .who span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .who strong {
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    color: var(--color-text-primary);
  }
  .who span {
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-normal);
    color: var(--color-text-secondary);
  }
  :global(.profile.astryx-button) {
    justify-content: flex-start;
  }
  @media (pointer: coarse) {
    .search {
      min-height: 44px;
    }
    .shortcut {
      display: none;
    }
  }
</style>
