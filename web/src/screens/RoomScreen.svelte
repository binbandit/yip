<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import type { PendingMessage } from '../lib/state/data';
  import RoomHeader from '../components/RoomHeader.svelte';
  import WorkStrip from '../components/WorkStrip.svelte';
  import MessageList from '../components/MessageList.svelte';
  import Composer from '../components/Composer.svelte';

  interface Props {
    roomId: string;
  }
  let { roomId }: Props = $props();

  const room = $derived(app.data.rooms[roomId]);
  const tl = $derived(app.data.timelines[roomId]);
  const items = $derived(tl ? tl.ids.map((id) => app.data.messages[id]).filter(Boolean) : []);
  const pending = $derived(Object.values(app.data.pending).filter((p) => p.roomId === roomId && !p.threadId));
  const streams = $derived(Object.values(app.data.streams).filter((s) => s.roomId === roomId && !s.threadId));
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
    composer?.fill(
      p.body,
      p.mentions
        .filter((m) => m.kind === 'engineer' && app.data.engineers[m.id])
        .map((m) => ({ kind: 'engineer' as const, id: m.id, handle: app.data.engineers[m.id].handle })),
    );
    if (p.jobId) app.steer[receiptKey(roomId)] = p.jobId;
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
      {streams}
      loaded={!!tl?.loaded}
      hasMore={!!tl?.hasMore}
      onloadolder={() => app.loadOlder(roomId)}
      {newAfterSeq}
      highlightId={app.loc.panel?.kind === 'thread' ? null : app.loc.msg}
      onreply={(m) => app.openPanel({ kind: 'thread', id: m.id })}
      onbottomchange={onBottom}
      oneditpending={editPending}
    >
      {#snippet empty()}
        <div class="empty-room">
          {#if room.kind === 'overview'}
            <p><strong>Ask “Where are we with everything?”</strong></p>
            <p class="muted">The hub answers from the work ledger and its confirmed timestamps — nobody is woken up to report status.</p>
            <button class="btn btn-sm" onclick={() => composer?.fill('Where are we with everything?')}>Use this question</button>
          {:else if engineersHere.length === 0}
            <p><strong>Bring a couple of engineers into this room, then tell them what you're working on.</strong></p>
            <button class="btn btn-primary btn-sm" onclick={() => app.openPanel({ kind: 'room', id: roomId })}>Add engineers</button>
          {:else}
            <p>
              <strong>Tell {engineersHere.map((m) => app.engineerName(m.id)).join(' and ')} what you're working on.</strong>
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
          {/if}
        </div>
      {/snippet}
    </MessageList>
    <div class="room-composer">
      <Composer bind:this={composer} {roomId} {placeholder} />
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
