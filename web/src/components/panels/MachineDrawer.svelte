<script lang="ts">
  // A machine's details in the right-hand panel, organised by purpose:
  // Overview (connection, work, capacity, disk, how it runs, and the
  // consequential actions), Connections (provider sign-in, accounts and
  // their shared concurrency), Storage (workspaces and cleanup) and
  // Diagnostics (versions, profiles, toolchains, fingerprint, activity).
  import { onMount, tick } from 'svelte';
  import { Tab, TabList, Text, VisuallyHidden } from '@astryx-svelte/core';
  import type { NodeWorkspace } from '../../lib/api/types.gen';
  import { api } from '../../lib/api/endpoints';
  import { app } from '../../lib/state/app.svelte';
  import { mergeNode, removeNode, replaceNodes } from '../../lib/state/data';
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
  type Section = (typeof TABS)[number];
  const TAB_LABEL: Record<Section, string> = { overview: 'Overview', connections: 'Connections', storage: 'Storage', diagnostics: 'Diagnostics' };
  const isTab = (v: string | null | undefined): v is Section => (TABS as readonly string[]).includes(v ?? '');
  const tab: Section = $derived(isTab(app.loc.tab) ? app.loc.tab : 'overview');

  const n = $derived(app.data.nodes[nodeId]);
  let missing = $state(false);
  onMount(() => {
    let alive = true;
    const requestedAt = app.data.lastSeq;
    if (!app.data.nodes[nodeId])
      api
        .nodes()
        .then((ns) => {
          if (!alive) return;
          replaceNodes(app.data, ns, requestedAt);
          missing = !app.data.nodes[nodeId];
        })
        .catch(() => { if (alive) missing = true; });
    return () => { alive = false; };
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

  // The tab list moves focus with the arrow keys, Home and End; the tab that
  // takes focus is shown straight away.
  function onTabFocus(e: FocusEvent) {
    const v = (e.target as HTMLElement).closest<HTMLElement>('[data-tab-value]')?.dataset.tabValue;
    if (isTab(v) && v !== tab) app.setTab(v);
  }
  // From a link inside a section: show the tab and move focus to its content.
  function showTab(t: Section) {
    app.setTab(t);
    queueMicrotask(() => document.getElementById('machinepanel')?.focus());
  }

  // ---- consequential actions, each confirmed ----

  type Kind = 'drain' | 'undrain' | 'stop' | 'revoke' | 'remove' | 'workspace';
  let confirm = $state<null | { kind: Kind; id: string; name: string; ws?: NodeWorkspace; invoker: HTMLElement | null; fallback?: string }>(null);

  function request(kind: Kind, ws?: NodeWorkspace) {
    if (!n) return;
    const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    confirm = { kind, id: n.id, name: n.name, ws, invoker, fallback: kind === 'workspace' ? '#machine-storage-heading' : undefined };
  }

  async function run() {
    if (!confirm) return;
    const { kind, id, name, ws } = confirm;
    if (kind === 'remove') {
      await api.removeNode(id);
      removeNode(app.data, id);
      app.announce(`Removed ${name} from Machines. Its history and files are preserved.`);
      if (app.loc.panel?.kind === 'machine' && app.loc.panel.id === id) {
        app.closePanel();
        await tick();
        document.querySelector<HTMLElement>('[data-screen-title]')?.focus();
      }
      return;
    }
    if (!n || n.id !== id) throw new Error('This machine is no longer available. Close this dialog and refresh Machines.');
    if (kind === 'drain' || kind === 'undrain') {
      mergeNode(app.data, await api.drainNode(id, kind === 'drain'));
      app.announce(kind === 'drain' ? `${name} will finish its current work and start nothing new.` : `${name} takes new work again.`);
    } else if (kind === 'workspace' && ws) {
      mergeNode(app.data, await api.removeWorkspace(id, ws.name, { confirm: ws.name, force: !ws.published }));
      app.announce(`Deleted ${ws.name} from ${name}.`);
    } else if (kind === 'stop') {
      await api.stopNodeWork(n.id);
      app.announce(`Stopping the work running on ${name}.`);
    } else if (kind === 'revoke') {
      const node = n;
      await api.revokeNode(id);
      mergeNode(app.data, { ...node, status: 'revoked', revokedAt: new Date().toISOString() });
      app.announce(`${name}'s access is revoked.`);
    }
  }

  // Focus goes back to the control that asked. When that control is gone
  // (a deleted workspace's row) or now disabled, it goes to a stable place
  // in the panel instead of being lost. A panel closed in the meantime has
  // nothing to return focus to.
  let refocus: ReturnType<typeof setTimeout> | undefined;
  onMount(() => () => clearTimeout(refocus));
  function closeConfirm() {
    const c = confirm;
    confirm = null;
    clearTimeout(refocus);
    refocus = setTimeout(() => {
      const panel = document.getElementById('machinepanel');
      const active = document.activeElement;
      if (active && active !== document.body && panel?.contains(active)) return;
      const inv = c?.invoker as HTMLButtonElement | null;
      if (inv && inv.isConnected && !inv.disabled && (inv.checkVisibility?.() ?? true)) {
        inv.focus();
        return;
      }
      (((c?.fallback && document.querySelector<HTMLElement>(c.fallback)) || panel) as HTMLElement | null)?.focus();
    }, 30);
  }

  const copy = $derived.by(() => {
    if (!confirm) return null;
    const name = confirm.name;
    const k = n?.activeRunIds.length ?? 0;
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
      case 'remove':
        return {
          title: `Remove ${name} from Machines?`,
          body: `${name} disappears from the machine list. Its runs, work history and reported workspaces stay on the hub, and its local files stay on the machine. Its access stays revoked. To use it again, pair it as a new machine.`,
          label: 'Remove machine',
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

{#snippet attentionMark()}
  <span class="mark" aria-hidden="true"></span><VisuallyHidden> (needs attention)</VisuallyHidden>
{/snippet}

<RightPanel title={n?.name ?? 'Machine'} {mode} wide onclose={() => app.closePanel()}>
  {#snippet subtitle()}{#if conn}{conn.label}{conn.meta ? ` · ${conn.meta}` : ''}{/if}{/snippet}
  {#if !n}
    <div class="pad"><Text as="p" color="secondary">{missing || app.data.removedNodeIds[nodeId] ? 'This machine isn’t available.' : 'Loading…'}</Text></div>
  {:else}
    <div class="tabs">
      <TabList value={tab} onChange={(v) => isTab(v) && app.setTab(v)} role="tablist" aria-label="Machine details" size="sm" hasDivider onfocusin={onTabFocus}>
        {#each TABS as t (t)}
          <Tab
            value={t}
            label={TAB_LABEL[t]}
            id="machinetab-{t}"
            panelId="machinepanel"
            endContent={(t === 'connections' && attention.connections) || (t === 'storage' && attention.storage) ? attentionMark : undefined}
          />
        {/each}
      </TabList>
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
    flex: none;
  }
  /* Inset inside the tab list, so its hairline runs the panel's full width. */
  .tabs > :global(.astryx-tab-list) {
    padding-inline: var(--spacing-2);
  }
  /* The amber "waiting on you" marker used across yip. */
  .mark {
    display: inline-block;
    width: 8px;
    height: 8px;
    margin-inline-start: var(--spacing-0-5);
    border-radius: var(--radius-full);
    background: var(--yip-attention-fill);
  }
  /* The section scrolls under the tabs, which stay in view. */
  .pad {
    flex: 1;
    min-height: 0;
    overflow: auto;
    overscroll-behavior: contain;
    padding: var(--spacing-4) var(--spacing-4) var(--spacing-7);
    container: machinepanel / inline-size;
  }
  [role='tabpanel']:focus-visible {
    outline: none;
  }
  /* All four sections stay in view on a phone rather than scrolling. */
  @media (max-width: 480px) {
    .tabs > :global(.astryx-tab-list) {
      padding-inline: var(--spacing-1);
    }
    .tabs :global([role='tab']) {
      padding-inline: var(--spacing-1-5);
    }
  }
</style>
