<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { app } from '../../lib/state/app.svelte';
  import { errorMessage } from '../../lib/api/client';
  import type { PendingMessage } from '../../lib/state/data';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import MessageList from '../MessageList.svelte';
  import MessageRow from '../MessageRow.svelte';
  import Composer from '../Composer.svelte';

  interface Props {
    rootId: string;
    mode: PanelMode;
  }
  let { rootId, mode }: Props = $props();

  const root = $derived(app.data.messages[rootId]);
  const th = $derived(app.data.threads[rootId]);
  const replies = $derived(th ? th.ids.map((id) => app.data.messages[id]).filter(Boolean) : []);
  const roomId = $derived(root?.roomId ?? (app.loc.route.name === 'room' ? app.loc.route.roomId : ''));
  const pending = $derived(Object.values(app.data.pending).filter((p) => p.threadId === rootId));
  const room = $derived(roomId ? app.data.rooms[roomId] : undefined);
  let error = $state('');
  let composer: Composer | undefined = $state();

  async function load() {
    error = '';
    try {
      await app.loadThread(rootId);
    } catch (err) {
      error = errorMessage(err);
    }
  }
  onMount(() => void load());
  $effect(() => {
    if (app.resetEpoch > 0) untrack(() => void load());
  });

  function editPending(p: PendingMessage) {
    app.discard(p.clientKey);
    composer?.fill(p.body);
  }
</script>

<RightPanel title="Thread" {mode} onclose={() => app.closePanel()}>
  {#snippet subtitle()}{room?.name ?? ''}{/snippet}
  {#if error}
    <p class="notice danger err" role="alert">{error}</p>
  {:else if !root}
    <p class="meta err">Loading the thread…</p>
  {:else}
    <MessageList
      label="Replies"
      items={replies}
      {pending}
      loaded={!!th?.loaded}
      inThread
      highlightId={app.loc.msg}
      oneditpending={editPending}
    >
      {#snippet top()}
        <div class="root">
          <MessageRow message={root} inThread tabindex={-1} />
          <p class="count meta">
            {replies.length === 0 ? 'No replies yet' : `${replies.length} ${replies.length === 1 ? 'reply' : 'replies'}`}
          </p>
        </div>
      {/snippet}
    </MessageList>
    {#if roomId}
      <Composer bind:this={composer} {roomId} threadId={rootId} placeholder="Reply in thread" compact />
    {/if}
  {/if}
</RightPanel>

<style>
  .err {
    margin: 16px;
  }
  .root {
    padding-bottom: 8px;
    border-bottom: 1px solid var(--line);
    margin-bottom: 4px;
  }
  .root :global(.msg:not(.continuation)) {
    margin-top: 8px;
  }
  .count {
    padding: 4px 20px 0;
  }
</style>
