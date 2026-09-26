<script lang="ts">
  // Where work runs: a compact list of machines, each row saying how it's
  // connected, what it's doing and what limits its work. Everything else
  // (sign-in, storage, diagnostics and the consequential actions) is in a
  // machine's details, opened in the right-hand panel (?panel=machine:id).
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { nodesDigest, providerProfiles } from '../lib/state/profiles.svelte';
  import Icon from '../components/Icon.svelte';
  import AddMachineDialog from '../components/AddMachineDialog.svelte';
  import MachineRow from '../components/machines/MachineRow.svelte';

  let adding = $state(false);

  onMount(() => {
    api
      .nodes()
      .then((ns) => {
        for (const n of ns ?? []) app.data.nodes[n.id] = n;
      })
      .catch(() => {});
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

<div class="screen">
  <div class="screen-inner machines-screen">
    <header class="screen-head">
      <div>
        <h1 class="screen-title" data-screen-title tabindex="-1">Machines</h1>
        <p class="screen-sub">The computers your engineers’ work runs on. Closing this window doesn’t stop that work.</p>
      </div>
      <button class="btn btn-primary" onclick={() => (adding = true)}><Icon name="plus" size={16} />Add machine</button>
    </header>

    {#if nodes.length === 0}
      <div class="empty">
        <p><strong>No machines yet.</strong></p>
        <p>Engineers need a machine to run work on. Pair one with Add machine: an always-on computer keeps work going while this laptop sleeps.</p>
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
  </div>
</div>

{#if adding}<AddMachineDialog onclose={() => (adding = false)} />{/if}

<style>
  .machines-screen {
    max-width: 1120px;
    padding: 24px 24px 48px;
  }
  .machines-screen .screen-title {
    font-size: var(--text-display);
  }
  .machines-screen .screen-head {
    align-items: flex-start;
    margin-bottom: 20px;
  }
  .machines-screen .screen-sub {
    font-size: var(--text-body);
  }
  .list-wrap {
    container: machines / inline-size;
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
  }
  .machines {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(0, 0.95fr) minmax(0, 0.85fr) minmax(0, 1.9fr) 88px;
    gap: 20px;
    padding: 10px 12px 8px;
    border-bottom: 1px solid var(--line);
    font-size: var(--text-meta);
    font-weight: 500;
    color: var(--ink-secondary);
  }
  .cols span:first-child {
    padding-left: 42px;
  }
  @container machines (max-width: 780px) {
    .cols {
      display: none;
    }
  }
  @media (max-width: 760px) {
    .machines-screen {
      padding: 16px 16px 40px;
    }
    .machines-screen .screen-title {
      font-size: var(--text-display);
    }
  }
</style>
