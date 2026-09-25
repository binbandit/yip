<script lang="ts">
  // Navigation over the aqua frame. Unread is shown by weight, mentions by a
  // count pill, and an engineer's active run by a quiet "working" bar.
  import { app } from '../lib/state/app.svelte';
  import { workingInRoom } from '../lib/state/data';
  import { roomsWithDrafts } from '../lib/state/drafts';
  import Icon from './Icon.svelte';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';

  const route = $derived(app.loc.route);
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

  const health = $derived.by(() => {
    if (nodes.length === 0) return { shape: 'circle' as const, tone: 'attention' as const, text: 'No machines paired yet · Add one to run work' };
    if (online.length === nodes.length)
      return {
        shape: 'check-filled' as const,
        tone: 'success' as const,
        text: `${online.length} ${online.length === 1 ? 'machine' : 'machines'} connected · work continues when you close this`,
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

  function workingNames(roomId: string): string[] {
    return workingInRoom(app.data, roomId).map((id) => app.engineerName(id));
  }
</script>

<div class="sidebar">
  <div class="scroll">
    <ul class="list primary">
      <li>
        <a class="row" href="/overview" aria-current={route.name === 'overview' ? 'page' : undefined}>
          <Icon name="overview" size={17} />
          <span class="label">Overview</span>
        </a>
      </li>
    </ul>

    <section aria-labelledby="nav-rooms">
      <div class="section-row">
        <h2 id="nav-rooms">Rooms</h2>
        <button class="icon-btn add" aria-label="Create a room" onclick={() => { app.sidebarOpen = false; app.createRoom = { kind: 'room' }; }}>
          <Icon name="plus" size={16} />
        </button>
      </div>
      {#if rooms.length === 0}
        <p class="hint">No rooms yet. Create one and bring a couple of engineers in.</p>
      {:else}
        <ul class="list">
          {#each rooms as r (r.id)}
            {@const working = workingNames(r.id)}
            {@const unread = r.unreadCount > 0 && !isCurrentRoom(r.id)}
            <li>
              <a class="row" class:unread href="/rooms/{r.id}" aria-current={isCurrentRoom(r.id) ? 'page' : undefined}>
                <span class="glyph" aria-hidden="true">{#if r.private}<Icon name="lock" size={15} />{:else}<Icon name="hash" size={15} />{/if}</span>
                <span class="label truncate">{r.name}</span>
                {#if r.private}<span class="vh">, private</span>{/if}
                {#if unread}<span class="vh">, {r.unreadCount} unread</span>{/if}
                {#if drafts.has(r.id) && !isCurrentRoom(r.id)}
                  <span class="draft" title="Draft saved"><Icon name="pencil" size={13} /><span class="vh">, draft saved</span></span>
                {/if}
                {#if working.length}
                  <span class="working" title="{working.join(', ')} working">
                    <StateIcon shape="bar" tone="accent" size={13} live />
                    <span class="vh">, {working.join(' and ')} working</span>
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
        <button class="icon-btn add" aria-label="Message an engineer directly" onclick={() => { app.sidebarOpen = false; app.createRoom = { kind: 'dm' }; }}>
          <Icon name="plus" size={16} />
        </button>
      </div>
      {#if dms.length === 0}
        <p class="hint">Talk one-to-one with an engineer.</p>
      {:else}
        <ul class="list">
          {#each dms as r (r.id)}
            {@const eng = dmEngineer(r.id)}
            {@const working = workingNames(r.id)}
            {@const unread = r.unreadCount > 0 && !isCurrentRoom(r.id)}
            <li>
              <a class="row" class:unread href="/rooms/{r.id}" aria-current={isCurrentRoom(r.id) ? 'page' : undefined}>
                {#if eng}<Avatar actor={{ kind: 'engineer', id: eng.id }} size={20} />{:else}<span class="glyph"><Icon name="reply" size={15} /></span>{/if}
                <span class="label truncate">{eng?.name ?? r.name}</span>
                {#if unread}<span class="vh">, {r.unreadCount} unread</span>{/if}
                {#if working.length}
                  <span class="working" title="{working.join(', ')} working"><StateIcon shape="bar" tone="accent" size={13} live /><span class="vh">, working</span></span>
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

    <section aria-label="Team and setup">
      <ul class="list primary">
        <li>
          <a class="row" href="/engineers" aria-current={route.name === 'engineers' || route.name === 'engineer' ? 'page' : undefined}>
            <Icon name="users" size={17} /><span class="label">Engineers</span>
          </a>
        </li>
        <li>
          <a class="row" href="/projects" aria-current={route.name === 'projects' || route.name === 'project' ? 'page' : undefined}>
            <Icon name="folder" size={17} /><span class="label">Projects</span>
          </a>
        </li>
        <li>
          <a class="row" href="/machines" aria-current={route.name === 'machines' ? 'page' : undefined}>
            <Icon name="machine" size={17} /><span class="label">Machines</span>
          </a>
        </li>
      </ul>
    </section>
  </div>

  <a class="health" href="/machines">
    <StateIcon shape={health.shape} tone={health.tone} size={13} />
    <span>{health.text}</span>
  </a>
</div>

<style>
  .sidebar {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 4px 10px 12px 12px;
    scrollbar-width: thin;
  }
  section {
    margin-top: 18px;
  }
  .section-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 2px 2px 8px;
  }
  h2 {
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--ink-secondary);
  }
  .add {
    width: 28px;
    height: 28px;
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
    gap: 9px;
    min-height: 32px;
    padding: 0 8px;
    border-radius: var(--r-control);
    color: var(--ink-secondary);
    font-size: var(--text-nav);
    font-weight: 450;
    text-decoration: none;
    transition: background-color var(--t-fast) var(--ease);
  }
  .row:hover {
    background: var(--hover);
    color: var(--ink);
  }
  .row[aria-current='page'] {
    background: var(--surface);
    color: var(--ink);
    font-weight: 560;
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--line) 70%, transparent);
  }
  .row.unread {
    color: var(--ink);
    font-weight: 700;
  }
  .primary .row {
    color: var(--ink);
  }
  .label {
    flex: 1;
    min-width: 0;
  }
  .glyph {
    display: inline-grid;
    place-items: center;
    width: 20px;
    color: var(--ink-secondary);
  }
  .working,
  .draft {
    display: inline-flex;
    align-items: center;
    color: var(--ink-secondary);
  }
  .hint {
    padding: 2px 8px;
    font-size: var(--text-meta);
    color: var(--ink-secondary);
  }
  .health {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin: 0 10px 10px 12px;
    padding: 9px 10px;
    border-radius: var(--r-control);
    font-size: 12.5px;
    line-height: 1.4;
    color: var(--ink-secondary);
    text-decoration: none;
  }
  .health :global(svg) {
    margin-top: 2px;
  }
  .health:hover {
    background: var(--hover);
    color: var(--ink);
  }
  @media (pointer: coarse) {
    .row {
      min-height: 44px;
    }
    .add {
      width: 44px;
      height: 44px;
    }
  }
</style>
