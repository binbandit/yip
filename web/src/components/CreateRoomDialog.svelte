<script lang="ts">
  import type { Attachment } from 'svelte/attachments';
  import { Button, CheckboxInput, CheckboxList, CheckboxListItem, Link, RadioList, RadioListItem, Selector, Text, TextInput } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import Dialog from './Dialog.svelte';
  import Avatar from './Avatar.svelte';

  interface Props {
    kind: 'room' | 'dm';
    onclose: () => void;
  }
  let { kind, onclose }: Props = $props();

  const engineers = $derived(Object.values(app.data.engineers).filter((e) => !e.archived));
  const projects = $derived(Object.values(app.data.projects));

  let name = $state('');
  let purpose = $state('');
  let isPrivate = $state(false);
  let engineerIds = $state<string[]>([]);
  let projectIds = $state<string[]>([]);
  let replyMode = $state<'quiet' | 'steward'>('quiet');
  let stewardId = $state('');
  let dmWith = $state('');
  let busy = $state(false);
  let error = $state('');

  // The first choice takes focus when the dialog opens (Dialog focuses `[data-autofocus]`).
  const focusFirstChoice: Attachment<HTMLElement> = (el) => {
    el.querySelector('input')?.setAttribute('data-autofocus', '');
  };

  function setEngineers(ids: string[]) {
    engineerIds = ids;
    // The steward is one of the room's engineers: unticking them clears it.
    if (!ids.includes(stewardId)) stewardId = '';
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (kind === 'dm') {
      if (!dmWith) {
        error = 'Choose an engineer.';
        return;
      }
      const existing = app.rooms.find((r) => r.kind === 'dm' && r.members.some((m) => m.kind === 'engineer' && m.id === dmWith));
      if (existing) {
        onclose();
        app.go({ name: 'room', roomId: existing.id });
        return;
      }
    } else {
      if (!name.trim()) {
        error = 'Give the room a name.';
        return;
      }
      if (replyMode === 'steward' && !stewardId) {
        error = 'Choose which engineer answers unaddressed messages, or keep the room quiet.';
        return;
      }
    }
    busy = true;
    try {
      const room = await api.createRoom(
        kind === 'dm'
          ? { name: '', purpose: '', kind: 'dm', private: true, replyMode: 'steward', stewardId: dmWith, engineerIds: [dmWith], projectIds: [] }
          : {
              name: name.trim(),
              purpose: purpose.trim(),
              kind: 'room',
              private: isPrivate,
              replyMode,
              stewardId: replyMode === 'steward' ? stewardId : '',
              engineerIds,
              projectIds,
            },
      );
      app.data.rooms[room.id] = room;
      onclose();
      app.go({ name: 'room', roomId: room.id });
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<Dialog title={kind === 'dm' ? 'Message an engineer' : 'Create a room'} {onclose} width={560} purpose="form">
  <form id="create-room" class="form" onsubmit={create} novalidate>
    {#if kind === 'dm'}
      {#if engineers.length === 0}
        <Text as="p" type="supporting">No engineers yet. <Link hasUnderline href={workspaceUrl('/engineers')}>Create one first.</Link></Text>
      {:else}
        <div {@attach focusFirstChoice}>
          <RadioList label="Who" value={dmWith} onChange={(v) => (dmWith = v)} htmlName="dm">
            {#each engineers as e (e.id)}
              <RadioListItem label={e.name} description={e.role} value={e.id} class="check">
                {#snippet startContent()}<span class="avatar"><Avatar actor={{ kind: 'engineer', id: e.id }} size={24} /></span>{/snippet}
              </RadioListItem>
            {/each}
          </RadioList>
        </div>
      {/if}
      <Text as="p" type="supporting">A direct message is private to you and that engineer; they answer everything you write there.</Text>
    {:else}
      <TextInput label="Name" bind:value={name} placeholder="e.g. Payments" width="100%" hasAutoFocus />
      <TextInput label="Purpose" isOptional bind:value={purpose} placeholder="What this group works on" width="100%" />
      <CheckboxList label="Engineers" density="compact" description={engineers.length ? undefined : 'No engineers yet — you can add them later.'} value={engineerIds} onChange={setEngineers}>
        {#each engineers as e (e.id)}
          <CheckboxListItem value={e.id} aria-label={e.name} description={e.role} class="check">
            {#snippet label()}<span class="who"><Avatar actor={{ kind: 'engineer', id: e.id }} size={24} /><strong>{e.name}</strong></span>{/snippet}
          </CheckboxListItem>
        {/each}
      </CheckboxList>
      {#if projects.length}
        <CheckboxList label="Projects" density="compact" description="Optional — rooms can range across projects" value={projectIds} onChange={(v) => (projectIds = v)}>
          {#each projects as p (p.id)}
            <CheckboxListItem value={p.id} label={p.name} class="check" />
          {/each}
        </CheckboxList>
      {/if}
      <div class="reply">
        <RadioList label="Who replies to unaddressed messages" value={replyMode} onChange={(v) => (replyMode = v === 'steward' ? 'steward' : 'quiet')} htmlName="mode">
          <RadioListItem label="Nobody — only engineers you mention reply" value="quiet" class="check" />
          <RadioListItem label="A steward answers" value="steward" class="check" />
        </RadioList>
        {#if replyMode === 'steward'}
          <Selector
            label="Steward"
            isLabelHidden
            placeholder="Choose an engineer from this room…"
            options={engineers.filter((e) => engineerIds.includes(e.id)).map((e) => ({ value: e.id, label: e.name }))}
            value={stewardId || undefined}
            onChange={(v: string) => (stewardId = v)}
            width="100%"
          />
        {/if}
      </div>
      <CheckboxInput label="Private" description="History here isn't carried into other rooms." value={isPrivate} onChange={(v) => (isPrivate = v)} class="check" />
    {/if}
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  </form>
  {#snippet footer()}
    <Button label="Cancel" onclick={onclose} />
    <Button label={busy ? 'Creating…' : kind === 'dm' ? 'Open conversation' : 'Create room'} variant="primary" type="submit" form="create-room" isLoading={busy} />
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: var(--spacing-4);
  }
  .who {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-2);
  }
  .who strong {
    font-weight: var(--font-weight-semibold);
  }
  .avatar {
    display: inline-flex;
    margin-inline-start: var(--spacing-1);
  }
  .reply {
    display: grid;
    gap: var(--spacing-2);
  }
</style>
