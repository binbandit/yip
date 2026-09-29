<script lang="ts">
  // One machine in the Machines list: its name, connection, current work,
  // each provider's availability and what limits its work, and a way into
  // its details. Four separate facts, so "Connected" never reads as "ready".
  import { Button, Heading, Icon, Text, Tooltip, VisuallyHidden } from '@astryx-svelte/core';
  import { ChevronRight, Monitor } from '@lucide/svelte';
  import type { Node } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { providerProfiles } from '../../lib/state/profiles.svelte';
  import { connectionSummary, currentWork, machineFacts, osLabel, providerStatuses, workSummary } from '../../lib/util/machines';
  import { FACT_ICONS } from '../../lib/util/machineIcons';
  import StateIcon from '../StateIcon.svelte';

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
    (e.currentTarget as HTMLElement).querySelector<HTMLButtonElement>('.action button')?.focus();
    openDetails();
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<li class="row" class:selected class:revoked={n.status === 'revoked'} aria-labelledby={nameId} onclick={onRowClick}>
  <div class="cell name">
    <span class="device" aria-hidden="true"><Icon icon={Monitor} size="sm" /></span>
    <div class="name-text">
      <Heading level={2} id={nameId}>{n.name}</Heading>
      {#if platform}<Text as="p" type="supporting" class="meta">{platform}</Text>{/if}
    </div>
  </div>

  <div class="cell conn">
    <VisuallyHidden>Connection: </VisuallyHidden>
    <p class="state"><StateIcon shape={conn.shape} tone={conn.tone} /><span class="word">{conn.label}</span></p>
    {#if conn.meta}
      <Tooltip content={conn.metaTitle ?? ''} isEnabled={!!conn.metaTitle} hasHoverIndication={false}><Text as="p" type="supporting" class="meta">{conn.meta}</Text></Tooltip>
    {/if}
  </div>

  <div class="cell work">
    <VisuallyHidden>Current work: </VisuallyHidden>
    {#if n.activeRunIds.length}
      <button class="state work-link" onclick={openWork}>
        <StateIcon shape={work.shape} tone={work.tone} /><span class="word">{work.label}</span>
      </button>
    {:else}
      <p class="state"><StateIcon shape={work.shape} tone={work.tone} /><span class="word">{work.label}</span></p>
    {/if}
    {#if work.meta}
      <Text as="p" type="supporting" class={n.draining ? 'meta paused' : 'meta'}>{#if n.draining}<StateIcon shape="pause" size={12} />{/if}{work.meta}</Text>
    {/if}
  </div>

  <div class="cell limits">
    <VisuallyHidden>Providers and limits: </VisuallyHidden>
    {#if n.status === 'revoked'}
      <Text as="p" type="supporting" class="meta">Can’t run work</Text>
    {:else}
      <ul class="facts">
        {#each providers as p (p.provider)}
          <li class="prov">
            <StateIcon shape={p.shape} tone={p.tone} size={13} />
            <span class="prov-text"><strong>{p.name}</strong> <span class="tone-{p.tone === 'success' ? 'neutral' : p.tone}">{p.word}</span>{#each p.limits as l (l)}<span class="limit">{` · ${l}`}</span>{/each}</span>
          </li>
        {/each}
        {#each facts as f (f.id)}
          <li class="fact tone-{f.tone}"><Icon icon={FACT_ICONS[f.icon]} size="xsm" /><span>{f.text}</span></li>
        {/each}
      </ul>
    {/if}
  </div>

  <div class="cell action">
    <Button label="Details for {n.name}" size="sm" variant={selected ? 'primary' : 'secondary'} aria-current={selected ? 'true' : undefined} onclick={openDetails}>
      Details
      {#snippet endContent()}<Icon icon={ChevronRight} size="xsm" />{/snippet}
    </Button>
  </div>
</li>

<style>
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1.6fr) minmax(0, 0.95fr) minmax(0, 0.85fr) minmax(0, 1.65fr) 88px;
    grid-template-areas: 'name conn work limits action';
    gap: var(--spacing-2) var(--spacing-5);
    align-items: start;
    padding: var(--spacing-4) var(--spacing-3);
    border-top: 1px solid var(--color-border);
    cursor: default;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .row:first-child {
    border-top: 0;
  }
  .row:hover {
    background: var(--color-overlay-hover);
  }
  .row.selected {
    background: var(--color-overlay-pressed);
  }
  .row.revoked .name :global(h2),
  .row.revoked .state .word {
    color: var(--color-text-secondary);
  }
  .cell {
    min-width: 0;
  }
  .name {
    grid-area: name;
    display: flex;
    gap: var(--spacing-3);
    align-items: flex-start;
  }
  .device {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: none;
    margin-top: calc(-1 * var(--spacing-1));
    border-radius: var(--radius-element);
    background: var(--color-background-muted);
    color: var(--color-icon-secondary);
  }
  .name-text {
    min-width: 0;
  }
  .name :global(h2) {
    font-size: var(--font-size-lg);
    font-weight: var(--font-weight-semibold);
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
    margin-top: calc(-1 * var(--spacing-1));
  }
  .state {
    display: flex;
    width: fit-content;
    max-width: 100%;
    align-items: center;
    gap: var(--spacing-1-5);
    font-size: var(--font-size-base);
    font-weight: var(--font-weight-medium);
    line-height: 20px;
  }
  .state .word {
    color: var(--color-text-primary);
  }
  .work-link {
    padding: 0;
    border: 0;
    background: none;
    font: inherit;
    font-weight: var(--font-weight-medium);
    cursor: pointer;
    text-align: left;
    border-radius: var(--radius-inner);
  }
  .work-link .word {
    text-decoration: underline;
    text-decoration-color: color-mix(in srgb, var(--color-text-primary) 35%, transparent);
    text-underline-offset: 3px;
  }
  .work-link:hover .word {
    text-decoration-color: currentColor;
  }
  .work-link:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  .cell :global(.meta) {
    margin-top: var(--spacing-0-5);
  }
  .cell :global(.meta.paused) {
    display: flex;
    align-items: center;
    gap: var(--spacing-1);
    color: var(--color-text-primary);
    font-weight: var(--font-weight-medium);
  }
  .facts {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--spacing-1);
    font-size: var(--font-size-base);
    line-height: 20px;
  }
  .prov,
  .fact {
    display: flex;
    gap: var(--spacing-1-5);
    align-items: flex-start;
    min-width: 0;
  }
  /* Centre the 12–13px marks on the first 20px line. */
  .prov > :global(svg),
  .fact > :global(.astryx-icon) {
    margin-top: 4px;
    flex: none;
  }
  .prov-text {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .prov strong {
    font-weight: var(--font-weight-semibold);
  }
  .limit {
    color: var(--color-warning);
  }
  .fact.tone-attention {
    color: var(--color-text-primary);
  }
  .fact.tone-attention > :global(.astryx-icon) {
    color: var(--color-warning);
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
      gap: var(--spacing-3) var(--spacing-4);
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
