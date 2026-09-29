<script lang="ts">
  // An optimistic send: clearly pending, or clearly not sent with Retry.
  // Retry reuses the same clientKey, so a send the hub did receive
  // reconciles to the original message instead of creating a second request.
  import { TriangleAlert } from '@lucide/svelte';
  import { Button, Icon, Text } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import type { PendingMessage } from '../lib/state/data';
  import Avatar from './Avatar.svelte';
  import MessageBody from './MessageBody.svelte';

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
      <Text weight="semibold">{app.me?.name}</Text>
      {#if p.status === 'sending'}<Text type="supporting">Sending…</Text>{/if}
    </header>
    <div class="body"><MessageBody message={{ body: p.body, mentions: p.mentions }} /></div>
    {#if p.status === 'failed'}
      <div class="failed-row" role="alert">
        <Icon icon={TriangleAlert} size="sm" color="error" />
        <Text>Not sent. {p.error ?? ''}{p.error?.startsWith("Can't reach") ? ' Your message is saved on this device.' : ''}</Text>
        <Button label="Retry" size="sm" onclick={() => app.retry(p.clientKey)} />
        {#if onedit}<Button label="Edit" variant="ghost" size="sm" onclick={onedit} />{/if}
        <Button label="Discard" variant="ghost" size="sm" onclick={() => app.discard(p.clientKey)} />
      </div>
    {/if}
  </div>
</article>

<style>
  .pending {
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr);
    gap: 10px;
    margin: var(--yip-group-gap) var(--spacing-2) 0;
    padding: var(--spacing-1-5) 10px var(--spacing-1-5) var(--spacing-2);
    border-radius: var(--radius-container);
  }
  .pending:not(.failed) .body {
    opacity: 0.7;
  }
  .failed {
    background: var(--color-error-muted);
  }
  .main {
    min-width: 0;
  }
  .header {
    display: flex;
    gap: var(--spacing-2);
    align-items: baseline;
  }
  .failed-row {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-2);
    margin-top: var(--spacing-1-5);
  }
</style>
