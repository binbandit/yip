<script lang="ts">
  // Where work runs: a compact list of machines, each row saying how it's
  // connected, what it's doing and what limits its work. Everything else
  // (sign-in, storage, diagnostics and the consequential actions) is in a
  // machine's details, opened in the right-hand panel (?panel=machine:id).
  import { onMount } from 'svelte';
  import { Button, EmptyState, Icon } from '@astryx-svelte/core';
  import { Plus } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { replaceNodes } from '../lib/state/data';
  import { workspaceUrl } from '../lib/workspace';
  import { api } from '../lib/api/endpoints';
  import { nodesDigest, providerProfiles } from '../lib/state/profiles.svelte';
  import Screen from '../components/Screen.svelte';
  import AddMachineDialog from '../components/AddMachineDialog.svelte';
  import MachineRow from '../components/machines/MachineRow.svelte';

  let adding = $state(false);

  onMount(() => {
    let alive = true;
    const requestedAt = app.data.lastSeq;
    api
      .nodes()
      .then((ns) => {
        if (alive) replaceNodes(app.data, ns, requestedAt);
      })
      .catch(() => {});
    return () => { alive = false; };
  });

  const nodes = $derived(
    Object.values(app.data.nodes).sort((a, b) => Number(a.status === 'revoked') - Number(b.status === 'revoked') || a.name.localeCompare(b.name)),
  );
  // Re-read provider accounts when a machine reports a change (a sign-in, an allowance pause).
  const digest = $derived(nodesDigest(nodes));
  $effect(() => {
    void providerProfiles.refresh(digest);
  });
  const openId = $derived(app.loc.panel?.kind === 'machine' ? app.loc.panel.id : null);
</script>

<Screen title="Machines" subtitle="The computers your engineers’ work runs on. Closing this window doesn’t stop that work." width={1120} class="machines-screen">
  {#snippet actions()}
    <Button label="Connect subscription" href={workspaceUrl('/connections')} />
    <Button label="Add machine" variant="primary" onclick={() => (adding = true)}>
      {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
    </Button>
  {/snippet}

  {#if nodes.length === 0}
    <div class="empty">
      <EmptyState
        title="No machines yet."
        headingLevel={2}
        description="Engineers need a machine to run work on. Pair one with Add machine: an always-on computer keeps work going while this laptop sleeps."
      />
    </div>
  {:else}
    <div class="list-wrap">
      <div class="cols" aria-hidden="true">
        <span>Machine</span><span>Connection</span><span>Work</span><span>Providers and limits</span><span></span>
      </div>
      <ul class="machines" aria-label="Machines">
        {#each nodes as n (n.id)}
          <MachineRow node={n} selected={openId === n.id} />
        {/each}
      </ul>
    </div>
  {/if}
</Screen>

{#if adding}<AddMachineDialog onclose={() => (adding = false)} />{/if}

<style>
  .empty {
    padding-block: var(--spacing-6);
  }
  .list-wrap {
    container: machines / inline-size;
    border-top: 1px solid var(--color-border);
    border-bottom: 1px solid var(--color-border);
  }
  .machines {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  /* The same tracks as MachineRow, so the header sits over its columns. */
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1.6fr) minmax(0, 0.95fr) minmax(0, 0.85fr) minmax(0, 1.65fr) 88px;
    gap: var(--spacing-5);
    padding: var(--spacing-2) var(--spacing-3);
    border-bottom: 1px solid var(--color-border);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-medium);
    color: var(--color-text-secondary);
  }
  /* Past the row's device icon (32px) and its gap. */
  .cols span:first-child {
    padding-left: calc(32px + var(--spacing-3));
  }
  @container machines (max-width: 780px) {
    .cols {
      display: none;
    }
  }
</style>
