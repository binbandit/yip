<script lang="ts">
  import { app } from '../lib/state/app.svelte';
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

  function toggle(list: string[], id: string): string[] {
    return list.includes(id) ? list.filter((x) => x !== id) : [...list, id];
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (kind === 'dm') {
      if (!dmWith) {
        error = 'Choose an engineer.';
        return;
      }
      busy = true;
      try {
        await app.messageEngineer(dmWith);
        onclose();
      } catch (err) {
        error = errorMessage(err);
        busy = false;
      }
      return;
    }
    if (!name.trim()) {
      error = 'Give the room a name.';
      return;
    }
    if (replyMode === 'steward' && !stewardId) {
      error = 'Choose which engineer answers unaddressed messages, or keep the room quiet.';
      return;
    }
    busy = true;
    try {
      const room = await api.createRoom({
        name: name.trim(),
        purpose: purpose.trim(),
        kind: 'room',
        private: isPrivate,
        replyMode,
        stewardId: replyMode === 'steward' ? stewardId : '',
        engineerIds,
        projectIds,
      });
      app.data.rooms[room.id] = room;
      onclose();
      app.go({ name: 'room', roomId: room.id });
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<Dialog title={kind === 'dm' ? 'Message an engineer' : 'Create a room'} {onclose} width={560} initialFocus="input, select">
  <form id="create-room" class="form" onsubmit={create}>
    {#if kind === 'dm'}
      <fieldset class="fs">
        <legend class="label">Who</legend>
        {#if engineers.length === 0}<p class="meta">No engineers yet. <a href="/engineers">Create one first.</a></p>{/if}
        {#each engineers as e (e.id)}
          <label class="check person"><input type="radio" name="dm" value={e.id} bind:group={dmWith} /><Avatar actor={{ kind: 'engineer', id: e.id }} size={24} /><span><strong>{e.name}</strong> <span class="meta">{e.role}</span></span></label>
        {/each}
      </fieldset>
      <p class="meta">A direct message is private to you and that engineer; they answer everything you write there.</p>
    {:else}
      <label class="field"><span class="label">Name</span><input class="input" bind:value={name} placeholder="e.g. Payments" /></label>
      <label class="field"><span class="label">Purpose <span class="meta">(optional)</span></span><input class="input" bind:value={purpose} placeholder="What this group works on" /></label>
      <fieldset class="fs">
        <legend class="label">Engineers</legend>
        {#if engineers.length === 0}<p class="meta">No engineers yet — you can add them later.</p>{/if}
        {#each engineers as e (e.id)}
          <label class="check person">
            <input type="checkbox" checked={engineerIds.includes(e.id)} onchange={() => (engineerIds = toggle(engineerIds, e.id))} />
            <Avatar actor={{ kind: 'engineer', id: e.id }} size={24} />
            <span><strong>{e.name}</strong> <span class="meta">{e.role}</span></span>
          </label>
        {/each}
      </fieldset>
      {#if projects.length}
        <fieldset class="fs">
          <legend class="label">Projects <span class="meta">(optional — rooms can range across projects)</span></legend>
          {#each projects as p (p.id)}
            <label class="check"><input type="checkbox" checked={projectIds.includes(p.id)} onchange={() => (projectIds = toggle(projectIds, p.id))} />{p.name}</label>
          {/each}
        </fieldset>
      {/if}
      <fieldset class="fs">
        <legend class="label">Who replies to unaddressed messages</legend>
        <label class="check"><input type="radio" name="mode" value="quiet" bind:group={replyMode} /><span>Nobody — only engineers you mention reply</span></label>
        <label class="check"><input type="radio" name="mode" value="steward" bind:group={replyMode} /><span>A steward answers</span></label>
        {#if replyMode === 'steward'}
          <select class="select" bind:value={stewardId} aria-label="Steward">
            <option value="">Choose an engineer from this room…</option>
            {#each engineers.filter((e) => engineerIds.includes(e.id)) as e (e.id)}<option value={e.id}>{e.name}</option>{/each}
          </select>
        {/if}
      </fieldset>
      <label class="check"><input type="checkbox" bind:checked={isPrivate} /><span>Private<br /><span class="meta">History here isn't carried into other rooms.</span></span></label>
    {/if}
    {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn" onclick={onclose}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="create-room" disabled={busy}>{busy ? 'Creating…' : kind === 'dm' ? 'Open conversation' : 'Create room'}</button>
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: 16px;
  }
  .fs {
    border: 0;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
  }
  .person {
    align-items: center;
  }
</style>
