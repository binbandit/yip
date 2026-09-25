<script lang="ts">
  // The room header always identifies who can answer: members with roles,
  // the reply mode, and linked projects.
  import { app } from '../lib/state/app.svelte';
  import type { Room } from '../lib/api/types.gen';
  import Avatar from './Avatar.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    room: Room;
  }
  let { room }: Props = $props();

  const engineers = $derived(room.members.filter((m) => m.kind === 'engineer').map((m) => app.data.engineers[m.id]).filter(Boolean));
  const projects = $derived(room.projectIds.map((id) => app.data.projects[id]).filter(Boolean));
  const replyText = $derived(
    room.kind === 'overview'
      ? 'Answers come from the work ledger'
      : room.replyMode === 'steward' && room.stewardId
        ? `${app.engineerName(room.stewardId)} answers unaddressed messages`
        : 'Quiet — only mentioned engineers reply',
  );
  const title = $derived(room.kind === 'dm' && engineers[0] ? engineers[0].name : room.kind === 'overview' ? 'Overview conversation' : room.name);
</script>

<header class="room-head">
  <div class="titles">
    <div class="line">
      <h1 id="room-title" data-screen-title tabindex="-1">{title}</h1>
      {#if room.private && room.kind !== 'overview'}
        <span class="private"><Icon name="lock" size={13} />Private</span>
      {/if}
    </div>
    <p class="purpose truncate">
      {#if room.kind === 'dm' && engineers[0]}{engineers[0].role}{:else}{room.purpose}{/if}
    </p>
  </div>

  <div class="facts">
    {#if engineers.length}
      <button class="members" onclick={() => app.openPanel({ kind: 'room', id: room.id })} aria-label="Members: {engineers.map((e) => `${e.name}, ${e.role}`).join('; ')}. Manage room">
        <span class="faces" aria-hidden="true">
          {#each engineers.slice(0, 4) as e (e.id)}<Avatar actor={{ kind: 'engineer', id: e.id }} size={24} />{/each}
        </span>
        <span class="names truncate" aria-hidden="true">
          {#each engineers.slice(0, 3) as e, i (e.id)}{i ? ', ' : ''}<span class="nm">{e.name}</span> <span class="rl">{e.role}</span>{/each}{engineers.length > 3 ? ` +${engineers.length - 3}` : ''}
        </span>
      </button>
    {/if}
    {#each projects as p (p.id)}
      <a class="chip" href="/projects/{p.id}"><Icon name="folder" size={13} />{p.name}</a>
    {/each}
    <span class="reply meta" title="Reply mode">{replyText}</span>
    {#if room.kind !== 'overview'}
      <button class="icon-btn" aria-label="Room settings" onclick={() => app.openPanel({ kind: 'room', id: room.id })}>
        <Icon name="settings" size={18} />
      </button>
    {/if}
  </div>
</header>

<style>
  .room-head {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px 16px;
    flex-wrap: wrap;
    min-height: 60px;
    padding: 10px 12px 10px 24px;
    border-bottom: 1px solid var(--line);
  }
  .titles {
    min-width: 0;
    flex: 1 1 220px;
  }
  .line {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  h1 {
    font-size: var(--text-title);
    font-weight: 680;
  }
  h1:focus-visible {
    outline-offset: 2px;
  }
  .private {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--ink-secondary);
  }
  .purpose {
    font-size: 13.5px;
    color: var(--ink-secondary);
  }
  .facts {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    min-width: 0;
  }
  .members {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    max-width: 420px;
    min-height: 34px;
    padding: 3px 10px 3px 4px;
    border: 1px solid var(--line);
    border-radius: var(--r-pill);
    background: var(--surface);
    color: var(--ink);
    font-size: 13px;
    cursor: pointer;
  }
  .members:hover {
    border-color: var(--control-edge);
  }
  .faces {
    display: inline-flex;
  }
  .faces :global(.avatar + .avatar) {
    margin-left: -6px;
    box-shadow: 0 0 0 2px var(--surface);
  }
  .nm {
    font-weight: 600;
  }
  .rl {
    color: var(--ink-secondary);
  }
  .reply {
    max-width: 280px;
  }
  @media (max-width: 1100px) {
    .names {
      display: none;
    }
    .members {
      padding-right: 6px;
    }
  }
  @media (max-width: 760px) {
    .room-head {
      padding: 8px 8px 8px 16px;
    }
    .reply {
      display: none;
    }
  }
</style>
