<script lang="ts">
  // One machine in the Machines list: its name, connection, current work,
  // each provider's availability and what limits its work, and a way into
  // its details. Four separate facts, so "Connected" never reads as "ready".
  import type { Node } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { providerProfiles } from '../../lib/state/profiles.svelte';
  import { connectionSummary, currentWork, machineFacts, osLabel, providerStatuses, workSummary } from '../../lib/util/machines';
  import StateIcon from '../StateIcon.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    node: Node;
    selected?: boolean;
  }
  let { node: n, selected = false }: Props = $props();

  const conn = $derived(connectionSummary(n, app.now));
  const work = $derived(workSummary(n));
  const running = $derived(currentWork(n, app.data.runs, app.data.jobs));
  const providers = $derived(providerStatuses(n, providerProfiles.list, app.now).filter((p) => p.installed));
  const facts = $derived(machineFacts(n, providers, Object.values(app.data.projects)));
  const platform = $derived([osLabel(n.os), n.arch].filter(Boolean).join(' · '));
  const nameId = $derived(`machine-${n.id}-name`);

  let detailsBtn: HTMLButtonElement | undefined = $state();

  function openDetails() {
    app.openPanel({ kind: 'machine', id: n.id });
  }
  function openWork() {
    const jobs = [...new Set(running.map((w) => w.job?.id).filter(Boolean))] as string[];
    if (jobs.length === 1) app.openPanel({ kind: 'job', id: jobs[0] });
    else app.openPanel({ kind: 'machine', id: n.id }, 'overview');
  }
  // Clicking the row's empty space opens its details, as the Details
  // button does (focus goes there first so it returns there on close).
  function onRowClick(e: MouseEvent) {
    if ((e.target as HTMLElement).closest('button, a, select, input')) return;
    if (window.getSelection()?.toString()) return;
    detailsBtn?.focus();
    openDetails();
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<li class="row" class:selected class:revoked={n.status === 'revoked'} aria-labelledby={nameId} onclick={onRowClick}>
  <div class="cell name">
    <span class="device" aria-hidden="true"><Icon name="machine" size={18} /></span>
    <div class="name-text">
      <h2 id={nameId}>{n.name}</h2>
      {#if platform}<p class="meta">{platform}</p>{/if}
    </div>
  </div>

  <div class="cell conn">
    <span class="vh">Connection: </span>
    <p class="state tone-{conn.tone}"><StateIcon shape={conn.shape} tone={conn.tone} /><span class="word">{conn.label}</span></p>
    {#if conn.meta}<p class="meta" title={conn.metaTitle}>{conn.meta}</p>{/if}
  </div>

  <div class="cell work">
    <span class="vh">Current work: </span>
    {#if n.activeRunIds.length}
      <button class="state work-link tone-{work.tone}" onclick={openWork}>
        <StateIcon shape={work.shape} tone={work.tone} /><span class="word">{work.label}</span>
      </button>
    {:else}
      <p class="state tone-{work.tone}"><StateIcon shape={work.shape} tone={work.tone} /><span class="word">{work.label}</span></p>
    {/if}
    {#if work.meta}
      <p class="meta" class:paused={n.draining}>{#if n.draining}<StateIcon shape="pause" size={12} />{/if}{work.meta}</p>
    {/if}
  </div>

  <div class="cell limits">
    <span class="vh">Providers and limits: </span>
    {#if n.status === 'revoked'}
      <p class="meta">Can’t run work</p>
    {:else}
      <ul class="facts">
        {#each providers as p (p.provider)}
          <li class="prov">
            <StateIcon shape={p.shape} tone={p.tone} size={13} />
            <span class="prov-text"><strong>{p.name}</strong> <span class="tone-{p.tone === 'success' ? 'neutral' : p.tone}">{p.word}</span>{#each p.limits as l (l)}<span class="limit">{` · ${l}`}</span>{/each}</span>
          </li>
        {/each}
        {#each facts as f (f.id)}
          <li class="fact tone-{f.tone}"><Icon name={f.icon} size={13} /><span>{f.text}</span></li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="cell action">
    <button bind:this={detailsBtn} class="btn btn-sm details" aria-current={selected ? 'true' : undefined} aria-label="Details for {n.name}" onclick={openDetails}>
      Details<Icon name="chevronRight" size={15} />
    </button>
  </div>
</li>

<style>
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1.35fr) minmax(0, 0.95fr) minmax(0, 0.85fr) minmax(0, 1.9fr) 88px;
    grid-template-areas: 'name conn work limits action';
    gap: 8px 20px;
    align-items: start;
    padding: 14px 12px;
    border-top: 1px solid var(--line);
    border-radius: 0;
    cursor: default;
    transition: background-color var(--t-fast) var(--ease);
  }
  .row:first-child {
    border-top: 0;
  }
  .row:hover {
    background: var(--hover);
  }
  .row.selected {
    background: var(--selected);
  }
  .row.revoked .name h2,
  .row.revoked .state {
    color: var(--ink-secondary);
  }
  .cell {
    min-width: 0;
  }
  .name {
    grid-area: name;
    display: flex;
    gap: 10px;
    align-items: flex-start;
  }
  .device {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: none;
    margin-top: -4px;
    border-radius: var(--r-row);
    background: var(--surface-subtle);
    color: var(--ink-secondary);
  }
  .name-text {
    min-width: 0;
  }
  h2 {
    font-size: var(--text-title);
    font-weight: 600;
    letter-spacing: -0.015em;
    line-height: 1.3;
    overflow-wrap: anywhere;
  }
  .conn {
    grid-area: conn;
  }
  .work {
    grid-area: work;
  }
  .limits {
    grid-area: limits;
  }
  .action {
    grid-area: action;
    justify-self: end;
    margin-top: -4px;
  }
  .state {
    display: flex;
    width: fit-content;
    max-width: 100%;
    align-items: center;
    gap: 6px;
    font-size: var(--text-body);
    font-weight: 500;
    line-height: 20px;
  }
  .state .word {
    color: var(--ink);
  }
  .work-link {
    padding: 0;
    border: 0;
    background: none;
    cursor: pointer;
    text-align: left;
  }
  .work-link .word {
    text-decoration: underline;
    text-decoration-color: color-mix(in srgb, var(--ink) 35%, transparent);
    text-underline-offset: 3px;
  }
  .work-link:hover .word {
    text-decoration-color: currentColor;
  }
  .meta {
    margin-top: 2px;
  }
  .meta.paused {
    display: flex;
    align-items: center;
    gap: 5px;
    color: var(--ink);
    font-weight: 500;
  }
  .facts {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
    font-size: var(--text-body);
    line-height: 20px;
  }
  .prov,
  .fact {
    display: flex;
    gap: 7px;
    align-items: flex-start;
    min-width: 0;
  }
  .prov :global(svg),
  .fact :global(svg) {
    margin-top: 3.5px;
    flex: none;
  }
  .prov-text {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .prov strong {
    font-weight: 600;
  }
  .limit {
    color: var(--attention-ink);
  }
  .fact.tone-neutral {
    color: var(--ink-secondary);
  }
  .fact.tone-attention {
    color: var(--ink);
  }
  .fact.tone-attention :global(svg) {
    color: var(--attention-ink);
  }
  .details {
    gap: 2px;
    padding-right: 6px;
  }
  .details[aria-current='true'] {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--accent-ink);
  }

  /* The list sits in a container: the details panel (inline beside it at
     ≥1200px) narrows it, so the row adapts to its own width. */
  @container machines (max-width: 780px) {
    .row {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
      grid-template-areas:
        'name name action'
        'conn work work'
        'limits limits limits';
      gap: 10px 16px;
    }
  }
  @container machines (max-width: 340px) {
    .row {
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-areas:
        'name action'
        'conn conn'
        'work work'
        'limits limits';
    }
  }
</style>
