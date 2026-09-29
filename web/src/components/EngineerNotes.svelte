<script lang="ts">
  // An engineer's notes from earlier work (spec §8, context layer 5): what
  // they keep, where each note can be used, where it came from, and when it's
  // due for review. Suggestions wait here quietly; nothing asks for them.
  import { onMount } from 'svelte';
  import { Button, Card, Link, Selector, Text, TextArea } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { EngineerNote } from '../lib/api/types.gen';
  import { atTime } from '../lib/util/time';
  import ScreenSection from './ScreenSection.svelte';

  interface Props {
    engineerId: string;
    name: string;
  }
  let { engineerId, name }: Props = $props();

  const NOTE_LIMIT = 400;
  // TextArea's maxLength only counts; the native attribute also stops typing at the limit.
  const noteHints = { maxlength: NOTE_LIMIT };

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

<ScreenSection title="Notes {name} keeps" id="eng-notes">
  {#snippet end()}
    {#if !draft}<Button label="Write a note" size="sm" variant="ghost" onclick={() => startNote()} />{/if}
  {/snippet}
  <div class="body">
    <Text as="p" type="supporting">
      Short conclusions from earlier work. {name} sees a note only in conversations where its sources are visible, and not once it's due for review.
    </Text>
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}

    {#if draft}
      <Card>
        <form class="form" onsubmit={saveNote}>
          <TextArea label={draft.supersedes ? 'Corrected note' : 'Note'} rows={3} maxLength={NOTE_LIMIT} {...noteHints} bind:value={draft.body} />
          {#if !draft.supersedes}
            <Selector
              label="Where it applies"
              options={scopes}
              value={draft.scope}
              onChange={(v: string) => {
                if (draft) draft.scope = v;
              }}
            />
          {/if}
          <div class="row">
            <Button
              label={draft.supersedes ? 'Save correction' : 'Keep note'}
              variant="primary"
              size="sm"
              type="submit"
              isLoading={busy === 'new'}
              isDisabled={!draft.body.trim()}
            />
            <Button label="Cancel" size="sm" onclick={() => (draft = null)} />
          </div>
        </form>
      </Card>
    {/if}

    {#if kept.length === 0 && suggested.length === 0 && !draft}
      <Text as="p" type="supporting">None yet. {name} keeps notes from finished work; you can write one too.</Text>
    {/if}

    {#if kept.length}
      <ul class="notes">
        {#each kept as n (n.id)}
          {@const src = sourceHref(n)}
          <li class:due={due(n)}>
            {#if n.kind === 'record'}<Text as="p" type="supporting">Work record · written by yip when the work finished</Text>{/if}
            <p class="note">{n.body}</p>
            <Text as="p" type="supporting">
              {scopeLabel(n)}{#if src}{' · from '}<Link hasUnderline type="inherit" href={src.href}>{src.label}</Link>{:else}{' · written by you'}{/if}
              {' · '}{due(n) ? 'due for review — not used until renewed' : `review ${atTime(n.reviewAfter)}`}
            </Text>
            <div class="row">
              {#if due(n)}<Button label="Still true" size="sm" isDisabled={busy === n.id} onclick={() => act(n, 'renew')} />{/if}
              <Button label="Correct" size="sm" variant="ghost" onclick={() => startNote(n)} />
              <Button label="Remove" size="sm" variant="ghost" isDisabled={busy === n.id} onclick={() => act(n, 'remove')} />
            </div>
          </li>
        {/each}
      </ul>
    {/if}

    {#if suggested.length}
      <div>
        <h3 class="sub">Suggested by {name}</h3>
        <ul class="notes">
          {#each suggested as n (n.id)}
            {@const src = sourceHref(n)}
            <li>
              <p class="note">{n.body}</p>
              <Text as="p" type="supporting">{scopeLabel(n)}{#if src}{' · from '}<Link hasUnderline type="inherit" href={src.href}>{src.label}</Link>{/if}</Text>
              <div class="row">
                <Button label="Keep" size="sm" isDisabled={busy === n.id} onclick={() => act(n, 'accept')} />
                <Button label="Discard" size="sm" variant="ghost" isDisabled={busy === n.id} onclick={() => act(n, 'reject')} />
              </div>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>
</ScreenSection>

<style>
  .body {
    display: grid;
    gap: var(--spacing-3);
  }
  .form {
    display: grid;
    gap: var(--spacing-3);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
  }
  .sub {
    margin-bottom: var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    color: var(--color-text-secondary);
  }
  .notes li {
    display: grid;
    gap: var(--spacing-1);
    padding: var(--spacing-3) 0;
    border-top: 1px solid var(--color-border);
  }
  .notes li:first-child {
    border-top: 0;
    padding-top: 0;
  }
  .notes li.due .note {
    color: var(--color-text-secondary);
  }
</style>
