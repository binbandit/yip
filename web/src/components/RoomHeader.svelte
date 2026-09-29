<script lang="ts">
  // The room header always identifies who can answer: members with roles,
  // the reply mode, and linked projects.
  import { AtSign, Folder, Lock, MessageSquareReply, Settings } from '@lucide/svelte';
  import { AvatarGroup, Button, Icon, IconButton, Text, Tooltip, VisuallyHidden } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import type { Room } from '../lib/api/types.gen';
  import Avatar from './Avatar.svelte';

  interface Props {
    room: Room;
  }
  let { room }: Props = $props();

  const engineers = $derived(room.members.filter((m) => m.kind === 'engineer').map((m) => app.data.engineers[m.id]).filter(Boolean));
  const projects = $derived(room.projectIds.map((id) => app.data.projects[id]).filter(Boolean));
  const replyText = $derived(
    room.replyMode === 'steward' && room.stewardId
      ? `${app.engineerName(room.stewardId)} answers unaddressed messages`
      : 'Quiet — only mentioned engineers reply',
  );
  const replyShort = $derived(room.replyMode === 'steward' && room.stewardId ? `${app.engineerName(room.stewardId)} answers` : 'Mentions only');
  const membersLabel = $derived(engineers.map((e) => `${e.name}, ${e.role}`).join('; '));
  const title = $derived(room.kind === 'dm' && engineers[0] ? engineers[0].name : room.name);
</script>

<header class="room-head">
  <div class="titles">
    <h1 id="room-title" data-screen-title tabindex="-1">
      {#if room.kind === 'room'}<span class="hash" aria-hidden="true">{#if room.private}<Icon icon={Lock} size="sm" />{:else}#{/if}</span>{/if}{title}
    </h1>
    {#if room.private}<VisuallyHidden>, private</VisuallyHidden>{/if}
    <Text as="p" type="supporting" maxLines={1} class="purpose">
      {#if room.kind === 'dm' && engineers[0]}{engineers[0].role}{:else}{room.purpose}{/if}
    </Text>
  </div>

  <div class="facts">
    {#each projects as p (p.id)}
      <Button class="project" variant="secondary" size="sm" href={workspaceUrl(`/projects/${p.id}`)} label={p.name}>
        {#snippet icon()}<Icon icon={Folder} size="sm" />{/snippet}
      </Button>
    {/each}
    <Tooltip content={replyText}>
      <span class="reply-mode">
        <Icon icon={room.replyMode === 'steward' ? MessageSquareReply : AtSign} size="sm" /><span aria-hidden="true">{replyShort}</span><VisuallyHidden>{replyText}</VisuallyHidden>
      </span>
    </Tooltip>
    {#if engineers.length}
      <Button
        class="members"
        variant="secondary"
        size="sm"
        label="Members: {membersLabel}. Manage room"
        tooltip={membersLabel}
        onclick={() => app.openPanel({ kind: 'room', id: room.id })}
      >
        <span class="members-row">
          <span class="faces" aria-hidden="true">
            <AvatarGroup size={20} shape="rounded">
              {#each engineers.slice(0, 3) as e (e.id)}<Avatar actor={{ kind: 'engineer', id: e.id }} size={20} />{/each}
            </AvatarGroup>
          </span>
          <!-- Who can answer, by name; a count when the room is narrow. -->
          <span class="names" aria-hidden="true">{engineers.slice(0, 3).map((e) => e.name).join(', ')}{engineers.length > 3 ? ` +${engineers.length - 3}` : ''}</span>
          <span class="count" aria-hidden="true">{engineers.length}</span>
        </span>
      </Button>
    {/if}
    <IconButton label="Room settings" tooltip="Room settings" variant="ghost" size="sm" onclick={() => app.openPanel({ kind: 'room', id: room.id })}>
      {#snippet icon()}<Icon icon={Settings} size="sm" />{/snippet}
    </IconButton>
  </div>
</header>

<style>
  .room-head {
    flex: none;
    display: flex;
    align-items: center;
    gap: var(--spacing-3);
    min-height: var(--yip-pane-header-h);
    padding: var(--spacing-2) var(--spacing-3) var(--spacing-2) var(--spacing-5);
    border-bottom: 1px solid color-mix(in srgb, var(--color-border) 80%, transparent);
  }
  .titles {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
    flex: 1;
  }
  /* The title keeps its width while the purpose gives way; if even the title
     doesn't fit, it ends in an ellipsis (which needs a block, not a flex box). */
  h1 {
    flex: 0 1 auto;
    min-width: 0;
    margin: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: var(--font-size-lg);
    font-weight: var(--font-weight-semibold);
    letter-spacing: -0.02em;
    line-height: 24px;
  }
  /* The title takes focus after navigation so screen readers start there; it isn't a control. */
  h1:focus {
    outline: none;
  }
  .hash {
    display: inline-flex;
    vertical-align: -2px;
    margin-right: var(--spacing-1);
    color: color-mix(in srgb, var(--color-text-primary) 45%, transparent);
    font-weight: var(--font-weight-medium);
  }
  .titles :global(.purpose) {
    flex: 1 1 0;
    min-width: 0;
  }
  .facts {
    display: flex;
    align-items: center;
    gap: var(--spacing-1-5);
    flex: none;
  }
  .reply-mode {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    padding: 0 10px;
    color: var(--color-text-secondary);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-medium);
    white-space: nowrap;
  }
  .facts :global(.members) {
    max-width: 280px;
    padding-left: 5px;
  }
  .members-row {
    display: flex;
    align-items: center;
    gap: var(--spacing-1-5);
    min-width: 0;
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
    flex: none;
  }
  @media (max-width: 1100px) {
    .titles :global(.purpose) {
      display: none;
    }
  }
  @media (max-width: 768px) {
    .room-head {
      padding: var(--spacing-2) var(--spacing-2) var(--spacing-2) var(--spacing-4);
    }
    .reply-mode,
    .facts :global(.project) {
      display: none;
    }
  }
</style>
