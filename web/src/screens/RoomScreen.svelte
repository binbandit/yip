<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import type { PendingMessage } from '../lib/state/data';
  import { Button, Link, Text } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import RoomHeader from '../components/RoomHeader.svelte';
  import Screen from '../components/Screen.svelte';
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
  <Screen title="This room isn't available">
    {#snippet subtitle()}It may have been archived, or you're no longer a member. <Link href="/overview" hasUnderline>Go to Overview</Link>.{/snippet}
  </Screen>
{:else}
  <section class="room" aria-labelledby="room-title">
    <RoomHeader {room} />
    {#if room.kind !== 'overview'}<WorkStrip {roomId} />{/if}
    {#if error}
      <div class="load-error">
        <Notice tone="danger" role="alert"><p>{error} <Link onclick={load} type="inherit" color="inherit" hasUnderline>Try again</Link></p></Notice>
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
            <Text as="p"><strong>Your workspace at a glance.</strong></Text>
            <Text as="p" color="secondary">Get a fresh summary to see what is moving, what finished and where a question is still open.</Text>
          {:else if engineersHere.length === 0}
            <Text as="p"><strong>Bring a couple of engineers into this room, then tell them what you're working on.</strong></Text>
            <Button label="Add engineers" variant="primary" size="sm" onclick={() => app.openPanel({ kind: 'room', id: roomId })} />
          {:else}
            <Text as="p">
              <strong>Tell {engineersHere.length > 3 ? 'the team' : engineersHere.map((m) => app.engineerName(m.id)).join(' and ')} what you're working on.</strong>
            </Text>
            <Text as="p" color="secondary">
              Mention someone with @ to ask them directly.
              {#if room.replyMode === 'steward' && room.stewardId}
                {app.engineerName(room.stewardId)} answers messages that don't mention anyone.
              {:else}
                This room is quiet: only the engineers you mention reply.
              {/if}
            </Text>
          {/if}
          {#if room.kind === 'room' && room.projectIds.length === 0}
            <Text as="p" color="secondary">You can talk here now. Connect a project when you want the team to inspect or change code.</Text>
            <Button label="Connect a project" size="sm" onclick={() => app.openPanel({ kind: 'room', id: roomId })} />
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
    gap: var(--spacing-2);
    justify-items: start;
    max-width: 560px;
    padding: var(--spacing-8) var(--spacing-7);
  }
  strong {
    font-weight: var(--font-weight-semibold);
  }
  .load-error {
    padding: var(--spacing-3) var(--spacing-4) 0;
  }
</style>
