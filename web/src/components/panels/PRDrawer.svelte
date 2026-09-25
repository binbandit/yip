<script lang="ts">
  import { untrack } from 'svelte';
  import { app } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { errorMessage } from '../../lib/api/client';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import PRFacts from '../PRFacts.svelte';
  import Icon from '../Icon.svelte';

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
    {#if error}<p class="notice danger" role="alert">{error}</p>{/if}
    {#if !pr}
      <p class="meta">Loading the pull request…</p>
    {:else}
      <PRFacts {pr} {reviews} />
      <div class="actions">
        <button class="btn btn-sm" onclick={refresh} disabled={refreshing}><Icon name="refresh" size={15} />{refreshing ? 'Checking the forge…' : 'Check the forge now'}</button>
        {#if pr.jobId}<button class="btn btn-sm btn-quiet" onclick={() => app.openPanel({ kind: 'job', id: pr.jobId! })}>Open the work</button>{/if}
        <a class="btn btn-sm btn-quiet" href={pr.url} target="_blank" rel="noopener noreferrer"><Icon name="external" size={15} />Open on {pr.host || pr.forge}</a>
      </div>
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: 14px 18px 24px;
    display: grid;
    gap: 14px;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
</style>
