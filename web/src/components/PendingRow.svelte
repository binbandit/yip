<script lang="ts">
  // An optimistic send: clearly pending, or clearly not sent with Retry.
  // Retry reuses the same clientKey, so a send the hub did receive
  // reconciles to the original message instead of creating a second request.
  import { app } from '../lib/state/app.svelte';
  import type { PendingMessage } from '../lib/state/data';
  import Avatar from './Avatar.svelte';
  import MessageBody from './MessageBody.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    p: PendingMessage;
    onedit?: () => void;
  }
  let { p, onedit }: Props = $props();
</script>

<article class="pending" class:failed={p.status === 'failed'} aria-label="Your message, {p.status === 'failed' ? 'not sent' : 'sending'}">
  <div class="gutter">{#if app.me}<Avatar actor={{ kind: 'user', id: app.me.id }} size={36} />{/if}</div>
  <div class="main">
    <header class="header">
      <span class="name">{app.me?.name}</span>
      {#if p.status === 'sending'}<span class="meta">Sending…</span>{/if}
    </header>
    <div class="body"><MessageBody message={{ body: p.body, mentions: p.mentions }} /></div>
    {#if p.status === 'failed'}
      <div class="failed-row" role="alert">
        <Icon name="alert" size={15} />
        <span>Not sent. {p.error ?? ''}{p.error?.startsWith("Can't reach") ? ' Your message is saved on this device.' : ''}</span>
        <button class="btn btn-sm" onclick={() => app.retry(p.clientKey)}>Retry</button>
        {#if onedit}<button class="btn btn-sm btn-quiet" onclick={onedit}>Edit</button>{/if}
        <button class="btn btn-sm btn-quiet" onclick={() => app.discard(p.clientKey)}>Discard</button>
      </div>
    {/if}
  </div>
</article>

<style>
  .pending {
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr);
    gap: 10px;
    margin: var(--group-gap) 8px 0;
    padding: 6px 10px 6px 8px;
    border-radius: 12px;
  }
  .pending:not(.failed) .body {
    opacity: 0.7;
  }
  .failed {
    background: var(--danger-subtle);
  }
  .header {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .name {
    font-weight: 650;
  }
  .failed-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 6px;
    font-size: 13.5px;
    color: var(--danger);
  }
  .failed-row span {
    color: var(--ink);
  }
</style>
