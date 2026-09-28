<script lang="ts">
  // Navigation over the tinted frame (no box of its own). Unread is shown by
  // weight, mentions by a count pill, and an engineer's active run by an
  // elapsed-time "working" pill. Selection is a grey wash, never a colour.
  import { app } from '../lib/state/app.svelte';
  import { roomsWithDrafts } from '../lib/state/drafts';
  import Icon from './Icon.svelte';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';
  import Wordmark from './Wordmark.svelte';
  import Menu from './Menu.svelte';

  const route = $derived(app.loc.route);
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  const rooms = $derived(
    app.rooms.filter((r) => r.kind === 'room').sort((a, b) => a.name.localeCompare(b.name)),
  );
  const dms = $derived(app.rooms.filter((r) => r.kind === 'dm').sort((a, b) => a.name.localeCompare(b.name)));
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
        // Name where work runs: it keeps going when this window closes, but
        // not if the named machine itself sleeps.
        text: `${online.length} ${online.length === 1 ? 'machine' : 'machines'} connected · work runs on ${online.map((n) => n.name).join(', ')}, not in this window`,
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
  function working(roomId: string): { ids: string[]; label: string } | null {
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
    const elapsed = mins < 1 ? 'just started' : mins < 60 ? `for ${mins} min` : `for ${Math.floor(mins / 60)} h`;
    const names = [...ids].map((id) => app.engineerName(id));
    return { ids: [...ids], label: `${names.join(' and ')} working, ${elapsed}` };
  }

  function go() {
    app.sidebarOpen = false;
  }
</script>

<div class="sidebar">
  <div class="head">
    <a class="brand" href="/overview" aria-label="yip — Overview" onclick={go}><Wordmark size={22} /></a>
  </div>
  <button class="search" onclick={() => { go(); app.searchOpen = true; }} aria-keyshortcuts={isMac ? 'Meta+K' : 'Control+K'}>
    <Icon name="search" size={15} />
    <span class="search-text">Search everything</span>
    <kbd aria-hidden="true">{isMac ? '⌘' : 'Ctrl'} K</kbd>
  </button>

  <div class="scroll">
    <ul class="list">
      <li>
        <a class="row nav" href="/overview" onclick={go} aria-current={route.name === 'overview' ? 'page' : undefined}>
          <Icon name="overview" size={16} /><span class="label">Overview</span>
        </a>
      </li>
      <li>
        <a class="row nav" href="/engineers" onclick={go} aria-current={route.name === 'engineers' || route.name === 'engineer' ? 'page' : undefined}>
          <Icon name="users" size={16} /><span class="label">Engineers</span>
        </a>
      </li>
      <li>
        <a class="row nav" href="/projects" onclick={go} aria-current={route.name === 'projects' || route.name === 'project' ? 'page' : undefined}>
          <Icon name="folder" size={16} /><span class="label">Projects</span>
        </a>
      </li>
      <li>
        <a class="row nav" href="/machines" onclick={go} aria-current={route.name === 'machines' ? 'page' : undefined}>
          <Icon name="machine" size={16} /><span class="label">Machines</span>
        </a>
      </li>
    </ul>

    <section aria-labelledby="nav-rooms">
      <div class="section-row">
        <h2 id="nav-rooms">Rooms</h2>
        <button class="icon-btn add" aria-label="Create a room" onclick={() => { go(); app.createRoom = { kind: 'room' }; }}>
          <Icon name="plus" size={15} />
        </button>
      </div>
      {#if rooms.length === 0}
        <p class="hint">No rooms yet. Create one and bring a couple of engineers in.</p>
      {:else}
        <ul class="list">
          {#each rooms as r (r.id)}
            {@const w = working(r.id)}
            {@const unread = r.unreadCount > 0 && !isCurrentRoom(r.id)}
            <li>
              <a class="row" class:unread href="/rooms/{r.id}" onclick={go} aria-current={isCurrentRoom(r.id) ? 'page' : undefined}>
                <span class="glyph" aria-hidden="true">{#if r.private}<Icon name="lock" size={14} />{:else}<Icon name="hash" size={15} />{/if}</span>
                <span class="label truncate">{r.name}</span>
                {#if r.private}<span class="vh">, private</span>{/if}
                {#if app.isMuted(r.id)}<span class="muted-mark" title="Muted"><Icon name="bellOff" size={13} /><span class="vh">, muted</span></span>{/if}
                {#if unread}<span class="vh">, {r.unreadCount} unread</span>{/if}
                {#if drafts.has(r.id) && !isCurrentRoom(r.id)}
                  <span class="draft" title="Draft saved"><Icon name="pencil" size={13} /><span class="vh">, draft saved</span></span>
                {/if}
                {#if w}
                  <span class="working" title={w.label}>
                    {#each w.ids.slice(0, 3) as id (id)}<Avatar actor={{ kind: 'engineer', id }} size={16} />{/each}
                    <span class="vh">, {w.label}</span>
                  </span>
                {/if}
                {#if r.mentionCount > 0 && !isCurrentRoom(r.id)}
                  <span class="count-pill" aria-hidden="true">{r.mentionCount}</span>
                  <span class="vh">, {r.mentionCount} {r.mentionCount === 1 ? 'mention' : 'mentions'}</span>
                {/if}
              </a>
            </li>
          {/each}
        </ul>
      {/if}
    </section>

    <section aria-labelledby="nav-dms">
      <div class="section-row">
        <h2 id="nav-dms">Direct messages</h2>
        <button class="icon-btn add" aria-label="Message an engineer directly" onclick={() => { go(); app.createRoom = { kind: 'dm' }; }}>
          <Icon name="plus" size={15} />
        </button>
      </div>
      {#if dms.length === 0}
        <p class="hint">Talk one-to-one with an engineer.</p>
      {:else}
        <ul class="list">
          {#each dms as r (r.id)}
            {@const eng = dmEngineer(r.id)}
            {@const w = working(r.id)}
            {@const unread = r.unreadCount > 0 && !isCurrentRoom(r.id)}
            <li>
              <a class="row" class:unread href="/rooms/{r.id}" onclick={go} aria-current={isCurrentRoom(r.id) ? 'page' : undefined}>
                {#if eng}<Avatar actor={{ kind: 'engineer', id: eng.id }} size={22} />{:else}<span class="glyph"><Icon name="reply" size={15} /></span>{/if}
                <span class="label truncate">{eng?.name ?? r.name}</span>
                {#if unread}<span class="vh">, {r.unreadCount} unread</span>{/if}
                {#if drafts.has(r.id) && !isCurrentRoom(r.id)}
                  <span class="draft" title="Draft saved"><Icon name="pencil" size={13} /><span class="vh">, draft saved</span></span>
                {/if}
                {#if w}
                  <span class="working-dm" title={w.label}>working<span class="vh">, {w.label}</span></span>
                {/if}
                {#if r.mentionCount > 0 && !isCurrentRoom(r.id)}
                  <span class="count-pill" aria-hidden="true">{r.mentionCount}</span>
                  <span class="vh">, {r.mentionCount} {r.mentionCount === 1 ? 'mention' : 'mentions'}</span>
                {/if}
              </a>
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  </div>

  <div class="foot">
    <a class="health" href="/machines" onclick={go}>
      <StateIcon shape={health.shape} tone={health.tone} size={13} />
      <span>{health.text}</span>
    </a>
    <Menu
      label="Your profile"
      buttonClass="profile"
      placement="above"
      align="start"
      items={[
        { label: 'Settings', icon: 'settings', href: '/settings' },
        {
          label: shown === 'night' ? 'Day appearance' : 'Night appearance',
          icon: shown === 'night' ? 'sun' : 'moon',
          onselect: () => void app.setPreferences({ theme: shown === 'night' ? 'day' : 'night' }),
        },
        { label: 'Sign out', icon: 'logout', onselect: () => void app.signOut() },
      ]}
    >
      {#snippet trigger()}
        {#if app.me}<Avatar actor={{ kind: 'user', id: app.me.id }} size={30} />{/if}
        <span class="who">
          <strong class="truncate">{app.me?.name ?? 'You'}</strong>
          <span class="truncate">{app.data.org?.name ?? 'yip'}</span>
        </span>
      {/snippet}
    </Menu>
  </div>
</div>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
    padding: 10px 8px 8px 10px;
  }
  .head {
    display: flex;
    align-items: center;
    height: 36px;
    padding: 0 8px;
  }
  .brand {
    display: inline-flex;
    align-items: center;
    color: var(--ink);
    text-decoration: none;
    border-radius: 6px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    height: 32px;
    margin: 6px 0 4px;
    padding: 0 6px 0 10px;
    border: 0;
    border-radius: var(--r-row);
    background: var(--hover);
    color: color-mix(in srgb, var(--ink) 72%, transparent);
    font-size: 13px;
    cursor: pointer;
    transition: background-color var(--t-fast) var(--ease);
  }
  .search:hover {
    background: var(--pressed);
  }
  .search-text {
    flex: 1;
    text-align: left;
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 4px 0 12px;
    scrollbar-width: thin;
  }
  section {
    margin-top: 14px;
  }
  .section-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 32px;
    padding: 0 2px 0 8px;
  }
  h2 {
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  .add {
    width: 26px;
    height: 26px;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 1px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 32px;
    padding: 0 8px;
    border-radius: var(--r-row);
    color: var(--ink);
    opacity: 0.82;
    font-size: var(--text-nav);
    font-weight: 400;
    text-decoration: none;
    transition:
      background-color 100ms var(--ease),
      opacity 100ms var(--ease);
  }
  .row:hover {
    background: var(--hover);
    opacity: 1;
  }
  .row[aria-current='page'] {
    background: var(--selected);
    opacity: 1;
    font-weight: 500;
  }
  .row.unread {
    opacity: 1;
    font-weight: 650;
  }
  .row.nav {
    opacity: 1;
  }
  .label {
    flex: 1;
    min-width: 0;
  }
  .glyph {
    display: inline-grid;
    place-items: center;
    width: 18px;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  .muted-mark {
    display: inline-flex;
    color: var(--ink-tertiary, var(--ink-secondary));
  }
  .draft {
    display: inline-flex;
    align-items: center;
    color: color-mix(in srgb, var(--ink) 55%, transparent);
  }
  /* Who is working in the room, as small overlapping avatars. */
  .working {
    display: inline-flex;
    flex: none;
  }
  .working > :global(* + *) {
    margin-left: -4px;
  }
  .working > :global(*) {
    box-shadow: 0 0 0 1.5px var(--navigation);
    border-radius: 50%;
  }
  .working-dm {
    flex: none;
    font-size: 11px;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  .hint {
    padding: 2px 8px;
    font-size: var(--text-meta);
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  .foot {
    display: grid;
    gap: 2px;
    padding-top: 6px;
  }
  .health {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    min-height: 30px;
    padding: 6px 8px;
    line-height: 1.35;
    border-radius: var(--r-row);
    font-size: 12px;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
    text-decoration: none;
  }
  .health:hover {
    background: var(--hover);
    color: var(--ink);
  }
  .health :global(svg) {
    flex: none;
    margin-top: 2px;
  }
  :global(.profile) {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 46px;
    padding: 6px 8px;
    border: 0;
    border-radius: var(--r-control);
    background: none;
    color: var(--ink);
    text-align: left;
    cursor: pointer;
  }
  :global(.profile:hover),
  :global(.profile[aria-expanded='true']) {
    background: var(--hover);
  }
  .who {
    display: grid;
    flex: 1;
    min-width: 0;
    line-height: 1.25;
  }
  .who strong {
    font-size: 13px;
    font-weight: 600;
  }
  .who span {
    font-size: 12px;
    color: color-mix(in srgb, var(--ink) 72%, transparent);
  }
  @media (pointer: coarse) {
    .row,
    .search {
      min-height: 44px;
    }
    .add {
      width: 44px;
      height: 44px;
    }
  }
</style>
