<script lang="ts">
  // A machine's details in the right-hand panel, organised by purpose:
  // Overview (connection, work, capacity, disk, how it runs, and the
  // consequential actions), Connections (provider sign-in, accounts and
  // their shared concurrency), Storage (workspaces and cleanup) and
  // Diagnostics (versions, profiles, toolchains, fingerprint, activity).
  import { onMount } from 'svelte';
  import type { NodeWorkspace } from '../../lib/api/types.gen';
  import { api } from '../../lib/api/endpoints';
  import { app } from '../../lib/state/app.svelte';
  import { nodesDigest, providerProfiles } from '../../lib/state/profiles.svelte';
  import { connectionSummary, currentWork, machineFacts, providerStatuses, workspaceKind, workspaceLoss, workspaceSize, workspaceTitle } from '../../lib/util/machines';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import MachineOverview from '../machines/MachineOverview.svelte';
  import MachineConnections from '../machines/MachineConnections.svelte';
  import MachineStorage from '../machines/MachineStorage.svelte';
  import MachineDiagnostics from '../machines/MachineDiagnostics.svelte';

  interface Props {
    nodeId: string;
    mode: PanelMode;
  }
  let { nodeId, mode }: Props = $props();

  const TABS = ['overview', 'connections', 'storage', 'diagnostics'] as const;
  type Tab = (typeof TABS)[number];
  const TAB_LABEL: Record<Tab, string> = { overview: 'Overview', connections: 'Connections', storage: 'Storage', diagnostics: 'Diagnostics' };
  const tab: Tab = $derived((TABS as readonly string[]).includes(app.loc.tab ?? '') ? (app.loc.tab as Tab) : 'overview');

  const n = $derived(app.data.nodes[nodeId]);
  let missing = $state(false);
  onMount(() => {
    if (!app.data.nodes[nodeId])
      api
        .nodes()
        .then((ns) => {
          for (const x of ns ?? []) app.data.nodes[x.id] = x;
          missing = !app.data.nodes[nodeId];
        })
        .catch(() => (missing = true));
  });
  const digest = $derived(n ? nodesDigest([n]) : '');
  $effect(() => {
    if (digest) void providerProfiles.refresh(nodesDigest(Object.values(app.data.nodes)));
  });

  const conn = $derived(n ? connectionSummary(n, app.now) : null);
  const work = $derived(n ? currentWork(n, app.data.runs, app.data.jobs) : []);
  const providers = $derived(n ? providerStatuses(n, providerProfiles.list, app.now) : []);
  const facts = $derived(n ? machineFacts(n, providers.filter((p) => p.installed), Object.values(app.data.projects)) : []);
  const attention = $derived({
    connections: n?.status !== 'revoked' && providers.some((p) => p.installed && p.tone === 'attention'),
    storage: facts.some((f) => f.id === 'disk'),
  });

  function tabKey(e: KeyboardEvent) {
    const i = TABS.indexOf(tab);
    let next = i;
    if (e.key === 'ArrowRight') next = (i + 1) % TABS.length;
    else if (e.key === 'ArrowLeft') next = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = TABS.length - 1;
    else return;
    e.preventDefault();
    showTab(TABS[next], true);
  }
  function showTab(t: Tab, focusTab = false) {
    app.setTab(t);
    queueMicrotask(() => (focusTab ? document.getElementById(`machinetab-${t}`) : document.getElementById('machinepanel'))?.focus());
  }

  // ---- consequential actions, each confirmed ----

  type Kind = 'drain' | 'undrain' | 'stop' | 'revoke' | 'workspace';
  let confirm = $state<null | { kind: Kind; ws?: NodeWorkspace; invoker: HTMLElement | null; fallback?: string }>(null);

  function request(kind: Kind, ws?: NodeWorkspace) {
    const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    confirm = { kind, ws, invoker, fallback: kind === 'workspace' ? '#machine-storage-heading' : undefined };
  }

  async function run() {
    if (!confirm || !n) return;
    const { kind, ws } = confirm;
    const name = n.name;
    if (kind === 'drain' || kind === 'undrain') {
      app.data.nodes[n.id] = await api.drainNode(n.id, kind === 'drain');
      app.announce(kind === 'drain' ? `${name} will finish its current work and start nothing new.` : `${name} takes new work again.`);
    } else if (kind === 'workspace' && ws) {
      app.data.nodes[n.id] = await api.removeWorkspace(n.id, ws.name, { confirm: ws.name, force: !ws.published });
      app.announce(`Deleted ${ws.name} from ${name}.`);
    } else if (kind === 'stop') {
      await api.stopNodeWork(n.id);
      app.announce(`Stopping the work running on ${name}.`);
    } else if (kind === 'revoke') {
      await api.revokeNode(n.id);
      app.data.nodes[n.id] = { ...n, status: 'revoked', revokedAt: new Date().toISOString() };
      app.announce(`${name}'s access is revoked.`);
    }
  }

  // Focus goes back to the control that asked. When that control is gone
  // (a deleted workspace's row) or now disabled, it goes to a stable place
  // in the panel instead of being lost.
  function closeConfirm() {
    const c = confirm;
    confirm = null;
    setTimeout(() => {
      const panel = document.getElementById('machinepanel');
      const active = document.activeElement;
      if (active && active !== document.body && panel?.contains(active)) return;
      const inv = c?.invoker as HTMLButtonElement | null;
      if (inv && inv.isConnected && !inv.disabled && inv.getClientRects().length) {
        inv.focus();
        return;
      }
      (((c?.fallback && document.querySelector<HTMLElement>(c.fallback)) || panel) as HTMLElement | null)?.focus();
    }, 30);
  }

  const copy = $derived.by(() => {
    if (!confirm || !n) return null;
    const name = n.name;
    const k = n.activeRunIds.length;
    switch (confirm.kind) {
      case 'drain':
        return {
          title: `Pause new work on ${name}?`,
          body: `${name} finishes the work it's running now${k ? ` (${k} running)` : ''} and starts nothing new until you resume. Nothing is stopped.`,
          label: 'Pause new work',
          danger: false,
        };
      case 'undrain':
        return { title: `Resume new work on ${name}?`, body: `${name} starts taking new work again.`, label: 'Resume new work', danger: false };
      case 'stop':
        return {
          title: `Stop the current work on ${name}?`,
          body: `Every run on ${name}${k ? ` (${k})` : ''} is asked to stop now. Changes already made stay where they are, and the work can be retried. The machine stays paired and keeps taking new work unless you pause it.`,
          label: 'Stop current work',
          danger: true,
        };
      case 'revoke':
        return {
          title: `Revoke ${name}'s access?`,
          body: `${name}'s credential is revoked immediately: it can't connect or run work again without pairing anew. Work it's running becomes “not confirmed”. Its files stay on the machine.`,
          label: 'Revoke access',
          danger: true,
        };
      case 'workspace': {
        const w = confirm.ws!;
        const kind = workspaceKind(w).label;
        return {
          title: `Delete this ${kind.toLowerCase()}?`,
          body: `${workspaceTitle(w)} (${w.name}, ${workspaceSize(w)}). ${workspaceLoss(w)} The files are deleted from ${name}; the work's record, revisions and reviews on the hub stay.`,
          label: w.published ? 'Delete workspace' : 'Delete, losing that work',
          danger: true,
        };
      }
    }
  });
</script>

<RightPanel title={n?.name ?? 'Machine'} {mode} wide onclose={() => app.closePanel()}>
  {#snippet subtitle()}{#if conn}{conn.label}{conn.meta ? ` · ${conn.meta}` : ''}{/if}{/snippet}
  {#if !n}
    <p class="pad meta">{missing ? 'This machine isn’t available.' : 'Loading…'}</p>
  {:else}
    <div class="tabs" role="tablist" aria-label="Machine details">
      {#each TABS as t (t)}
        <button
          class="tab"
          role="tab"
          id="machinetab-{t}"
          aria-selected={tab === t}
          aria-controls="machinepanel"
          tabindex={tab === t ? 0 : -1}
          onclick={() => app.setTab(t)}
          onkeydown={tabKey}
        >
          {TAB_LABEL[t]}
          {#if (t === 'connections' && attention.connections) || (t === 'storage' && attention.storage)}<span class="marker tab-marker" aria-hidden="true"></span><span class="vh"> (needs attention)</span>{/if}
        </button>
      {/each}
    </div>
    <div class="pad" role="tabpanel" id="machinepanel" aria-labelledby="machinetab-{tab}" tabindex="-1">
      {#if tab === 'overview'}
        <MachineOverview node={n} {work} {providers} {facts} onrequest={request} ontab={(t) => showTab(t)} />
      {:else if tab === 'connections'}
        <MachineConnections node={n} {providers} ontab={(t) => showTab(t)} />
      {:else if tab === 'storage'}
        <MachineStorage node={n} onrequest={request} />
      {:else}
        <MachineDiagnostics node={n} {providers} />
      {/if}
    </div>
  {/if}
</RightPanel>

{#if confirm && copy}
  <ConfirmDialog title={copy.title} body={copy.body} confirmLabel={copy.label} danger={copy.danger} onconfirm={run} onclose={closeConfirm} />
{/if}

<style>
  .tabs {
    padding: 0 10px;
    gap: 0;
    flex: none;
  }
  .tab {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    padding-left: 8px;
    padding-right: 8px;
    font-size: var(--text-body);
  }
  .tab-marker {
    margin-left: 6px;
    width: 7px;
    height: 7px;
  }
  .pad {
    padding: 16px 18px 28px;
    container: machinepanel / inline-size;
  }
  [role='tabpanel']:focus-visible {
    outline: none;
  }
  @media (pointer: coarse) {
    .tab {
      min-height: 44px;
    }
  }
  @media (max-width: 480px) {
    .tabs {
      padding: 0 4px;
    }
    .tab {
      padding-left: 7px;
      padding-right: 7px;
    }
    .tab-marker {
      margin-left: 4px;
    }
  }
  @media (max-width: 760px) {
    .pad {
      padding: 16px 16px 28px;
    }
  }
</style>
