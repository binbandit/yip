<script lang="ts">
  // Overview: is it connected, what is it doing, how much can it take, is
  // there disk space, and does it keep running when a terminal closes. The
  // consequential actions sit beside the facts they change.
  import { Button, Code, CodeBlock, Heading, Icon, MetadataList, MetadataListItem, Text, Tooltip } from '@astryx-svelte/core';
  import { Info, RefreshCw, SquareTerminal, TriangleAlert } from '@lucide/svelte';
  import type { Node } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { jobShape, jobStateLabel, jobTone, providerLabel } from '../../lib/util/labels';
  import { bytes } from '../../lib/util/time';
  import { connectionSummary, LOW_DISK_MB, runsAs, workSummary, type CurrentWork, type Fact, type FactTab, type ProviderStatus } from '../../lib/util/machines';
  import { FACT_ICONS } from '../../lib/util/machineIcons';
  import StateIcon from '../StateIcon.svelte';

  interface Props {
    node: Node;
    work: CurrentWork[];
    providers: ProviderStatus[];
    facts: Fact[];
    onrequest: (kind: 'drain' | 'undrain' | 'stop' | 'revoke') => void;
    ontab: (tab: FactTab) => void;
  }
  let { node: n, work, providers, facts, onrequest, ontab }: Props = $props();

  const conn = $derived(connectionSummary(n, app.now));
  const summary = $derived(workSummary(n));
  const service = $derived(runsAs(n));
  const revoked = $derived(n.status === 'revoked');
  const slots = $derived(Math.max(1, n.capacity.slots || 0));
  const lowDisk = $derived(n.capacity.diskPressure || (n.capacity.diskFreeMb > 0 && n.capacity.diskFreeMb < LOW_DISK_MB));

  // What limits its work, most pressing first, each pointing at where it's handled.
  const limits = $derived.by(() => {
    if (revoked) return [];
    const out: Fact[] = [];
    for (const p of providers.filter((x) => x.installed)) {
      if (p.tone === 'attention') out.push({ id: `p-${p.provider}`, text: `${p.name}: ${p.word.toLowerCase().startsWith('allowance') ? p.word : p.word.toLowerCase()}`, icon: p.signIn ? 'key' : 'alert', tone: 'attention', tab: 'connections' });
      if (p.usable || p.pausedUntil) {
        if (!p.readOnly) out.push({ id: `ro-${p.provider}`, text: `${p.name} can’t run read-only reviews here`, icon: 'eye', tone: 'attention', tab: 'connections' });
      }
      if (p.limits.includes('Untested version')) out.push({ id: `v-${p.provider}`, text: `${p.name} is a version yip hasn’t been tested with`, icon: 'info', tone: 'neutral', tab: 'connections' });
    }
    for (const f of facts) if (f.id !== 'session') out.push(f);
    return out.sort((a, b) => Number(a.tone !== 'attention') - Number(b.tone !== 'attention'));
  });

  function workLabel(w: CurrentWork): string {
    const j = w.job;
    if (!j) return 'Work the hub is still loading';
    if (j.kind === 'reply') {
      const room = app.data.rooms[j.source?.roomId ?? ''];
      return room ? `Replying in ${room.kind === 'dm' ? 'a direct message' : '#' + room.name}` : 'Replying in a conversation';
    }
    return j.title;
  }
  function who(w: CurrentWork): string {
    const e = w.job ? app.data.engineers[w.job.ownerId] : undefined;
    return [e?.name, w.run ? providerLabel(w.run.provider) : ''].filter(Boolean).join(' · ');
  }
</script>

{#if limits.length}
  <section class="block" aria-labelledby="mo-limits">
    <Heading level={3} id="mo-limits">What limits its work</Heading>
    <ul class="limits">
      {#each limits as l (l.id)}
        <li>
          <button class="limit tone-{l.tone}" onclick={() => ontab(l.tab)}>
            <Icon icon={FACT_ICONS[l.icon]} size="sm" /><span class="text">{l.text}</span><span class="go">{l.tab === 'overview' ? '' : `Open ${l.tab[0].toUpperCase()}${l.tab.slice(1)}`}</span>
          </button>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<div class="facts">
  <MetadataList>
    <MetadataListItem label="Connection">
      <!-- A div, not a p: Tooltip wraps its trigger in a block element. -->
      <div class="state"><StateIcon shape={conn.shape} tone={conn.tone} /><span class="word">{conn.label}</span>{#if conn.meta}<Tooltip content={conn.metaTitle ?? ''} isEnabled={!!conn.metaTitle} hasHoverIndication={false}><Text type="supporting">· {conn.meta}</Text></Tooltip>{/if}</div>
      <p class="explain">{conn.explain}</p>
    </MetadataListItem>

    <MetadataListItem label="Current work">
      {#if work.length === 0}
        <p class="state"><StateIcon shape={summary.shape} tone={summary.tone} /><span class="word">{summary.label}</span></p>
      {:else}
        {#if n.status !== 'online'}
          <p class="explain">The hub hasn’t heard from it recently, so this work isn’t confirmed.</p>
        {/if}
        <ul class="work">
          {#each work as w (w.runId)}
            <li>
              {#if w.job}
                <button class="work-item" onclick={() => app.openPanel({ kind: 'job', id: w.job!.id })}>
                  <StateIcon shape={jobShape(w.job.state)} tone={jobTone(w.job.state)} />
                  <span class="work-title">{workLabel(w)}</span>
                  <Text type="supporting" class="work-meta">{jobStateLabel(w.job)}{who(w) ? ` · ${who(w)}` : ''}</Text>
                </button>
              {:else}
                <Text as="p" type="supporting">{workLabel(w)}</Text>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </MetadataListItem>

    {#if !revoked}
      <MetadataListItem label="New work">
        <div class="with-action">
          <div>
            <p class="state">
              <StateIcon shape={n.draining ? 'pause' : 'check'} tone={n.draining ? 'attention' : 'neutral'} /><span class="word">{n.draining ? 'Paused' : 'Taking new work'}</span>
            </p>
            <p class="explain">{n.draining ? 'Its current work finishes; nothing new starts here until you resume.' : 'The hub can give it work its providers and profiles allow.'}</p>
          </div>
          <Button label={n.draining ? 'Resume new work' : 'Pause new work'} size="sm" onclick={() => onrequest(n.draining ? 'undrain' : 'drain')} />
        </div>
      </MetadataListItem>

      <MetadataListItem label="Capacity">
        <p>{slots} {slots === 1 ? 'slot' : 'slots'} · {n.activeRunIds.length} in use</p>
        <p class="explain">How many runs this machine takes at once, set on the machine (<Code size="inherit">yip runner --slots {slots}</Code>). Each provider account also has its own limit, shared across machines: see Connections.</p>
      </MetadataListItem>

      <MetadataListItem label="Disk">
        {#if n.capacity.diskFreeMb > 0}
          <p class:low={lowDisk}>{#if lowDisk}<Icon icon={TriangleAlert} size="sm" />{/if}{bytes(n.capacity.diskFreeMb * 1024 * 1024)} free{lowDisk ? ' — low' : ''}</p>
          {#if lowDisk}
            <p class="explain">It takes no new work until more than 2 GB is free. Deleting workspaces you no longer need frees space.</p>
            <Button label="Review storage" size="sm" onclick={() => ontab('storage')} />
          {/if}
        {:else}
          <Text as="p" type="supporting">Not reported</Text>
        {/if}
      </MetadataListItem>

      <MetadataListItem label="Runs as">
        <p class="state">
          <Icon icon={service.kind === 'session' ? SquareTerminal : service.kind === 'service' ? RefreshCw : Info} size="sm" color="secondary" /><span class="word">{service.label}</span>
        </p>
        <p class="explain">{service.detail}</p>
        {#if service.kind === 'session'}
          <p class="explain">To keep it running after the terminal closes, install the runner as a background service on that machine, then check it comes back after a restart:</p>
          <CodeBlock code="yip service install runner" size="sm" width="100%" isWrapped />
          <Text as="p" type="supporting">If this runner is part of the hub (<Code size="inherit">yip hub --local-runner</Code> or <Code size="inherit">yip demo</Code>), install the hub instead: <Code size="inherit">yip service install hub</Code>.</Text>
        {/if}
      </MetadataListItem>
    {/if}
  </MetadataList>
</div>

{#if !revoked}
  <section class="block actions" aria-labelledby="mo-actions">
    <Heading level={3} id="mo-actions">Machine actions</Heading>
    <div class="action">
      <div>
        <p class="action-title">Stop current work</p>
        <p class="explain">Asks every run on it to stop now. The work can be retried; the machine stays paired.</p>
      </div>
      <Button
        label="Stop current work{n.activeRunIds.length ? ` (${n.activeRunIds.length})` : ''}"
        size="sm"
        isDisabled={n.activeRunIds.length === 0}
        onclick={() => onrequest('stop')}
      />
    </div>
    <div class="action">
      <div>
        <p class="action-title">Revoke access</p>
        <p class="explain">Removes its credential: it can’t connect or run work until you pair it again.</p>
      </div>
      <Button label="Revoke access…" size="sm" variant="destructive" onclick={() => onrequest('revoke')} />
    </div>
  </section>
{/if}

<style>
  .block {
    margin-bottom: var(--spacing-5);
  }
  .block :global(h3) {
    font-size: var(--font-size-base);
    font-weight: var(--font-weight-semibold);
    margin-bottom: var(--spacing-2);
  }
  .limits {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--spacing-0-5);
  }
  .limit {
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-2);
    width: 100%;
    min-height: 36px;
    padding: var(--spacing-2) var(--spacing-3);
    border: 0;
    border-radius: var(--radius-element);
    background: var(--color-background-muted);
    color: var(--color-text-primary);
    font: inherit;
    font-size: var(--font-size-base);
    text-align: left;
    cursor: pointer;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .limit:hover {
    background: color-mix(in srgb, var(--color-background-muted) 80%, var(--color-text-primary) 6%);
  }
  .limit:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  .limit > :global(.astryx-icon) {
    margin-top: 2px;
    flex: none;
  }
  .limit.tone-attention > :global(.astryx-icon) {
    color: var(--color-warning);
  }
  .limit.tone-neutral > :global(.astryx-icon) {
    color: var(--color-icon-secondary);
  }
  .limit .text {
    flex: 1;
    min-width: 0;
  }
  .go {
    flex: none;
    font-size: var(--font-size-sm);
    line-height: 20px;
    color: var(--color-text-secondary);
    white-space: nowrap;
  }

  /* The facts read as rows split by hairlines, label beside value. */
  .facts {
    margin-bottom: var(--spacing-5);
  }
  .block + .facts {
    border-top: 1px solid var(--color-border);
  }
  /* Label and value stretch to the row so their hairlines meet. */
  .facts :global(dl) {
    grid-template-columns: 112px minmax(0, 1fr);
    gap: 0;
    align-items: stretch;
  }
  .facts :global(dt),
  .facts :global(dd) {
    padding-block: var(--spacing-3);
    border-bottom: 1px solid var(--color-border);
  }
  .facts:first-child :global(dt:first-of-type),
  .facts:first-child :global(dd:first-of-type) {
    padding-top: 0;
  }
  .facts :global(dt) {
    align-items: flex-start;
    padding-inline-end: var(--spacing-4);
    font-weight: var(--font-weight-normal);
  }
  .facts :global(dd) {
    min-width: 0;
    display: grid;
    gap: var(--spacing-1);
    justify-items: start;
    overflow-wrap: anywhere;
  }
  .with-action {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: var(--spacing-3);
    width: 100%;
  }
  .state {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
    font-weight: var(--font-weight-medium);
    line-height: 20px;
  }
  .state .word {
    color: var(--color-text-primary);
  }
  .state :global(.astryx-text) {
    font-weight: var(--font-weight-normal);
  }
  .explain {
    color: var(--color-text-secondary);
    line-height: 1.45;
  }
  .low {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    font-weight: var(--font-weight-semibold);
  }
  .low > :global(.astryx-icon) {
    color: var(--color-warning);
  }
  .work {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--spacing-0-5);
    width: 100%;
  }
  .work-item {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    column-gap: var(--spacing-2);
    align-items: center;
    width: 100%;
    padding: var(--spacing-1) 0;
    border: 0;
    background: none;
    color: var(--color-text-primary);
    font: inherit;
    text-align: left;
    cursor: pointer;
    border-radius: var(--radius-inner);
  }
  .work-item:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  .work-item :global(.work-meta) {
    grid-column: 2;
  }
  .work-title {
    overflow-wrap: anywhere;
  }
  .work-item:hover .work-title {
    text-decoration: underline;
  }
  .actions {
    display: grid;
    gap: var(--spacing-3);
  }
  .actions :global(h3) {
    margin-bottom: 0;
  }
  .action {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--spacing-3);
    align-items: start;
  }
  .action-title {
    font-weight: var(--font-weight-medium);
  }
  @container machinepanel (max-width: 440px) {
    .facts :global(dl) {
      grid-template-columns: minmax(0, 1fr);
    }
    .facts :global(dt) {
      padding-bottom: 0;
      border-bottom: 0;
    }
    .facts :global(dd) {
      padding-top: var(--spacing-1);
    }
    .with-action,
    .action {
      grid-template-columns: minmax(0, 1fr);
      justify-items: start;
    }
  }
</style>
