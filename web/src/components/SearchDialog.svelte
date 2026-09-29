<script lang="ts">
  // ⌘K search. Results are grouped and each opens its actual source: a room,
  // a message scrolled into view, a job drawer, a profile, a decision.
  // Mounting it opens it; `onclose` asks the parent to unmount it. Escape and
  // the backdrop close it through Astryx's Dialog.
  import { onMount, type Component } from 'svelte';
  import { Dialog, Icon, Kbd, Layout, LayoutContent, LayoutFooter, LayoutHeader, Selector, Text, TextInput } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { BookOpen, Folder, GitCommitHorizontal, Hash, Info, MessageSquare, Search, Users } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { SearchResult } from '../lib/api/types.gen';
  import { relative } from '../lib/util/time';
  import { snippetParts } from '../lib/util/markdown';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();

  let q = $state('');
  // Narrow everything to one project (rooms linked to it, its work, its decisions).
  let project = $state('');
  const projects = $derived(Object.values(app.data.projects).sort((a, b) => a.name.localeCompare(b.name)));
  const ALL = 'all';
  const scopes = $derived([{ value: ALL, label: 'All projects' }, ...projects.map((p) => ({ value: p.id, label: p.name }))]);
  let results = $state<SearchResult[]>([]);
  let loading = $state(false);
  let error = $state('');
  let ctrl: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const ORDER = ['room', 'engineer', 'job', 'message', 'decision', 'project'];
  const LABEL: Record<string, string> = {
    room: 'Rooms',
    engineer: 'People',
    job: 'Work',
    message: 'Messages',
    decision: 'Decisions',
    project: 'Projects',
  };
  const ICON: Record<string, Component> = { room: Hash, engineer: Users, job: GitCommitHorizontal, message: MessageSquare, decision: BookOpen, project: Folder };

  // Before typing: jump straight to rooms and people.
  const quick: SearchResult[] = $derived.by(() => {
    const s = q.trim().toLowerCase();
    const rooms = app.rooms
      .filter((r) => (!s || r.name.toLowerCase().includes(s)) && (!project || r.projectIds.includes(project)))
      .map((r) => ({ kind: 'room', id: r.id, title: r.kind === 'dm' ? `${r.name} (direct)` : r.name, snippet: r.purpose, roomId: r.id }));
    const people = Object.values(app.data.engineers)
      .filter((e) => !project && (!s || e.name.toLowerCase().includes(s) || e.role.toLowerCase().includes(s)))
      .map((e) => ({ kind: 'engineer', id: e.id, title: e.name, snippet: e.role }));
    return [...rooms, ...people];
  });

  const keyOf = (r: SearchResult) => r.kind + r.id;

  const shown: SearchResult[] = $derived.by(() => {
    const base = q.trim().length >= 2 ? results : [];
    const seen = new Set(base.map(keyOf));
    const merged = [...base, ...quick.filter((r) => !seen.has(keyOf(r)))];
    return merged.sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind));
  });

  // The highlighted result is tracked by identity, so it stays put when
  // results arrive and re-sort the list; when it's gone, the first one is.
  let activeKey = $state('');
  const active = $derived(Math.max(0, shown.findIndex((r) => keyOf(r) === activeKey)));

  const groups = $derived.by(() => {
    const out: { kind: string; items: { r: SearchResult; i: number }[] }[] = [];
    shown.forEach((r, i) => {
      let g = out.find((x) => x.kind === r.kind);
      if (!g) out.push((g = { kind: r.kind, items: [] }));
      g.items.push({ r, i });
    });
    return out;
  });

  // The input is the combobox for the results listbox. TextInput forwards
  // these to its <input> but only types the generic HTML attributes.
  const combobox = $derived({
    role: 'combobox',
    'aria-expanded': shown.length > 0,
    'aria-controls': 'search-results',
    'aria-activedescendant': shown.length ? `sr-${active}` : undefined,
    'aria-autocomplete': 'list' as const,
    autocomplete: 'off',
    enterkeyhint: 'search' as const,
    spellcheck: false,
  });

  // Focus goes back to whatever was focused before ⌘K, unless closing moved
  // it somewhere on purpose (opening a result focuses its screen or panel).
  const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  onMount(() => () => {
    ctrl?.abort();
    if (timer) clearTimeout(timer);
    queueMicrotask(() => {
      const current = document.activeElement;
      if (invoker?.isConnected && (!current || current === document.body)) invoker.focus({ preventScroll: true });
    });
  });

  function onInput() {
    activeKey = '';
    error = '';
    if (timer) clearTimeout(timer);
    const term = q.trim();
    if (term.length < 2) {
      results = [];
      loading = false;
      return;
    }
    loading = true;
    timer = setTimeout(async () => {
      ctrl?.abort();
      ctrl = new AbortController();
      try {
        results = (await api.search(term, { project: project || undefined }, ctrl.signal)) ?? [];
        loading = false;
      } catch (err) {
        if ((err as Error).name === 'AbortError') return;
        error = errorMessage(err);
        loading = false;
      }
    }, 160);
  }

  function open(r: SearchResult) {
    onclose();
    switch (r.kind) {
      case 'room':
        app.go({ name: 'room', roomId: r.id });
        break;
      case 'engineer':
        app.go({ name: 'engineer', id: r.id });
        break;
      case 'project':
        app.go({ name: 'project', id: r.id });
        break;
      case 'job': {
        const job = app.data.jobs[r.id];
        const roomId = r.roomId || job?.source.roomId;
        if (roomId) app.go({ name: 'room', roomId }, { panel: { kind: 'job', id: r.id } });
        else app.openPanel({ kind: 'job', id: r.id });
        break;
      }
      case 'message':
        if (r.roomId) app.go({ name: 'room', roomId: r.roomId }, { msg: r.id, panel: r.threadId ? { kind: 'thread', id: r.threadId } : null });
        break;
      case 'decision':
        if (r.roomId) app.go({ name: 'room', roomId: r.roomId }, { panel: { kind: 'decision', id: r.id } });
        else app.openPanel({ kind: 'decision', id: r.id });
        break;
    }
  }

  function move(by: 1 | -1) {
    if (shown.length === 0) return;
    const i = Math.min(shown.length - 1, Math.max(0, active + by));
    activeKey = keyOf(shown[i]);
    document.getElementById(`sr-${i}`)?.scrollIntoView({ block: 'nearest' });
  }

  // Arrow keys move through the results and Enter opens one; Escape is the
  // dialog's (Astryx's layer stack closes the topmost layer).
  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      move(1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      move(-1);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const r = shown[active];
      if (r) open(r);
    }
  }
</script>

{#snippet bar()}
  <LayoutHeader hasDivider padding={0}>
    <div class="bar">
      <TextInput
        label="Search"
        isLabelHidden
        size="lg"
        width="100%"
        placeholder="Search rooms, people, work, work IDs and decisions"
        hasAutoFocus
        isLoading={loading}
        {...combobox}
        value={q}
        onChange={(v) => {
          q = v;
          onInput();
        }}
        onkeydown={onKey}
      >
        {#snippet startIcon()}<Icon icon={Search} size="sm" color="secondary" />{/snippet}
      </TextInput>
      {#if projects.length}
        <div class="scope">
          <Selector
            label="Project"
            isLabelHidden
            variant="ghost"
            size="sm"
            options={scopes}
            value={project || ALL}
            onChange={(v: string) => {
              project = v === ALL ? '' : v;
              onInput();
            }}
          />
        </div>
      {/if}
      <span class="esc"><Kbd keys="escape" /></span>
    </div>
  </LayoutHeader>
{/snippet}

{#snippet list()}
  <LayoutContent padding={0}>
    <div class="results" id="search-results" role="listbox" aria-label="Results" aria-busy={loading}>
      {#if error}
        <div class="state"><Notice tone="danger" role="alert">{error}</Notice></div>
      {:else if shown.length === 0}
        <Text as="p" color="secondary" class="state">
          {loading ? 'Searching…' : q.trim().length >= 2 ? `Nothing matches “${q.trim()}” in the rooms you can see.` : 'Type to search messages, work and decisions.'}
        </Text>
      {/if}
      {#each groups as g (g.kind)}
        <div role="group" aria-labelledby="sg-{g.kind}">
          <p class="group" id="sg-{g.kind}">{LABEL[g.kind] ?? g.kind}</p>
          {#each g.items as { r, i } (keyOf(r))}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div
              id="sr-{i}"
              class="item"
              class:active={i === active}
              role="option"
              aria-selected={i === active}
              tabindex="-1"
              onclick={() => open(r)}
              onmousemove={() => (activeKey = keyOf(r))}
            >
              <span class="ic"><Icon icon={ICON[r.kind] ?? Info} size="sm" /></span>
              <span class="text">
                <span class="title">{r.title}</span>
                {#if r.snippet}<span class="snippet">{#each snippetParts(r.snippet.slice(0, 220)) as part, pi (pi)}{#if part.match}<mark>{part.text}</mark>{:else}{part.text}{/if}{/each}</span>{/if}
              </span>
              <span class="where">
                {#if r.roomId && r.kind !== 'room' && app.data.rooms[r.roomId]}in {app.data.rooms[r.roomId].name}{/if}
                {#if r.at}{' · '}{relative(r.at, app.now)}{/if}
              </span>
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </LayoutContent>
{/snippet}

{#snippet foot()}
  <LayoutFooter hasDivider padding={0}>
    <p class="foot" aria-hidden="true">
      <Kbd keys="up" /><Kbd keys="down" /> to move · <Kbd keys="enter" /> to open · <Kbd keys="escape" /> to close
    </p>
  </LayoutFooter>
{/snippet}

<Dialog isOpen onOpenChange={(open) => !open && onclose()} class="search" aria-label="Search" width={672} maxHeight={app.narrow ? 'calc(100dvh - 16px)' : 'min(64vh, 600px)'} padding={0}>
  <!-- Phones have no arrow keys to hint at. -->
  <Layout header={bar} content={list} footer={app.narrow ? undefined : foot} />
</Dialog>

<style>
  /* Anchored near the top, over a blurred veil, like any command palette. */
  :global(dialog.search) {
    margin-block: 18vh auto;
  }
  :global(dialog.search::backdrop) {
    backdrop-filter: blur(5px);
  }
  .bar {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    min-height: 52px;
    padding-inline: var(--spacing-3) var(--spacing-4);
  }
  /* The whole palette is the field: the input takes no box of its own. */
  .bar :global(.astryx-text-input) {
    padding-inline: var(--spacing-1);
    border-color: transparent;
    box-shadow: none;
    background: transparent;
  }
  .bar :global(input) {
    font-size: var(--font-size-lg);
  }
  .bar > :global(:first-child) {
    flex: 1;
    min-width: 0;
  }
  .scope {
    flex: none;
    max-width: 180px;
  }
  .results {
    padding: var(--spacing-1-5);
  }
  .results :global(.state) {
    padding: var(--spacing-3) var(--spacing-2);
  }
  .group {
    padding: var(--spacing-3) var(--spacing-2) var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    color: var(--color-text-secondary);
  }
  .item {
    display: grid;
    grid-template-columns: 28px minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--spacing-3);
    min-height: 44px;
    padding: var(--spacing-1-5) var(--spacing-2);
    border-radius: var(--radius-element);
    cursor: pointer;
  }
  .item.active {
    background: var(--color-overlay-pressed);
  }
  .ic {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-inner);
    background: var(--color-background-muted);
    color: var(--color-icon-secondary);
  }
  .text {
    display: grid;
    min-width: 0;
  }
  .title,
  .snippet {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .title {
    font-weight: var(--font-weight-semibold);
  }
  .snippet {
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  .where {
    white-space: nowrap;
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  mark {
    background: var(--color-warning-muted);
    color: var(--color-text-primary);
    border-radius: var(--radius-inner);
    box-shadow: inset 0 -2px 0 var(--yip-attention-fill);
  }
  .foot {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-1);
    padding: var(--spacing-2) var(--spacing-4);
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  @media (max-width: 520px) {
    .esc {
      display: none;
    }
  }
  @media (max-width: 768px) {
    :global(dialog.search) {
      margin-block-start: var(--spacing-2);
    }
    .where {
      display: none;
    }
  }
</style>
