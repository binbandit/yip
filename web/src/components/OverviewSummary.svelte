<script lang="ts">
  import { Button, Icon, Link, Text } from '@astryx-svelte/core';
  import { RefreshCw } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';

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
  <Text as="p" display="block" type="supporting">A snapshot of active work, recent results and open questions across your projects.</Text>
  <div class="actions">
    <Button label="Get a fresh summary" variant="primary" isLoading={sending} isDisabled={pending} onclick={summarize}>
      {#snippet icon()}<Icon icon={RefreshCw} size="sm" />{/snippet}
    </Button>
    <Link href={workspaceUrl('/engineers')} isStandalone hasUnderline>Talk with an engineer</Link>
  </div>
</div>

<style>
  .summary-actions {
    flex: none;
    display: grid;
    gap: var(--spacing-3);
    padding: var(--spacing-5);
    border-top: 1px solid var(--color-border);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-3);
  }
</style>
