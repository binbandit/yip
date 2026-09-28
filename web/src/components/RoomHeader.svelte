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
      ? 'Summaries use recorded work and confirmed outcomes'
      : room.replyMode === 'steward' && room.stewardId
        ? `${app.engineerName(room.stewardId)} answers unaddressed messages`
        : 'Quiet — only mentioned engineers reply',
  );
  const replyShort = $derived(
    room.kind === 'overview' ? 'Recorded updates' : room.replyMode === 'steward' && room.stewardId ? `${app.engineerName(room.stewardId)} answers` : 'Mentions only',
  );
  const membersLabel = $derived(engineers.map((e) => `${e.name}, ${e.role}`).join('; '));
  const title = $derived(room.kind === 'dm' && engineers[0] ? engineers[0].name : room.kind === 'overview' ? 'Workspace summary' : room.name);
</script>

<header class="room-head">
  <div class="titles">
    <h1 id="room-title" data-screen-title tabindex="-1">
      {#if room.kind === 'room'}<span class="hash" aria-hidden="true">{#if room.private}<Icon name="lock" size={14} />{:else}#{/if}</span>{/if}{title}
    </h1>
    {#if room.private && room.kind !== 'overview'}<span class="vh">, private</span>{/if}
    <p class="purpose truncate">
      {#if room.kind === 'dm' && engineers[0]}{engineers[0].role}{:else if room.kind === 'overview'}Across your rooms and projects{:else}{room.purpose}{/if}
    </p>
  </div>

  <div class="facts">
    {#each projects as p (p.id)}
      <a class="head-btn project" href="/projects/{p.id}"><Icon name="folder" size={14} />{p.name}</a>
    {/each}
    <span class="head-btn static" title={replyText}>
      <Icon name={room.replyMode === 'steward' ? 'reply' : 'at'} size={14} /><span aria-hidden="true">{replyShort}</span><span class="vh">{replyText}</span>
    </span>
    {#if engineers.length}
      <button class="head-btn members" onclick={() => app.openPanel({ kind: 'room', id: room.id })} title={membersLabel} aria-label="Members: {membersLabel}. Manage room">
        <span class="faces" aria-hidden="true">
          {#each engineers.slice(0, 3) as e (e.id)}<Avatar actor={{ kind: 'engineer', id: e.id }} size={20} />{/each}
        </span>
        <!-- Who can answer, by name; a count when the room is narrow. -->
        <span class="names" aria-hidden="true">{engineers.slice(0, 3).map((e) => e.name).join(', ')}{engineers.length > 3 ? ` +${engineers.length - 3}` : ''}</span>
        <span class="count" aria-hidden="true">{engineers.length}</span>
      </button>
    {/if}
    {#if room.kind !== 'overview'}
      <button class="icon-btn" aria-label="Room settings" onclick={() => app.openPanel({ kind: 'room', id: room.id })}>
        <Icon name="settings" size={17} />
      </button>
    {/if}
  </div>
</header>

<style>
  .room-head {
    flex: none;
    display: flex;
    align-items: center;
    gap: 12px;
    min-height: 52px;
    padding: 8px 12px 8px 20px;
    border-bottom: 1px solid color-mix(in srgb, var(--line) 80%, transparent);
  }
  .titles {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
    flex: 1;
  }
  h1 {
    display: inline-flex;
    align-items: baseline;
    gap: 2px;
    flex: 0 1 auto;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: var(--text-title);
    font-weight: 600;
    letter-spacing: -0.02em;
    line-height: 24px;
  }
  h1:focus-visible {
    outline-offset: 2px;
  }
  .hash {
    display: inline-flex;
    align-self: center;
    width: 14px;
    margin-right: 4px;
    color: color-mix(in srgb, var(--ink) 45%, transparent);
    font-weight: 500;
  }
  .purpose {
    min-width: 0;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .facts {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
  .head-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 30px;
    padding: 0 10px;
    border: 1px solid var(--line-strong);
    border-radius: var(--r-control);
    background: var(--surface);
    color: var(--ink);
    font-size: 13px;
    font-weight: 500;
    text-decoration: none;
    white-space: nowrap;
    cursor: pointer;
  }
  .head-btn:hover {
    background: var(--surface-subtle);
  }
  .head-btn.static {
    border-color: transparent;
    color: var(--ink-secondary);
    cursor: default;
  }
  .head-btn.static:hover {
    background: none;
  }
  .members {
    padding-left: 5px;
    max-width: 280px;
  }
  .names {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .count {
    display: none;
  }
  @container room (max-width: 820px) {
    .names {
      display: none;
    }
    .count {
      display: inline;
    }
  }
  .faces {
    display: inline-flex;
  }
  .faces :global(.avatar + .avatar) {
    margin-left: -5px;
    box-shadow: 0 0 0 2px var(--surface);
  }
  @media (max-width: 1100px) {
    .purpose {
      display: none;
    }
  }
  @media (max-width: 760px) {
    .room-head {
      padding: 8px 8px 8px 16px;
    }
    .head-btn.static,
    .head-btn.project {
      display: none;
    }
  }
</style>
