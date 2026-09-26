<script lang="ts">
  // Connections: each provider's sign-in on this machine, the account it
  // uses, whether it's available now, how well yip knows its version, and
  // how many runs that account may carry at once. The command to sign in
  // sits next to the provider that needs it. Adapter notes are in
  // Diagnostics, not here.
  import type { Node } from '../../lib/api/types.gen';
  import { api } from '../../lib/api/endpoints';
  import { errorMessage } from '../../lib/api/client';
  import { app } from '../../lib/state/app.svelte';
  import { providerProfiles } from '../../lib/state/profiles.svelte';
  import { lastHeard, type FactTab, type ProviderStatus } from '../../lib/util/machines';
  import { clock } from '../../lib/util/time';
  import StateIcon from '../StateIcon.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    node: Node;
    providers: ProviderStatus[];
    ontab: (tab: FactTab) => void;
  }
  let { node: n, providers, ontab }: Props = $props();

  const installed = $derived(providers.filter((p) => p.installed));
  const absent = $derived(providers.filter((p) => !p.installed));
  const online = $derived(n.status === 'online');
  const heard = $derived(lastHeard(n, app.now));
  let open = $state<Record<string, boolean>>({});
  let checking = $state(false);

  const inst = (provider: string) => n.providers.find((p) => p.provider === provider);
  const readOnlyReason = (provider: string) => (inst(provider)?.limitations ?? []).find((l) => /read-only/i.test(l));

  async function recheck() {
    checking = true;
    try {
      await api.probeNode(n.id);
      app.toast(`Checking the providers on ${n.name}…`);
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    } finally {
      setTimeout(() => (checking = false), 4000);
    }
  }

  async function setConcurrency(profileId: string, max: number) {
    try {
      const p = await api.setProviderConcurrency(profileId, max);
      providerProfiles.replace(p);
      app.toast(`${p.label} can now run ${max} ${max === 1 ? 'job' : 'jobs'} at once.`);
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    }
  }
</script>

<div class="intro">
  <p class="explain">
    Each provider signs in with its own tool on this machine; yip reads the result and never asks for tokens.
    {#if !online && heard}This is from its last report ({heard.rel}).{/if}
  </p>
  {#if online}
    <button class="btn btn-sm" disabled={checking} onclick={recheck}><Icon name="refresh" size={14} />{checking ? 'Checking…' : 'Check sign-in again'}</button>
  {/if}
</div>

{#if installed.length === 0}
  <p class="notice">No Codex, Claude Code or Cursor was found on this machine. Install one and sign in with its own tool; yip picks it up on the next check.</p>
{:else}
  <ul class="providers">
    {#each installed as p (p.provider)}
      {@const i = inst(p.provider)}
      {@const prof = providerProfiles.get(i?.profileId)}
      {@const bodyId = `prov-${n.id}-${p.provider}`}
      <li class="prov">
        <button class="prov-head" aria-expanded={!!open[p.provider]} aria-controls={bodyId} onclick={() => (open[p.provider] = !open[p.provider])}>
          <StateIcon shape={p.shape} tone={p.tone} />
          <span class="prov-main">
            <span class="prov-line"><strong>{p.name}</strong> <span class="word">{p.word}</span>{#each p.limits as l (l)}<span class="limit">{` · ${l}`}</span>{/each}</span>
            <span class="meta">{i?.account || 'No account reported'} · {p.billing.split(' (')[0]}</span>
          </span>
          <Icon name={open[p.provider] ? 'chevronDown' : 'chevronRight'} size={16} />
        </button>
        {#if p.signIn || i?.authState === 'needs_signin'}
          <p class="signin">
            {#if p.signIn}Run <code>{p.signIn}</code> on this machine{online ? ', then choose Check sign-in again.' : '. It re-checks when it reconnects.'}{:else}Sign in on this machine with the provider’s own tool.{/if}
          </p>
        {/if}
        {#if open[p.provider]}
          <dl class="body" id={bodyId}>
            {#if i?.authDetail && i.authState !== 'ready'}<div><dt>Sign-in</dt><dd>{i.authDetail}</dd></div>{/if}
            <div><dt>Account</dt><dd>{i?.account || 'Not reported'}</dd></div>
            <div><dt>Billing</dt><dd>{p.billing}</dd></div>
            {#if p.pausedUntil}
              <div><dt>Availability</dt><dd>This account’s allowance ran out. Its work waits until {clock(p.pausedUntil)}, then carries on by itself.</dd></div>
            {/if}
            <div>
              <dt>Reviews</dt>
              <dd>{p.readOnly ? 'Can run read-only reviews here.' : `Can’t run read-only reviews here${readOnlyReason(p.provider) ? `: ${readOnlyReason(p.provider)}` : '.'}`}</dd>
            </div>
            <div>
              <dt>Compatibility</dt>
              <dd>
                {p.compat}.
                {#if !i?.tested}yip hasn’t been tested with this version, so runs may misbehave; the adapter notes in Diagnostics say what’s known.{/if}
              </dd>
            </div>
            {#if prof && p.provider !== 'fake' && i?.authState === 'ready'}
              <div>
                <dt>Runs at once</dt>
                <dd>
                  <label class="conc">
                    <span>Runs at once on this account</span>
                    <select class="select" value={String(prof.maxConcurrency)} onchange={(ev) => setConcurrency(prof.id, Number((ev.target as HTMLSelectElement).value))}>
                      {#each [...new Set([1, 2, 3, 4, 6, 8, prof.maxConcurrency])].sort((a, b) => a - b) as k (k)}<option value={String(k)}>{k}</option>{/each}
                    </select>
                  </label>
                  <span class="explain">Shared by every machine signed in to {prof.label}, because runs on one account share its allowance. This machine’s own slots are in Overview.</span>
                </dd>
              </div>
            {/if}
            {#if (i?.limitations ?? []).length}
              <div>
                <dt>Adapter notes</dt>
                <dd><button class="link-btn" onclick={() => ontab('diagnostics')}>{i!.limitations!.length} {i!.limitations!.length === 1 ? 'note' : 'notes'} in Diagnostics</button></dd>
              </div>
            {/if}
          </dl>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
{#if absent.length}
  <p class="meta absent">Not installed here: {absent.map((p) => p.name).join(', ')}. Install it and sign in with its own tool; yip picks it up on the next check.</p>
{/if}

<style>
  .intro {
    display: grid;
    gap: 10px;
    justify-items: start;
    margin-bottom: 14px;
  }
  .explain {
    color: var(--ink-secondary);
    font-size: var(--text-body);
  }
  .providers {
    list-style: none;
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--line);
  }
  .prov {
    border-bottom: 1px solid var(--line);
    padding: 4px 0;
  }
  .prov-head {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 10px;
    align-items: start;
    width: 100%;
    min-height: 44px;
    padding: 8px 4px;
    border: 0;
    border-radius: var(--r-row);
    background: none;
    color: var(--ink);
    font-size: var(--text-body);
    text-align: left;
    cursor: pointer;
  }
  .prov-head:hover {
    background: var(--hover);
  }
  .prov-head > :global(svg:first-child) {
    margin-top: 3px;
  }
  .prov-head > :global(svg:last-child) {
    margin-top: 2px;
    color: var(--ink-secondary);
  }
  .prov-main {
    display: grid;
    gap: 2px;
    min-width: 0;
  }
  .prov-line {
    overflow-wrap: anywhere;
  }
  .prov-line strong {
    font-weight: 600;
  }
  .limit {
    color: var(--attention-ink);
  }
  .meta {
    overflow-wrap: anywhere;
  }
  .signin {
    margin: 0 4px 8px 32px;
    font-size: var(--text-body);
  }
  .signin code {
    padding: 1px 5px;
    border-radius: 5px;
    background: var(--surface-subtle);
    border: 1px solid var(--line);
    user-select: all;
  }
  .body {
    margin: 0 4px 10px 32px;
    display: grid;
    gap: 8px;
  }
  .body > div {
    display: grid;
    grid-template-columns: 108px minmax(0, 1fr);
    gap: 12px;
    font-size: var(--text-body);
  }
  dt {
    color: var(--ink-secondary);
  }
  dd {
    margin: 0;
    min-width: 0;
    display: grid;
    gap: 4px;
    justify-items: start;
    overflow-wrap: anywhere;
  }
  .conc {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
  }
  .conc .select {
    width: auto;
  }
  .absent {
    margin-top: 12px;
  }
  @container machinepanel (max-width: 440px) {
    .body > div {
      grid-template-columns: minmax(0, 1fr);
      gap: 2px;
    }
    .signin,
    .body {
      margin-left: 4px;
    }
  }
</style>
