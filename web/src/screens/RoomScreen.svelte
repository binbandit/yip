<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import type { PendingMessage } from '../lib/state/data';
  import RoomHeader from '../components/RoomHeader.svelte';
  import WorkStrip from '../components/WorkStrip.svelte';
  import MessageList from '../components/MessageList.svelte';
  import Composer from '../components/Composer.svelte';
  import OverviewSummary from '../components/OverviewSummary.svelte';

  interface Props {
    roomId: string;
  }
  let { roomId }: Props = $props();

  const room = $derived(app.data.rooms[roomId]);
  const tl = $derived(app.data.timelines[roomId]);
  const items = $derived(tl ? tl.ids.map((id) => app.data.messages[id]).filter(Boolean) : []);
  const pending = $derived(Object.values(app.data.pending).filter((p) => p.roomId === roomId && !p.threadId));
  const engineersHere = $derived((room?.members ?? []).filter((m) => m.kind === 'engineer'));
  // Where "New" starts: the read position when you opened the room.
  const newAfterSeq = untrack(() => app.data.rooms[roomId]?.lastReadSeq ?? null);

  let error = $state('');
  let composer: Composer | undefined = $state();

  async function load() {
    error = '';
    try {
      await app.loadRoom(roomId);
    } catch (err) {
      error = errorMessage(err);
    }
  }

  onMount(() => {
    void load();
  });

  $effect(() => {
    const epoch = app.resetEpoch;
    if (epoch > 0) untrack(() => void load());
  });

  onDestroy(() => {
    if (app.viewingBottomRoomId === roomId) app.viewingBottomRoomId = null;
  });

  function onBottom(atBottom: boolean) {
    app.viewingBottomRoomId = atBottom ? roomId : null;
    if (atBottom) app.markRead(roomId);
  }

  function editPending(p: PendingMessage) {
    app.discard(p.clientKey);
    composer?.restorePending(p);
  }

  const placeholder = $derived(
    room?.kind === 'overview'
      ? 'Ask where things stand…'
      : room?.kind === 'dm'
        ? `Message ${app.engineerName(engineersHere[0]?.id)}`
        : `Message ${room?.name ?? 'the room'}`,
  );
</script>

{#if !room}
  <div class="screen">
    <div class="screen-inner">
      <h1 class="screen-title" data-screen-title tabindex="-1">This room isn't available</h1>
      <p class="screen-sub">It may have been archived, or you're no longer a member. <a href="/overview">Go to Overview</a>.</p>
    </div>
  </div>
{:else}
  <section class="room" aria-labelledby="room-title">
    <RoomHeader {room} />
    {#if room.kind !== 'overview'}<WorkStrip {roomId} />{/if}
    {#if error}
      <div class="load-error">
        <p class="notice danger" role="alert">{error}</p>
        <button class="btn btn-sm" onclick={load}>Try again</button>
      </div>
    {/if}
    <MessageList
      label="Messages in {room.name}"
      {items}
      {pending}
      loaded={!!tl?.loaded}
      hasMore={!!tl?.hasMore}
      onloadolder={() => app.loadOlder(roomId)}
      {newAfterSeq}
      highlightId={app.loc.panel?.kind === 'thread' ? null : app.loc.msg}
      onreply={room.kind === 'overview' ? undefined : (m) => app.openPanel({ kind: 'thread', id: m.id })}
      onbottomchange={onBottom}
      oneditpending={room.kind === 'overview' ? undefined : editPending}
    >
      {#snippet empty()}
        <div class="empty-room">
          {#if room.kind === 'overview'}
            <p><strong>Your workspace at a glance.</strong></p>
            <p class="muted">Get a fresh summary to see what is moving, what finished and where a question is still open.</p>
          {:else if engineersHere.length === 0}
            <p><strong>Bring a couple of engineers into this room, then tell them what you're working on.</strong></p>
            <button class="btn btn-primary btn-sm" onclick={() => app.openPanel({ kind: 'room', id: roomId })}>Add engineers</button>
          {:else if room.kind === 'dm'}
            {@const name = app.engineerName(engineersHere[0].id)}
            <p><strong>This is your direct conversation with {name}.</strong></p>
            <p class="muted">Everything you write here goes to {name}, who works from the projects they can access.</p>
          {:else}
            <p>
              <strong
                >Tell {engineersHere.length > 3
                  ? 'the team'
                  : new Intl.ListFormat(undefined, { type: 'conjunction' }).format(engineersHere.map((m) => app.engineerName(m.id)))} what you're working on.</strong
              >
            </p>
            <p class="muted">
              Mention someone with @ to ask them directly.
              {#if room.replyMode === 'steward' && room.stewardId}
                {app.engineerName(room.stewardId)} answers messages that don't mention anyone.
              {:else}
                This room is quiet: only the engineers you mention reply.
              {/if}
            </p>
          {/if}
          {#if room.kind === 'room' && room.projectIds.length === 0}
            <p class="muted">You can talk here now. Connect a project when you want the team to inspect or change code.</p>
            <button class="btn btn-sm" onclick={() => app.openPanel({ kind: 'room', id: roomId })}>Connect a project</button>
          {/if}
        </div>
      {/snippet}
    </MessageList>
    <div class="room-composer">
      {#if room.kind === 'overview'}
        <OverviewSummary {roomId} />
      {:else}
        <Composer bind:this={composer} {roomId} {placeholder} />
      {/if}
    </div>
  </section>
{/if}

<style>
  .room {
    flex: 1;
    min-height: 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    container-type: inline-size;
    container-name: room;
  }
  .room-composer {
    flex: none;
  }
  .empty-room {
    display: grid;
    gap: 8px;
    justify-items: start;
    max-width: 560px;
    padding: 32px 28px;
  }
  .load-error {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 12px 16px 0;
  }
</style>
