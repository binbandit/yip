<script lang="ts">
  import { untrack } from 'svelte';
  import { Button, Text, TextInput } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import { mergeRoom } from '../lib/state/data';
  import type { Room } from '../lib/api/types.gen';
  import Dialog from './Dialog.svelte';
  import Notice from './Notice.svelte';

  let { room, onclose }: { room: Room; onclose: () => void } = $props();
  // An explicit save uses the version the user started editing, even if a
  // newer room event arrives while the dialog is open.
  let name = $state(untrack(() => room.name));
  let version = untrack(() => room.version);
  const data = app.data;
  let busy = $state(false);
  let error = $state('');
  let currentName = $state('');
  let alive = true;
  const id = $props.id();
  $effect(() => () => { alive = false; });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    error = '';
    if (!name.trim()) {
      error = 'Give the room a name.';
      return;
    }
    busy = true;
    try {
      const updated = await api.updateRoom(room.id, { version, name: name.trim() });
      if (!alive || app.data !== data) return;
      mergeRoom(data, updated);
      app.announce(`Room renamed to ${updated.name}.`);
      onclose();
    } catch (err) {
      if (!alive || app.data !== data) return;
      error = errorMessage(err);
      if (err instanceof ApiError && err.conflict) {
        try {
          const latest = await api.room(room.id);
          if (!alive || app.data !== data) return;
          mergeRoom(data, latest);
          version = latest.version;
          currentName = latest.name;
          error = 'This room changed. Your name has not been saved. Review the current name before trying again.';
        } catch (refreshError) {
          error = errorMessage(refreshError);
        }
      }
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title="Rename room" description="Change the name for everyone in this room." {onclose} width={440} purpose="form">
  <form id={id} onsubmit={save} novalidate>
    <TextInput label="Name" bind:value={name} width="100%" hasAutoFocus />
    {#if currentName}<Text as="p" type="supporting">Current name: {currentName}</Text>{/if}
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  </form>
  {#snippet footer()}
    <Button label="Cancel" onclick={onclose} />
    <Button label="Save name" type="submit" form={id} variant="primary" isLoading={busy} />
  {/snippet}
</Dialog>

<style>
  form { display: grid; gap: var(--spacing-3); }
</style>
