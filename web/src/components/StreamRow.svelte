<script lang="ts">
  // A provisional live preview from a running engineer. It is not a live
  // region (no token-by-token announcements) and is replaced by the canonical
  // message when it lands.
  import { app } from '../lib/state/app.svelte';
  import type { StreamPreview } from '../lib/state/data';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    stream: StreamPreview;
  }
  let { stream }: Props = $props();
  const name = $derived(app.engineerName(stream.engineerId));
</script>

<div class="stream" aria-label="{name} is writing (preview)">
  <div class="gutter"><Avatar actor={{ kind: 'engineer', id: stream.engineerId }} size={36} /></div>
  <div class="main">
    <header class="header">
      <span class="name">{name}</span>
      <span class="meta"><StateIcon shape="bar" tone="accent" size={12} live /> {stream.status ?? 'writing'}</span>
    </header>
    {#if stream.text}
      <p class="text" aria-hidden="true">{stream.text.length > 600 ? '…' + stream.text.slice(-600) : stream.text}</p>
    {/if}
  </div>
</div>

<style>
  .stream {
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr);
    gap: 10px;
    margin: var(--group-gap) 8px 0;
    padding: 6px 10px 6px 8px;
  }
  .header {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .name {
    font-weight: 650;
  }
  .meta {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
  .text {
    color: var(--ink-secondary);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
