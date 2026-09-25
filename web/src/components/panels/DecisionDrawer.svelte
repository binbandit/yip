<script lang="ts">
  // A remembered decision with its provenance: scope, status, who proposed
  // and who accepted it, supersession, and links to its sources.
  import { app } from '../../lib/state/app.svelte';
  import { api } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import { atTime } from '../../lib/util/time';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import MessageBody from '../MessageBody.svelte';

  interface Props {
    decisionId: string;
    mode: PanelMode;
  }
  let { decisionId, mode }: Props = $props();
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    if (app.data.decisions[decisionId]) return;
    api
      .decisions()
      .then((ds) => {
        for (const d of ds) app.data.decisions[d.id] = d;
        if (!app.data.decisions[decisionId]) error = "This decision doesn't exist or isn't visible from your rooms.";
      })
      .catch((e) => (error = errorMessage(e)));
  });

  const d = $derived(app.data.decisions[decisionId]);
  const scopeLabel = $derived(
    !d ? '' : d.scope.kind === 'project' ? `Project · ${app.data.projects[d.scope.id]?.name ?? 'a project'}` : d.scope.kind === 'room' ? `Room · ${app.data.rooms[d.scope.id]?.name ?? 'a room'}` : 'Whole workspace',
  );

  function sourceHref(s: { kind: string; id: string; roomId?: string }): string | null {
    if (s.kind === 'message' && s.roomId) return `/rooms/${s.roomId}?msg=${s.id}`;
    if (s.kind === 'job') return `${location.pathname}?panel=job%3A${s.id}`;
    if (s.kind === 'review') return `${location.pathname}?panel=review%3A${s.id}`;
    if (s.kind === 'decision') return `${location.pathname}?panel=decision%3A${s.id}`;
    return null;
  }

  async function act(action: 'accept' | 'reject') {
    if (!d) return;
    busy = true;
    error = '';
    try {
      app.data.decisions[d.id] = await api.decideDecision(d.id, { action, version: d.version });
    } catch (e) {
      error = e instanceof ApiError && e.conflict ? 'This decision changed since you opened it.' : errorMessage(e);
    } finally {
      busy = false;
    }
  }
</script>

<RightPanel title={d?.title ?? 'Decision'} {mode} onclose={() => app.closePanel()}>
  {#snippet subtitle()}{scopeLabel}{/snippet}
  <div class="pad">
    {#if error}<p class="notice danger" role="alert">{error}</p>{/if}
    {#if !d}
      {#if !error}<p class="meta">Loading…</p>{/if}
    {:else}
      <p class="status">
        <strong>{d.status === 'accepted' ? 'Accepted' : d.status === 'proposed' ? 'Proposed' : d.status === 'superseded' ? 'Superseded' : 'Rejected'}</strong>
        <span class="meta">
          · proposed by {app.actorName(d.createdBy)} {atTime(d.createdAt)}
          {#if d.acceptedBy}
            · accepted {d.acceptedBy.kind === 'system' ? 'automatically under the project policy' : `by ${app.actorName(d.acceptedBy)}`}
            {atTime(d.acceptedAt)}{/if}
        </span>
      </p>
      <MessageBody message={{ body: d.body, mentions: [] }} />
      {#if d.supersededById}
        <p class="notice attention">
          A newer decision replaces this one. <button class="link-btn" onclick={() => app.openPanel({ kind: 'decision', id: d.supersededById! })}>Open it</button>
        </p>
      {/if}
      {#if d.supersedesId}
        <p class="meta">Replaces <button class="link-btn" onclick={() => app.openPanel({ kind: 'decision', id: d.supersedesId! })}>an earlier decision</button>.</p>
      {/if}
      <section>
        <h3>Sources</h3>
        {#if d.sources.length === 0}
          <p class="meta">No sources recorded.</p>
        {:else}
          <ul>
            {#each d.sources as s (s.kind + s.id)}
              {@const h = sourceHref(s)}
              <li>
                {#if h}<a href={h}>{s.kind === 'message' ? `Message in ${app.data.rooms[s.roomId ?? '']?.name ?? 'a room'}` : s.kind === 'job' ? (app.data.jobs[s.id]?.title ?? 'The work') : s.kind}</a>{:else}{s.kind}{/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>
      <p class="meta">
        {#if (d.visibleRoomIds ?? []).length}
          Visible from: {(d.visibleRoomIds ?? []).map((r) => app.data.rooms[r]?.name ?? 'a private room').join(', ')}.
        {:else}
          Visible wherever its scope allows.
        {/if}
      </p>
      {#if d.status === 'proposed'}
        <div class="actions">
          <button class="btn btn-sm btn-primary" disabled={busy} onclick={() => act('accept')}>Accept</button>
          <button class="btn btn-sm" disabled={busy} onclick={() => act('reject')}>Reject</button>
        </div>
      {/if}
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: 14px 18px 24px;
    display: grid;
    gap: 12px;
  }
  .status {
    font-size: 14px;
  }
  h3 {
    font-size: 14px;
    margin-bottom: 6px;
  }
  ul {
    margin: 0;
    padding-left: 18px;
    font-size: 14px;
  }
  .actions {
    display: flex;
    gap: 8px;
  }
</style>
