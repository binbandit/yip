<script lang="ts">
  // Where work runs. Connection and provider readiness are separate facts; a
  // sleeping laptop reads as asleep/offline with its last-seen time, not as a
  // mysterious failure. Drain and Stop its work are distinct actions.
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Node } from '../lib/api/types.gen';
  import { authStateLabel, billingLabel, nodeShape, nodeStatusLabel, nodeTone, providerLabel } from '../lib/util/labels';
  import { atTime, bytes, relative } from '../lib/util/time';
  import StateIcon from '../components/StateIcon.svelte';
  import Icon from '../components/Icon.svelte';
  import ConfirmDialog from '../components/ConfirmDialog.svelte';
  import AddMachineDialog from '../components/AddMachineDialog.svelte';

  // Each provider signs in with its own tool on the machine; yip only reads
  // the result. "Check again" asks the machine to re-read it right away.
  const SIGN_IN: Record<string, string> = { codex: 'codex login', claude: 'claude auth login', cursor: 'agent login' };
  let checking = $state<Record<string, boolean>>({});
  // Per-account concurrency: runs on one account share its allowance, so the
  // default is one at a time (brief §7) until the owner raises it.
  let profiles = $state<import('../lib/api/types.gen').ProviderProfile[]>([]);
  $effect(() => {
    void api.providerProfiles().then((p) => (profiles = p), () => {});
  });
  async function setConcurrency(profileId: string, max: number) {
    try {
      const p = await api.setProviderConcurrency(profileId, max);
      profiles = profiles.map((x) => (x.id === p.id ? p : x));
      app.toast(`${p.label} can now run ${max} ${max === 1 ? 'job' : 'jobs'} at once.`);
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    }
  }
  async function recheck(n: Node) {
    checking[n.id] = true;
    try {
      await api.probeNode(n.id);
      app.toast(`Checking the providers on ${n.name}…`);
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    } finally {
      setTimeout(() => (checking[n.id] = false), 4000);
    }
  }

  let adding = $state(false);
  let confirm = $state<null | { kind: 'drain' | 'undrain' | 'stop' | 'revoke'; node: Node }>(null);

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

  function connectionText(n: Node): string {
    const seen = n.lastSeenAt ? atTime(n.lastSeenAt) : '';
    switch (n.status) {
      case 'online':
        return n.lastActivity ? `Connected · ${n.lastActivity}` : 'Connected';
      case 'suspect':
        return `Not responding since ${seen || 'recently'}. If it doesn't come back, its running work is marked “not confirmed” rather than failed.`;
      case 'offline':
        return `Offline since ${seen || 'an unknown time'} — asleep, shut down or off the network. Work waits for it or can be retried elsewhere.`;
      case 'revoked':
        return `Revoked${n.revokedAt ? ' ' + atTime(n.revokedAt) : ''}. It can no longer connect.`;
    }
    return n.status;
  }

  async function run() {
    if (!confirm) return;
    const { kind, node } = confirm;
    if (kind === 'drain' || kind === 'undrain') {
      app.data.nodes[node.id] = await api.drainNode(node.id, kind === 'drain');
      app.announce(kind === 'drain' ? `${node.name} will finish its current work and take nothing new.` : `${node.name} accepts new work again.`);
    } else if (kind === 'stop') {
      await api.stopNodeWork(node.id);
      app.announce(`Stopping the work running on ${node.name}.`);
    } else {
      await api.revokeNode(node.id);
      app.data.nodes[node.id] = { ...node, status: 'revoked', revokedAt: new Date().toISOString() };
      app.announce(`${node.name} revoked.`);
    }
  }

  const confirmCopy = $derived.by(() => {
    if (!confirm) return null;
    const n = confirm.node.name;
    switch (confirm.kind) {
      case 'drain':
        return { title: `Drain ${n}?`, body: `${n} finishes the work it's running now and takes no new work. Nothing is stopped.`, label: 'Drain', danger: false };
      case 'undrain':
        return { title: `Resume ${n}?`, body: `${n} starts accepting new work again.`, label: 'Resume', danger: false };
      case 'stop':
        return {
          title: `Stop the work on ${n}?`,
          body: `Every run on ${n} is asked to stop now. Changes already made stay where they are; the work can be retried. The machine stays paired.`,
          label: 'Stop its work',
          danger: true,
        };
      case 'revoke':
        return {
          title: `Revoke ${n}?`,
          body: `${n}'s credential is revoked immediately; it can't connect or run work again without pairing anew. Work it was running becomes “not confirmed”.`,
          label: 'Revoke',
          danger: true,
        };
    }
  });
</script>

<div class="screen">
  <div class="screen-inner">
    <header class="screen-head">
      <div>
        <h1 class="screen-title" data-screen-title tabindex="-1">Machines</h1>
        <p class="screen-sub">Where engineers' work runs. Work continues on these when you close this window.</p>
      </div>
      <button class="btn btn-primary" onclick={() => (adding = true)}><Icon name="plus" size={16} />Add machine</button>
    </header>

    {#if nodes.length === 0}
      <div class="empty">
        <p><strong>No machines are paired yet.</strong></p>
        <p>Add an always-on machine so engineers can run work — and so it keeps running when your laptop sleeps.</p>
        <button class="btn btn-primary" onclick={() => (adding = true)}>Add machine</button>
      </div>
    {:else}
      <ul class="nodes">
        {#each nodes as n (n.id)}
          {@const cap = n.capacity}
          <li class="node" class:revoked={n.status === 'revoked'}>
            <header class="n-head">
              <div>
                <h2 class="n-name">{n.name}</h2>
                <p class="meta">{n.hostname ? `${n.hostname} · ` : ''}{n.os}/{n.arch} · runner {n.runnerVersion || 'unknown'}</p>
              </div>
              <span class="conn tone-{nodeTone(n.status)}"><StateIcon shape={nodeShape(n.status)} tone={nodeTone(n.status)} />{nodeStatusLabel(n.status, n.draining)}</span>
            </header>
            <p class="conn-text">{connectionText(n)}</p>
            {#if n.lastSeenAt && n.status === 'online'}<p class="meta">Last heard {relative(n.lastSeenAt, app.now)}</p>{/if}

            <dl class="facts">
              <div>
                <dt>Slots</dt>
                <dd>{cap.used} of {cap.slots} in use{n.draining ? ' · taking no new work' : ''}</dd>
              </div>
              <div>
                <dt>Disk</dt>
                <dd class:tone-danger={cap.diskPressure}>
                  {cap.diskPressure ? 'Low on space — ' : ''}{bytes(cap.diskFreeMb * 1024 * 1024)} free
                </dd>
              </div>
              {#if cap.cpus}<div><dt>Hardware</dt><dd>{cap.cpus} CPUs · {bytes(cap.memMb * 1024 * 1024)} memory</dd></div>{/if}
              {#if n.serviceState}<div><dt>Service</dt><dd>{n.serviceState}</dd></div>{/if}
            </dl>

            <section class="sub">
              <div class="sub-head">
                <h3>Providers</h3>
                {#if n.status === 'online'}
                  <button class="btn btn-sm btn-quiet" disabled={checking[n.id]} onclick={() => recheck(n)}>
                    <Icon name="refresh" size={14} />{checking[n.id] ? 'Checking…' : 'Check sign-in again'}
                  </button>
                {/if}
              </div>
              {#if n.providers.length === 0}
                <p class="meta">No providers detected on this machine.</p>
              {:else}
                <ul class="providers">
                  {#each n.providers as pv (pv.provider + pv.profileId)}
                    {@const prof = profiles.find((p) => p.id === pv.profileId)}
                    <li>
                      <p class="pv-head">
                        <StateIcon shape={pv.authState === 'ready' ? 'check-filled' : pv.authState === 'needs_signin' ? 'pause' : 'circle'} tone={pv.authState === 'ready' ? 'success' : pv.authState === 'needs_signin' || pv.authState === 'error' ? 'attention' : 'neutral'} size={13} />
                        <strong>{providerLabel(pv.provider)}</strong>
                        <span>{authStateLabel(pv.authState, pv.authDetail)}</span>
                        <span class="meta">· {billingLabel(pv.billing)}{pv.account ? ` · ${pv.account}` : ''}</span>
                      </p>
                      {#if pv.authState === 'ready' && prof && pv.provider !== 'fake'}
                        <label class="conc">
                          <span>Runs at once on this account</span>
                          <select class="select" value={String(prof.maxConcurrency)} onchange={(ev) => setConcurrency(prof.id, Number((ev.target as HTMLSelectElement).value))}>
                            {#each [...new Set([1, 2, 3, 4, 6, 8, prof.maxConcurrency])].sort((a, b) => a - b) as n (n)}<option value={String(n)}>{n}</option>{/each}
                          </select>
                          <span class="meta">They share the account's allowance.</span>
                        </label>
                      {/if}
                      {#if pv.authState === 'needs_signin'}
                        <p class="meta">
                          {providerLabel(pv.provider)} needs sign-in on {n.name}.
                          {#if SIGN_IN[pv.provider]}Run <code>{SIGN_IN[pv.provider]}</code> there, then choose Check sign-in again.{:else}Sign in there with the provider's own tool.{/if}
                          yip reuses that sign-in and never asks for tokens.
                        </p>
                      {/if}
                      <p class="meta">
                        {pv.version ? `Version ${pv.version}` : 'Version unknown'}
                        {#if pv.tested}· tested with yip{:else}· <span class="untested">untested version{pv.testedVersion ? ` (tested: ${pv.testedVersion})` : ''}</span>{/if}
                      </p>
                      {#if pv.limitations?.length}
                        <ul class="limits">{#each pv.limitations as l (l)}<li>{l}</li>{/each}</ul>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            </section>

            {#if n.profiles.length}
              <section class="sub">
                <h3>Execution profiles</h3>
                <ul class="profiles">
                  {#each n.profiles as pr (pr.name)}
                    <li>
                      <StateIcon shape={pr.available ? 'check-filled' : 'slash'} tone={pr.available ? 'success' : 'neutral'} size={13} />
                      <span><strong>{pr.name}</strong> — {pr.available ? pr.summary : pr.reason || 'Unavailable'}</span>
                    </li>
                  {/each}
                </ul>
              </section>
            {/if}

            {#if Object.keys(n.toolchains ?? {}).length}
              <p class="meta tools">Toolchains: {Object.entries(n.toolchains).map(([k, v]) => (v.toLowerCase().startsWith(k.toLowerCase()) ? v : `${k} ${v}`)).join(' · ')}</p>
            {/if}
            <p class="meta fp">Fingerprint <code>{n.fingerprint}</code></p>

            {#if n.status !== 'revoked'}
              <div class="actions">
                {#if n.draining}
                  <button class="btn btn-sm" onclick={() => (confirm = { kind: 'undrain', node: n })}>Resume new work</button>
                {:else}
                  <button class="btn btn-sm" onclick={() => (confirm = { kind: 'drain', node: n })}>Drain</button>
                {/if}
                <button class="btn btn-sm" disabled={n.activeRunIds.length === 0} onclick={() => (confirm = { kind: 'stop', node: n })}>
                  Stop its work{n.activeRunIds.length ? ` (${n.activeRunIds.length})` : ''}
                </button>
                <button class="btn btn-sm btn-danger" onclick={() => (confirm = { kind: 'revoke', node: n })}>Revoke</button>
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

{#if adding}<AddMachineDialog onclose={() => (adding = false)} />{/if}
{#if confirm && confirmCopy}
  <ConfirmDialog
    title={confirmCopy.title}
    body={confirmCopy.body}
    confirmLabel={confirmCopy.label}
    danger={confirmCopy.danger}
    onconfirm={run}
    onclose={() => (confirm = null)}
  />
{/if}

<style>
  .conc {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin: 4px 0;
    font-size: 13px;
  }
  .conc .select {
    width: auto;
    min-height: 28px;
    padding: 2px 8px;
  }
  .sub-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .nodes {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
    gap: 14px;
  }
  .node {
    display: grid;
    gap: 10px;
    align-content: start;
    padding: 16px;
    border: 1px solid var(--line);
    border-radius: 14px;
  }
  .node.revoked {
    background: var(--surface-subtle);
  }
  .n-head {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: flex-start;
  }
  .n-name {
    font-size: 17px;
  }
  .conn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13.5px;
    font-weight: 650;
    white-space: nowrap;
  }
  .conn-text {
    font-size: 14px;
  }
  .facts {
    margin: 0;
    display: grid;
    gap: 4px;
  }
  .facts > div {
    display: grid;
    grid-template-columns: 90px minmax(0, 1fr);
    gap: 10px;
    font-size: 14px;
  }
  dt {
    color: var(--ink-secondary);
  }
  dd {
    margin: 0;
  }
  .sub h3 {
    font-size: 13px;
    font-weight: 650;
    color: var(--ink-secondary);
    margin-bottom: 6px;
  }
  .providers,
  .profiles,
  .limits {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
    font-size: 14px;
  }
  .pv-head {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .untested {
    color: var(--attention-ink);
    font-weight: 600;
  }
  .limits {
    gap: 2px;
    padding-left: 18px;
    list-style: disc;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .profiles li {
    display: flex;
    gap: 6px;
    align-items: baseline;
  }
  .fp code {
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    padding-top: 4px;
    border-top: 1px solid var(--line-soft);
  }
  .actions .btn {
    margin-top: 8px;
  }
</style>
