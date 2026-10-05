<script lang="ts">
  // Connections: each provider's sign-in on this machine, the account it
  // uses, whether it's available now, how well yip knows its version, and
  // how many runs that account may carry at once. The command to sign in
  // sits next to the provider that needs it. Adapter notes are in
  // Diagnostics, not here.
  import { Button, Code, Icon, Link, MetadataList, MetadataListItem, Selector, Text } from '@astryx-svelte/core';
  import { ChevronDown, ChevronRight, RefreshCw } from '@lucide/svelte';
  import type { Node } from '../../lib/api/types.gen';
  import { api } from '../../lib/api/endpoints';
  import { errorMessage } from '../../lib/api/client';
  import { app } from '../../lib/state/app.svelte';
  import { workspaceUrl } from '../../lib/workspace';
  import { providerProfiles } from '../../lib/state/profiles.svelte';
  import { lastHeard, type FactTab, type ProviderStatus } from '../../lib/util/machines';
  import { clock } from '../../lib/util/time';
  import StateIcon from '../StateIcon.svelte';
  import Notice from '../Notice.svelte';

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
  <Link href={workspaceUrl('/connections')} hasUnderline>Connect a subscription — setup guides for each tool</Link>
  {#if online}
    <Button label={checking ? 'Checking…' : 'Check sign-in again'} size="sm" isDisabled={checking} onclick={recheck}>
      {#snippet icon()}<Icon icon={RefreshCw} size="sm" />{/snippet}
    </Button>
  {/if}
</div>

{#if installed.length === 0}
  <Notice title="No supported agent tool was found on this machine." description="Open Connections to choose a tool, install it and sign in. Then check the connection again." />
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
            <Text type="supporting" class="prov-meta">{i?.account || 'No account reported'} · {p.billing.split(' (')[0]}</Text>
          </span>
          <Icon icon={open[p.provider] ? ChevronDown : ChevronRight} size="sm" color="secondary" />
        </button>
        {#if p.signIn || i?.authState === 'needs_signin'}
          <p class="signin">
            {#if p.signIn}Run <Code size="inherit">{p.signIn}</Code> on this machine{online ? ', then choose Check sign-in again.' : '. It re-checks when it reconnects.'}{:else}Sign in on this machine with the provider’s own tool.{/if}
          </p>
        {/if}
        {#if open[p.provider]}
          <div class="body" id={bodyId}>
            <MetadataList>
              {#if i?.authDetail && i.authState !== 'ready'}<MetadataListItem label="Sign-in">{i.authDetail}</MetadataListItem>{/if}
              <MetadataListItem label="Account">{i?.account || 'Not reported'}</MetadataListItem>
              <MetadataListItem label="Billing">{p.billing}</MetadataListItem>
              {#if p.pausedUntil}
                <MetadataListItem label="Availability">This account’s allowance ran out. Its work waits until {clock(p.pausedUntil)}, then carries on by itself.</MetadataListItem>
              {/if}
              <MetadataListItem label="Reviews">
                {p.readOnly ? 'Can run read-only reviews here.' : `Can’t run read-only reviews here${readOnlyReason(p.provider) ? `: ${readOnlyReason(p.provider)}` : '.'}`}
              </MetadataListItem>
              <MetadataListItem label="Installation">{p.compat}</MetadataListItem>
              {#if prof && i?.authState === 'ready'}
                <MetadataListItem label="Runs at once">
                  <div class="conc">
                    <Selector
                      label="Runs at once on this account"
                      size="sm"
                      width="fit-content"
                      options={[...new Set([1, 2, 3, 4, 6, 8, prof.maxConcurrency])].sort((a, b) => a - b).map(String)}
                      value={String(prof.maxConcurrency)}
                      onChange={(v: string) => setConcurrency(prof.id, Number(v))}
                    />
                    <p class="explain">Shared by every machine signed in to {prof.label}, because runs on one account share its allowance. This machine’s own slots are in Overview.</p>
                  </div>
                </MetadataListItem>
              {/if}
              {#if (i?.limitations ?? []).length}
                <MetadataListItem label="Adapter notes">
                  <Button
                    label="{i!.limitations!.length} {i!.limitations!.length === 1 ? 'note' : 'notes'} in Diagnostics"
                    variant="ghost"
                    size="sm"
                    onclick={() => ontab('diagnostics')}
                  />
                </MetadataListItem>
              {/if}
            </MetadataList>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
{#if absent.length}
  <div class="absent">
    <Text as="p" type="supporting">Not installed here: {absent.map((p) => p.name).join(', ')}. Install it and sign in with its own tool; yip picks it up on the next check.</Text>
  </div>
{/if}

<style>
  .intro {
    display: grid;
    gap: var(--spacing-3);
    justify-items: start;
    margin-bottom: var(--spacing-4);
  }
  .explain {
    color: var(--color-text-secondary);
  }
  .providers {
    list-style: none;
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--color-border);
  }
  .prov {
    border-bottom: 1px solid var(--color-border);
    padding: var(--spacing-1) 0;
  }
  /* A disclosure row: the provider's state, account and billing; its
     details expand below the sign-in line. */
  .prov-head {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: var(--spacing-3);
    align-items: start;
    width: 100%;
    min-height: 44px;
    padding: var(--spacing-2) var(--spacing-1);
    border: 0;
    border-radius: var(--radius-element);
    background: none;
    color: var(--color-text-primary);
    font: inherit;
    text-align: left;
    cursor: pointer;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .prov-head:hover {
    background: var(--color-overlay-hover);
  }
  .prov-head:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 0;
  }
  .prov-head > :global(svg:first-child) {
    margin-top: 3px;
  }
  .prov-head > :global(.astryx-icon) {
    margin-top: 2px;
  }
  .prov-main {
    display: grid;
    gap: var(--spacing-0-5);
    min-width: 0;
  }
  .prov-line {
    overflow-wrap: anywhere;
  }
  .prov-line strong {
    font-weight: var(--font-weight-semibold);
  }
  .limit {
    color: var(--color-warning);
  }
  .prov-main :global(.prov-meta) {
    overflow-wrap: anywhere;
  }
  /* Both line up with the provider's name, past its 14px state mark. */
  .signin,
  .body {
    margin: 0 var(--spacing-1) var(--spacing-2) calc(var(--spacing-1) + 14px + var(--spacing-3));
  }
  /* One click selects the whole command for copying. */
  .signin :global(code) {
    user-select: all;
  }
  .body {
    margin-bottom: var(--spacing-3);
  }
  .body :global(dl) {
    grid-template-columns: 108px minmax(0, 1fr);
  }
  .body :global(dd) {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  /* A text-like action: its label lines up with the values above it. */
  .body :global(dd > .astryx-button[data-variant='ghost']) {
    margin-block: calc(-1 * var(--spacing-0-5));
    margin-inline-start: calc(-1 * var(--spacing-3));
  }
  .conc {
    display: grid;
    gap: var(--spacing-1);
    justify-items: start;
  }
  .conc :global(.astryx-selector) {
    width: 96px;
  }
  .absent {
    margin-top: var(--spacing-3);
  }
  @container machinepanel (max-width: 440px) {
    .body :global(dl) {
      grid-template-columns: minmax(0, 1fr);
      row-gap: var(--spacing-0-5);
    }
    .body :global(dd:not(:last-child)) {
      margin-bottom: var(--spacing-1-5);
    }
    .signin,
    .body {
      margin-left: var(--spacing-1);
    }
  }
</style>
