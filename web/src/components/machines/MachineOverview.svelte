<script lang="ts">
  // Overview: is it connected, what is it doing, how much can it take, is
  // there disk space, and does it keep running when a terminal closes. The
  // consequential actions sit beside the facts they change.
  import type { Node } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { jobShape, jobStateLabel, jobTone, providerLabel } from '../../lib/util/labels';
  import { bytes } from '../../lib/util/time';
  import { connectionSummary, LOW_DISK_MB, runsAs, workSummary, type CurrentWork, type Fact, type FactTab, type ProviderStatus } from '../../lib/util/machines';
  import StateIcon from '../StateIcon.svelte';
  import Icon from '../Icon.svelte';

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
    const out: { id: string; text: string; icon: string; tone: 'attention' | 'neutral'; tab: FactTab }[] = [];
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
    <h3 id="mo-limits">What limits its work</h3>
    <ul class="limits">
      {#each limits as l (l.id)}
        <li>
          <button class="limit tone-{l.tone}" onclick={() => ontab(l.tab)}>
            <Icon name={l.icon} size={15} /><span>{l.text}</span><span class="go">{l.tab === 'overview' ? '' : `Open ${l.tab[0].toUpperCase()}${l.tab.slice(1)}`}</span>
          </button>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<dl class="facts">
  <div class="fact">
    <dt>Connection</dt>
    <dd>
      <p class="state tone-{conn.tone}"><StateIcon shape={conn.shape} tone={conn.tone} /><span class="word">{conn.label}</span>{#if conn.meta}<span class="meta" title={conn.metaTitle}>· {conn.meta}</span>{/if}</p>
      <p class="explain">{conn.explain}</p>
    </dd>
  </div>

  <div class="fact">
    <dt>Current work</dt>
    <dd>
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
                  <span class="meta">{jobStateLabel(w.job)}{who(w) ? ` · ${who(w)}` : ''}</span>
                </button>
              {:else}
                <p class="meta">{workLabel(w)}</p>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </dd>
  </div>

  {#if !revoked}
    <div class="fact">
      <dt>New work</dt>
      <dd class="with-action">
        <div>
          <p class="state">
            <StateIcon shape={n.draining ? 'pause' : 'check'} tone={n.draining ? 'attention' : 'neutral'} /><span class="word">{n.draining ? 'Paused' : 'Taking new work'}</span>
          </p>
          <p class="explain">{n.draining ? 'Its current work finishes; nothing new starts here until you resume.' : 'The hub can give it work its providers and profiles allow.'}</p>
        </div>
        <button class="btn btn-sm" onclick={() => onrequest(n.draining ? 'undrain' : 'drain')}>{n.draining ? 'Resume new work' : 'Pause new work'}</button>
      </dd>
    </div>

    <div class="fact">
      <dt>Capacity</dt>
      <dd>
        <p>{slots} {slots === 1 ? 'slot' : 'slots'} · {n.activeRunIds.length} in use</p>
        <p class="explain">How many runs this machine takes at once, set on the machine (<code>yip runner --slots {slots}</code>). Each provider account also has its own limit, shared across machines: see Connections.</p>
      </dd>
    </div>

    <div class="fact">
      <dt>Disk</dt>
      <dd>
        {#if n.capacity.diskFreeMb > 0}
          <p class:low={lowDisk}>{#if lowDisk}<Icon name="alert" size={15} />{/if}{bytes(n.capacity.diskFreeMb * 1024 * 1024)} free{lowDisk ? ' — low' : ''}</p>
          {#if lowDisk}
            <p class="explain">It takes no new work until more than 2 GB is free. Deleting workspaces you no longer need frees space.</p>
            <button class="btn btn-sm" onclick={() => ontab('storage')}>Review storage</button>
          {/if}
        {:else}
          <p class="meta">Not reported</p>
        {/if}
      </dd>
    </div>

    <div class="fact">
      <dt>Runs as</dt>
      <dd>
        <p class="state">
          <Icon name={service.kind === 'session' ? 'terminal' : service.kind === 'service' ? 'refresh' : 'info'} size={15} /><span class="word">{service.label}</span>
        </p>
        <p class="explain">{service.detail}</p>
        {#if service.kind === 'session'}
          <p class="explain">To keep it running after the terminal closes, install the runner as a background service on that machine, then check it comes back after a restart:</p>
          <pre class="cmd">yip service install runner</pre>
          <p class="meta">If this runner is part of the hub (<code>yip hub --local-runner</code> or <code>yip demo</code>), install the hub instead: <code>yip service install hub</code>.</p>
        {/if}
      </dd>
    </div>
  {/if}
</dl>

{#if !revoked}
  <section class="block actions" aria-labelledby="mo-actions">
    <h3 id="mo-actions">Machine actions</h3>
    <div class="action">
      <div>
        <p class="action-title">Stop current work</p>
        <p class="explain">Asks every run on it to stop now. The work can be retried; the machine stays paired.</p>
      </div>
      <button class="btn btn-sm" disabled={n.activeRunIds.length === 0} onclick={() => onrequest('stop')}>
        Stop current work{n.activeRunIds.length ? ` (${n.activeRunIds.length})` : ''}
      </button>
    </div>
    <div class="action">
      <div>
        <p class="action-title">Revoke access</p>
        <p class="explain">Removes its credential: it can’t connect or run work until you pair it again.</p>
      </div>
      <button class="btn btn-sm btn-danger" onclick={() => onrequest('revoke')}>Revoke access…</button>
    </div>
  </section>
{/if}

<style>
  .block {
    margin-bottom: 20px;
  }
  h3 {
    font-size: var(--text-body);
    font-weight: 600;
    margin-bottom: 8px;
  }
  .limits {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 2px;
  }
  .limit {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    width: 100%;
    min-height: 36px;
    padding: 8px 10px;
    border: 0;
    border-radius: var(--r-row);
    background: var(--surface-subtle);
    color: var(--ink);
    font-size: var(--text-body);
    text-align: left;
    cursor: pointer;
  }
  .limit:hover {
    background: color-mix(in srgb, var(--surface-subtle) 80%, var(--ink) 6%);
  }
  .limit :global(svg) {
    margin-top: 2.5px;
    flex: none;
  }
  .limit.tone-attention :global(svg) {
    color: var(--attention-ink);
  }
  .limit.tone-neutral :global(svg) {
    color: var(--ink-secondary);
  }
  .limit span:nth-of-type(1) {
    flex: 1;
    min-width: 0;
  }
  .go {
    flex: none;
    font-size: var(--text-meta);
    color: var(--ink-secondary);
    white-space: nowrap;
  }
  .facts {
    margin: 0 0 20px;
  }
  .block + .facts {
    border-top: 1px solid var(--line);
  }
  .facts:first-child > .fact:first-child {
    padding-top: 0;
  }
  .fact {
    display: grid;
    grid-template-columns: 112px minmax(0, 1fr);
    gap: 4px 16px;
    padding: 12px 0;
    border-bottom: 1px solid var(--line);
  }
  dt {
    font-size: var(--text-body);
    color: var(--ink-secondary);
    line-height: 20px;
  }
  dd {
    margin: 0;
    min-width: 0;
    display: grid;
    gap: 4px;
    justify-items: start;
    font-size: var(--text-body);
  }
  .with-action {
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: 12px;
  }
  .state {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    font-weight: 500;
    line-height: 20px;
  }
  .state .word {
    color: var(--ink);
  }
  .state .meta {
    font-weight: 400;
  }
  .explain {
    color: var(--ink-secondary);
    font-size: var(--text-body);
    line-height: 1.45;
  }
  .low {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-weight: 600;
  }
  .low :global(svg) {
    color: var(--attention-ink);
  }
  .work {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 2px;
    width: 100%;
  }
  .work-item {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    column-gap: 8px;
    align-items: center;
    width: 100%;
    padding: 4px 0;
    border: 0;
    background: none;
    color: var(--ink);
    font-size: var(--text-body);
    text-align: left;
    cursor: pointer;
  }
  .work-item .meta {
    grid-column: 2;
  }
  .work-title {
    overflow-wrap: anywhere;
  }
  .work-item:hover .work-title {
    text-decoration: underline;
  }
  .cmd {
    margin: 2px 0;
    padding: 8px 12px;
    border-radius: var(--r-control);
    border: 1px solid var(--line);
    background: var(--surface-subtle);
    font-size: 12.5px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: all;
    width: 100%;
  }
  code {
    overflow-wrap: anywhere;
  }
  .actions {
    display: grid;
    gap: 12px;
  }
  .actions h3 {
    margin-bottom: 0;
  }
  .action {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 12px;
    align-items: start;
  }
  .action-title {
    font-weight: 500;
  }
  @container machinepanel (max-width: 440px) {
    .fact {
      grid-template-columns: minmax(0, 1fr);
    }
    .with-action,
    .action {
      grid-template-columns: minmax(0, 1fr);
      justify-items: start;
    }
  }
</style>
