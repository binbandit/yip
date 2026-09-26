<script lang="ts">
  // An engineer's notes from earlier work (spec §8, context layer 5): what
  // they keep, where each note can be used, where it came from, and when it's
  // due for review. Suggestions wait here quietly; nothing asks for them.
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { EngineerNote } from '../lib/api/types.gen';
  import { atTime } from '../lib/util/time';

  interface Props {
    engineerId: string;
    name: string;
  }
  let { engineerId, name }: Props = $props();

  let error = $state('');
  onMount(() => {
    api
      .engineerNotes(engineerId)
      .then((ns) => {
        for (const n of ns ?? []) app.data.notes[n.id] = n;
      })
      .catch((err) => (error = errorMessage(err)));
  });

  const all = $derived(
    Object.values(app.data.notes)
      .filter((n) => n.engineerId === engineerId && (n.status === 'accepted' || n.status === 'proposed'))
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
  );
  const kept = $derived(all.filter((n) => n.status === 'accepted'));
  const suggested = $derived(all.filter((n) => n.status === 'proposed'));
  const due = (n: EngineerNote) => new Date(n.reviewAfter).getTime() <= app.now;

  function scopeLabel(n: EngineerNote): string {
    const where = n.scope.kind === 'project' ? (app.data.projects[n.scope.id]?.name ?? 'a project') : `#${app.data.rooms[n.scope.id]?.name ?? 'a room'}`;
    const only = n.visibleRoomIds?.length ? ` · only in ${n.visibleRoomIds.map((r) => app.data.rooms[r]?.name ?? 'a private room').join(', ')}` : '';
    return where + only;
  }
  function sourceHref(n: EngineerNote): { href: string; label: string } | null {
    const s = n.sources[0];
    if (!s) return null;
    if (s.kind === 'job') return { href: app.panelHref({ kind: 'job', id: s.id }), label: app.data.jobs[s.id]?.title ?? 'the work' };
    if (s.kind === 'message' && s.roomId) return { href: `/rooms/${s.roomId}?msg=${s.id}`, label: `a message in ${app.data.rooms[s.roomId]?.name ?? 'a room'}` };
    return null;
  }

  let busy = $state('');
  async function act(n: EngineerNote, action: 'accept' | 'reject' | 'renew' | 'remove') {
    busy = n.id;
    error = '';
    try {
      app.data.notes[n.id] = await api.decideNote(n.id, { action, version: n.version });
    } catch (err) {
      error = err instanceof ApiError && err.conflict ? 'This note changed meanwhile.' : errorMessage(err);
    } finally {
      busy = '';
    }
  }

  // Writing a note (or correcting one).
  let draft = $state<null | { body: string; scope: string; supersedes?: EngineerNote }>(null);
  const scopes = $derived([
    ...Object.values(app.data.projects).map((p) => ({ value: `project:${p.id}`, label: p.name })),
    ...Object.values(app.data.rooms)
      .filter((r) => r.kind === 'room' && r.members.some((m) => m.kind === 'engineer' && m.id === engineerId))
      .map((r) => ({ value: `room:${r.id}`, label: `#${r.name}` })),
  ]);
  function startNote(supersedes?: EngineerNote) {
    error = '';
    draft = { body: supersedes?.body ?? '', scope: supersedes ? `${supersedes.scope.kind}:${supersedes.scope.id}` : (scopes[0]?.value ?? ''), supersedes };
  }
  async function saveNote(e: SubmitEvent) {
    e.preventDefault();
    if (!draft) return;
    const [kind, id] = draft.scope.split(':');
    busy = 'new';
    error = '';
    try {
      const n = await api.createNote(engineerId, { body: draft.body.trim(), scope: { kind, id }, supersedesId: draft.supersedes?.id });
      app.data.notes[n.id] = n;
      if (draft.supersedes) app.data.notes[draft.supersedes.id] = { ...draft.supersedes, status: 'superseded', supersededById: n.id };
      draft = null;
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = '';
    }
  }
</script>

<section class="section" aria-labelledby="eng-notes">
  <div class="head">
    <h2 class="section-title" id="eng-notes">Notes {name} keeps</h2>
    {#if !draft}<button class="btn btn-sm btn-quiet" onclick={() => startNote()}>Write a note</button>{/if}
  </div>
  <p class="meta lead">
    Short conclusions from earlier work. {name} sees a note only in conversations where its sources are visible, and not once it's due for review.
  </p>
  {#if error}<p class="form-error" role="alert">{error}</p>{/if}

  {#if draft}
    <form class="panel-box form" onsubmit={saveNote}>
      <label class="field">
        <span class="label">{draft.supersedes ? 'Corrected note' : 'Note'}</span>
        <textarea class="input" rows="3" maxlength="400" bind:value={draft.body}></textarea>
      </label>
      {#if !draft.supersedes}
        <label class="field">
          <span class="label">Where it applies</span>
          <select class="select" bind:value={draft.scope}>
            {#each scopes as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
          </select>
        </label>
      {/if}
      <div class="row">
        <button class="btn btn-primary btn-sm" type="submit" disabled={busy === 'new' || !draft.body.trim()}>{draft.supersedes ? 'Save correction' : 'Keep note'}</button>
        <button class="btn btn-sm" type="button" onclick={() => (draft = null)}>Cancel</button>
      </div>
    </form>
  {/if}

  {#if kept.length === 0 && suggested.length === 0 && !draft}
    <p class="meta">None yet. {name} keeps notes from finished work; you can write one too.</p>
  {/if}

  {#if kept.length}
    <ul class="notes">
      {#each kept as n (n.id)}
        {@const src = sourceHref(n)}
        <li class:due={due(n)}>
          {#if n.kind === 'record'}<p class="tag meta">Work record · written by yip when the work finished</p>{/if}
          <p class="body">{n.body}</p>
          <p class="meta">
            {scopeLabel(n)}{#if src}{' · from '}<a href={src.href}>{src.label}</a>{:else}{' · written by you'}{/if}
            {' · '}{due(n) ? 'due for review — not used until renewed' : `review ${atTime(n.reviewAfter)}`}
          </p>
          <div class="acts">
            {#if due(n)}<button class="btn btn-sm" disabled={busy === n.id} onclick={() => act(n, 'renew')}>Still true</button>{/if}
            <button class="btn btn-sm btn-quiet" onclick={() => startNote(n)}>Correct</button>
            <button class="btn btn-sm btn-quiet" disabled={busy === n.id} onclick={() => act(n, 'remove')}>Remove</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}

  {#if suggested.length}
    <h3 class="sub">Suggested by {name}</h3>
    <ul class="notes">
      {#each suggested as n (n.id)}
        {@const src = sourceHref(n)}
        <li>
          <p class="body">{n.body}</p>
          <p class="meta">{scopeLabel(n)}{#if src}{' · from '}<a href={src.href}>{src.label}</a>{/if}</p>
          <div class="acts">
            <button class="btn btn-sm" disabled={busy === n.id} onclick={() => act(n, 'accept')}>Keep</button>
            <button class="btn btn-sm btn-quiet" disabled={busy === n.id} onclick={() => act(n, 'reject')}>Discard</button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
  }
  .lead {
    margin: 2px 0 10px;
  }
  .sub {
    margin: 14px 0 4px;
    font-size: 13px;
    font-weight: 600;
    color: var(--ink-secondary);
  }
  .notes {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
  }
  .notes li {
    padding: 10px 0;
    border-top: 1px solid var(--line-soft);
    display: grid;
    gap: 4px;
  }
  .notes li:first-child {
    border-top: 0;
  }
  .notes li.due .body {
    color: var(--ink-secondary);
  }
  .body {
    margin: 0;
  }
  .tag {
    margin: 0;
    font-size: 12px;
  }
  .acts {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
</style>
