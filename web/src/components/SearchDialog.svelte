<script lang="ts">
  // ⌘K search. Results are grouped and each opens its actual source: a room,
  // a message scrolled into view, a job drawer, a profile, a decision.
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { SearchResult } from '../lib/api/types.gen';
  import { pushLayer, trapFocus } from '../lib/ui/layers';
  import { relative } from '../lib/util/time';
  import { snippetParts } from '../lib/util/markdown';
  import Icon from './Icon.svelte';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();

  let q = $state('');
  let results = $state<SearchResult[]>([]);
  let loading = $state(false);
  let error = $state('');
  let active = $state(0);
  let input: HTMLInputElement | undefined = $state();
  let dialog: HTMLDialogElement | undefined = $state();
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
  const ICON: Record<string, string> = { room: 'hash', engineer: 'users', job: 'commit', message: 'reply', decision: 'book', project: 'folder' };

  // Before typing: jump straight to rooms and people.
  const quick: SearchResult[] = $derived.by(() => {
    const s = q.trim().toLowerCase();
    const rooms = app.rooms
      .filter((r) => r.kind !== 'overview' && (!s || r.name.toLowerCase().includes(s)))
      .map((r) => ({ kind: 'room', id: r.id, title: r.kind === 'dm' ? `${r.name} (direct)` : r.name, snippet: r.purpose, roomId: r.id }));
    const people = Object.values(app.data.engineers)
      .filter((e) => !s || e.name.toLowerCase().includes(s) || e.role.toLowerCase().includes(s))
      .map((e) => ({ kind: 'engineer', id: e.id, title: e.name, snippet: e.role }));
    return [...rooms, ...people];
  });

  const shown: SearchResult[] = $derived.by(() => {
    const base = q.trim().length >= 2 ? results : [];
    const seen = new Set(base.map((r) => r.kind + r.id));
    const merged = [...base, ...quick.filter((r) => !seen.has(r.kind + r.id))];
    return merged.sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind));
  });

  const groups = $derived.by(() => {
    const out: { kind: string; items: { r: SearchResult; i: number }[] }[] = [];
    shown.forEach((r, i) => {
      let g = out.find((x) => x.kind === r.kind);
      if (!g) out.push((g = { kind: r.kind, items: [] }));
      g.items.push({ r, i });
    });
    return out;
  });

  onMount(() => {
    const release = pushLayer(onclose);
    try {
      dialog?.showModal();
    } catch {
      dialog?.setAttribute('open', '');
    }
    const untrap = dialog ? trapFocus(dialog) : () => {};
    input?.focus();
    return () => {
      untrap();
      ctrl?.abort();
      if (dialog?.open) dialog.close();
      release();
    };
  });

  function onInput() {
    active = 0;
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
        results = (await api.search(term, undefined, ctrl.signal)) ?? [];
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
        app.go(r.roomId ? { name: 'room', roomId: r.roomId } : { name: 'overview' }, { panel: { kind: 'decision', id: r.id } });
        break;
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      active = Math.min(shown.length - 1, active + 1);
      document.getElementById(`sr-${active}`)?.scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      active = Math.max(0, active - 1);
      document.getElementById(`sr-${active}`)?.scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const r = shown[active];
      if (r) open(r);
    }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={dialog}
  class="search"
  aria-label="Search"
  oncancel={(e) => e.preventDefault()}
  onclick={(e) => e.target === dialog && onclose()}
>
  <div class="bar">
    <Icon name="search" />
    <input
      bind:this={input}
      bind:value={q}
      class="q"
      type="search"
      placeholder="Search rooms, people, work, messages and decisions"
      aria-label="Search"
      role="combobox"
      aria-expanded={shown.length > 0}
      aria-controls="search-results"
      aria-activedescendant={shown.length ? `sr-${active}` : undefined}
      aria-autocomplete="list"
      autocomplete="off"
      spellcheck="false"
      oninput={onInput}
      onkeydown={onKey}
    />
    <kbd>Esc</kbd>
  </div>
  <div class="results" id="search-results" role="listbox" aria-label="Results" aria-busy={loading}>
    {#if error}
      <p class="state notice danger">{error}</p>
    {:else if shown.length === 0}
      <p class="state meta">{loading ? 'Searching…' : q.trim().length >= 2 ? `Nothing matches “${q.trim()}” in the rooms you can see.` : 'Type to search messages, work and decisions.'}</p>
    {/if}
    {#each groups as g (g.kind)}
      <div role="group" aria-labelledby="sg-{g.kind}">
        <p class="group" id="sg-{g.kind}">{LABEL[g.kind] ?? g.kind}</p>
        {#each g.items as { r, i } (r.kind + r.id)}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <div
            id="sr-{i}"
            class="item"
            class:active={i === active}
            role="option"
            aria-selected={i === active}
            tabindex="-1"
            onclick={() => open(r)}
            onmousemove={() => (active = i)}
          >
            <span class="ic"><Icon name={ICON[r.kind] ?? 'info'} size={16} /></span>
            <span class="text">
              <span class="title truncate">{r.title}</span>
              {#if r.snippet}<span class="snippet truncate">{#each snippetParts(r.snippet.slice(0, 220)) as part, pi (pi)}{#if part.match}<mark>{part.text}</mark>{:else}{part.text}{/if}{/each}</span>{/if}
            </span>
            <span class="where meta">
              {#if r.roomId && r.kind !== 'room' && app.data.rooms[r.roomId]}in {app.data.rooms[r.roomId].name}{/if}
              {#if r.at} · {relative(r.at, app.now)}{/if}
            </span>
          </div>
        {/each}
      </div>
    {/each}
  </div>
  <p class="foot meta" aria-hidden="true">↑↓ to move · Enter to open · Esc to close</p>
</dialog>

<style>
  .search {
    width: min(680px, calc(100vw - 24px));
    max-height: min(70vh, 640px);
    margin: 12vh auto auto;
    padding: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-surface);
    background: var(--surface);
    color: var(--ink);
    box-shadow: var(--shadow-pop);
    overflow: hidden;
  }
  .search[open] {
    display: flex;
    flex-direction: column;
    animation: pop var(--t-slow) var(--ease);
  }
  .search::backdrop {
    background: var(--veil);
    backdrop-filter: blur(3px);
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 14px;
    height: 54px;
    border-bottom: 1px solid var(--line);
    color: var(--ink-secondary);
  }
  .q {
    flex: 1;
    min-width: 0;
    height: 100%;
    border: 0;
    background: transparent;
    font-size: 16px;
    color: var(--ink);
  }
  .q:focus-visible {
    outline: none;
  }
  .results {
    overflow: auto;
    padding: 6px;
    min-height: 0;
  }
  .state {
    padding: 14px 10px;
  }
  .group {
    padding: 10px 10px 4px;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--ink-secondary);
  }
  .item {
    display: grid;
    grid-template-columns: 28px minmax(0, 1fr) auto;
    align-items: center;
    gap: 10px;
    min-height: 44px;
    padding: 6px 10px;
    border-radius: 10px;
    cursor: pointer;
  }
  .item.active {
    background: var(--accent-subtle);
    box-shadow: inset 0 0 0 1px var(--accent);
  }
  .ic {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: 8px;
    background: var(--surface-subtle);
    color: var(--ink-secondary);
  }
  .text {
    display: grid;
    min-width: 0;
  }
  .title {
    font-weight: 600;
    font-size: 14.5px;
  }
  .snippet {
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .where {
    white-space: nowrap;
  }
  mark {
    background: var(--attention-subtle);
    color: var(--ink);
    border-radius: 3px;
    box-shadow: inset 0 -2px 0 var(--attention-fill);
  }
  .foot {
    padding: 8px 14px;
    border-top: 1px solid var(--line);
  }
  @keyframes pop {
    from {
      opacity: 0;
      transform: translateY(-6px) scale(0.99);
    }
  }
  @media (max-width: 760px) {
    .search {
      margin-top: 8px;
      max-height: calc(100vh - 16px);
    }
    .where {
      display: none;
    }
    .foot {
      display: none;
    }
  }
</style>
