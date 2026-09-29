<script lang="ts">
  import { untrack } from 'svelte';
  import { Button, Icon, Text } from '@astryx-svelte/core';
  import Notice from '../Notice.svelte';
  import { ExternalLink, RefreshCw } from '@lucide/svelte';
  import { app } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { errorMessage } from '../../lib/api/client';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import PRFacts from '../PRFacts.svelte';

  interface Props {
    prId: string;
    mode: PanelMode;
  }
  let { prId, mode }: Props = $props();
  let error = $state('');
  let refreshing = $state(false);

  $effect(() => {
    const id = prId;
    untrack(() => details.ensurePR(id).catch((e) => (error = errorMessage(e))));
  });
  const pr = $derived(app.data.prs[prId]);
  const reviews = $derived(pr?.jobId ? Object.values(app.data.reviews).filter((r) => r.jobId === pr.jobId) : []);

  async function refresh() {
    refreshing = true;
    error = '';
    try {
      await details.ensurePR(prId, true);
    } catch (e) {
      error = errorMessage(e);
    } finally {
      refreshing = false;
    }
  }
</script>

<RightPanel title={pr ? `PR #${pr.number}` : 'Pull request'} {mode} onclose={() => app.closePanel()}>
  {#snippet subtitle()}{pr?.title ?? ''}{/snippet}
  <div class="pad">
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
    {#if !pr}
      <Text as="p" type="supporting">Loading the pull request…</Text>
    {:else}
      <PRFacts {pr} {reviews} />
      <div class="actions">
        <Button size="sm" label={refreshing ? 'Checking the forge…' : 'Check the forge now'} isDisabled={refreshing} onclick={refresh}>
          {#snippet icon()}<Icon icon={RefreshCw} size="sm" />{/snippet}
        </Button>
        {#if pr.jobId}<Button size="sm" variant="ghost" label="Open the work" onclick={() => app.openPanel({ kind: 'job', id: pr.jobId! })} />{/if}
        <Button size="sm" variant="ghost" label="Open on {pr.host || pr.forge}" href={pr.url} target="_blank" rel="noopener noreferrer">
          {#snippet icon()}<Icon icon={ExternalLink} size="sm" />{/snippet}
        </Button>
      </div>
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: var(--spacing-3) var(--spacing-4) var(--spacing-6);
    display: grid;
    gap: var(--spacing-4);
  }
  .actions {
    display: flex;
    gap: var(--spacing-2);
    flex-wrap: wrap;
  }
</style>
