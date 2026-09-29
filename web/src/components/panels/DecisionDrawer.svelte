<script lang="ts">
  // A remembered decision with its provenance: scope, status, who proposed
  // and who accepted it, supersession, and links to its sources.
  import { untrack } from 'svelte';
  import { Button, Heading, Link, Text, TextArea, TextInput } from '@astryx-svelte/core';
  import { app } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { api } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import { atTime } from '../../lib/util/time';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import MessageBody from '../MessageBody.svelte';
  import Notice from '../Notice.svelte';

  interface Props {
    decisionId: string;
    mode: PanelMode;
  }
  let { decisionId, mode }: Props = $props();
  let error = $state('');
  let busy = $state(false);

  $effect(() => {
    const id = decisionId;
    untrack(() =>
      details.ensureDecision(id).catch((e) => {
        error = e instanceof ApiError && (e.status === 404 || e.status === 403) ? "This decision doesn't exist or isn't visible from your rooms." : errorMessage(e);
      }),
    );
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

  // Correcting an accepted decision records a new one that replaces it, with
  // the same sources (so it stays exactly as visible), accepted by you.
  let correcting = $state<{ title: string; body: string } | null>(null);
  async function correct(e: SubmitEvent) {
    e.preventDefault();
    if (!d || !correcting) return;
    if (!correcting.title.trim() || !correcting.body.trim()) {
      error = 'A decision needs a title and what was decided.';
      return;
    }
    busy = true;
    error = '';
    try {
      const nd = await api.createDecision({ scope: d.scope, title: correcting.title.trim(), body: correcting.body.trim(), sources: d.sources, supersedesId: d.id, accept: true });
      app.data.decisions[nd.id] = nd;
      correcting = null;
      app.toast('Recorded the corrected decision; it replaces the earlier one.');
      app.openPanel({ kind: 'decision', id: nd.id });
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
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
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
    {#if !d}
      {#if !error}<Text as="p" type="supporting">Loading…</Text>{/if}
    {:else}
      <p class="status">
        <strong>{d.status === 'accepted' ? 'Accepted' : d.status === 'proposed' ? 'Proposed' : d.status === 'superseded' ? 'Superseded' : 'Rejected'}</strong>
        <Text type="supporting">
          · proposed by {app.actorName(d.createdBy)} {atTime(d.createdAt)}
          {#if d.acceptedBy}
            · accepted {d.acceptedBy.kind === 'system' ? 'automatically under the project policy' : `by ${app.actorName(d.acceptedBy)}`}
            {atTime(d.acceptedAt)}{/if}
        </Text>
      </p>
      <MessageBody message={{ body: d.body, mentions: [] }} />
      {#if d.supersededById}
        <Notice tone="warning" title="A newer decision replaces this one.">
          {#snippet end()}
            <Button size="sm" label="Open it" onclick={() => app.openPanel({ kind: 'decision', id: d.supersededById! })} />
          {/snippet}
        </Notice>
      {/if}
      {#if d.supersedesId}
        <Text as="p" type="supporting"
          >Replaces <Link onclick={() => app.openPanel({ kind: 'decision', id: d.supersedesId! })} type="inherit" hasUnderline>an earlier decision</Link>.</Text
        >
      {/if}
      <section class="sources">
        <Heading level={3}>Sources</Heading>
        {#if d.sources.length === 0}
          <Text as="p" type="supporting">No sources recorded.</Text>
        {:else}
          <ul>
            {#each d.sources as s (s.kind + s.id)}
              {@const h = sourceHref(s)}
              <li>
                {#if h}<Link hasUnderline href={h}
                    >{s.kind === 'message' ? `Message in ${app.data.rooms[s.roomId ?? '']?.name ?? 'a room'}` : s.kind === 'job' ? (app.data.jobs[s.id]?.title ?? 'The work') : s.kind}</Link
                  >{:else}{s.kind}{/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>
      <Text as="p" type="supporting">
        {#if d.visibleRoomIds == null}
          Visible wherever its scope allows.
        {:else if d.visibleRoomIds.length === 0}
          Restricted: not visible from any room.
        {:else}
          Visible only from: {d.visibleRoomIds.map((r) => app.data.rooms[r]?.name ?? 'a private room').join(', ')}.
        {/if}
      </Text>
      {#if d.status === 'accepted' && !d.supersededById}
        {#if correcting}
          <form class="correct" onsubmit={correct}>
            <TextInput label="Title" width="100%" bind:value={correcting.title} />
            <TextArea label="What was decided" width="100%" rows={5} bind:value={correcting.body} />
            <Text as="p" type="supporting">Engineers use the corrected version from now on; the earlier one stays in the history, marked as replaced.</Text>
            <div class="actions">
              <Button size="sm" variant="primary" type="submit" label="Record correction" isDisabled={busy} />
              <Button size="sm" label="Cancel" onclick={() => (correcting = null)} />
            </div>
          </form>
        {:else}
          <div class="actions">
            <Button size="sm" label="Correct this decision" onclick={() => (correcting = { title: d!.title, body: d!.body })} />
          </div>
        {/if}
      {/if}
      {#if d.status === 'proposed'}
        <div class="actions">
          <Button size="sm" variant="primary" label="Accept" isDisabled={busy} onclick={() => act('accept')} />
          <Button size="sm" label="Reject" isDisabled={busy} onclick={() => act('reject')} />
        </div>
      {/if}
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: var(--spacing-3) var(--spacing-4) var(--spacing-6);
    display: grid;
    gap: var(--spacing-3);
  }
  .status strong {
    font-weight: var(--font-weight-semibold);
  }
  .sources {
    display: grid;
    gap: var(--spacing-1-5);
  }
  /* Section titles in a drawer sit below its 17px title. */
  .sources :global(h3.astryx-heading) {
    font-size: var(--text-heading-4-size);
    line-height: var(--text-heading-4-leading);
  }
  ul {
    padding-left: var(--spacing-5);
    list-style: disc;
  }
  .correct {
    display: grid;
    gap: var(--spacing-3);
    margin-top: var(--spacing-3);
  }
  .actions {
    display: flex;
    gap: var(--spacing-2);
  }
</style>
