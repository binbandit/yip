<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import Icon from './Icon.svelte';

  let { roomId }: { roomId: string } = $props();
  let sending = $state(false);
  const pending = $derived(Object.values(app.data.pending).some((p) => p.roomId === roomId && !p.threadId));

  async function summarize() {
    if (sending || pending) return;
    sending = true;
    try {
      await app.send({ roomId, body: 'Where are we with everything?', mentions: [], projectIds: [] });
    } finally {
      sending = false;
    }
  }
</script>

<div class="summary-actions">
  <p class="meta">A snapshot of active work, recent results and open questions across your projects.</p>
  <div class="actions">
    <button class="btn btn-primary" disabled={sending || pending} aria-busy={sending} onclick={summarize}>
      <Icon name="refresh" size={16} />{sending ? 'Gathering updates…' : 'Get a fresh summary'}
    </button>
    <a class="link-btn" href="/engineers">Talk with an engineer</a>
  </div>
</div>

<style>
  .summary-actions { flex: none; display: grid; gap: 12px; padding: 20px; border-top: 1px solid var(--line); }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
</style>
