<script lang="ts">
  // Room settings: name, purpose, privacy, reply mode, projects and members.
  // Adding a member first shows what history becomes visible to them.
  import { app } from '../../lib/state/app.svelte';
  import { api } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import { setRoomSnapshot } from '../../lib/state/data';
  import type { MembershipPreview, Room } from '../../lib/api/types.gen';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import Avatar from '../Avatar.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';

  interface Props {
    roomId: string;
    mode: PanelMode;
  }
  let { roomId, mode }: Props = $props();
  const room = $derived(app.data.rooms[roomId]);

  let name = $state('');
  let purpose = $state('');
  let isPrivate = $state(false);
  let replyMode = $state('quiet');
  let stewardId = $state('');
  let projectIds = $state<string[]>([]);
  let loadedVersion = -1;
  let saving = $state(false);
  let error = $state('');
  let saved = $state(false);

  $effect(() => {
    const r = room;
    if (r && r.version !== loadedVersion) {
      loadedVersion = r.version;
      name = r.name;
      purpose = r.purpose;
      isPrivate = r.private;
      replyMode = r.replyMode || 'quiet';
      stewardId = r.stewardId ?? '';
      projectIds = [...r.projectIds];
    }
  });

  const members = $derived((room?.members ?? []).filter((m) => m.kind === 'engineer').map((m) => app.data.engineers[m.id]).filter(Boolean));
  const others = $derived(Object.values(app.data.engineers).filter((e) => !e.archived && !members.some((m) => m.id === e.id)));
  const dirty = $derived(
    !!room &&
      (name !== room.name ||
        purpose !== room.purpose ||
        isPrivate !== room.private ||
        replyMode !== room.replyMode ||
        (replyMode === 'steward' && stewardId !== (room.stewardId ?? '')) ||
        projectIds.slice().sort().join() !== room.projectIds.slice().sort().join()),
  );

  function apply(r: Room) {
    const cur = app.data.rooms[r.id];
    setRoomSnapshot(app.data, { ...r, lastReadSeq: cur?.lastReadSeq ?? r.lastReadSeq, unreadCount: cur?.unreadCount ?? 0, mentionCount: cur?.mentionCount ?? 0 });
  }

  async function save() {
    if (!room) return;
    if (replyMode === 'steward' && !stewardId) {
      error = 'Choose which engineer answers unaddressed messages.';
      return;
    }
    saving = true;
    error = '';
    try {
      apply(
        await api.updateRoom(room.id, {
          version: room.version,
          name: name.trim(),
          purpose: purpose.trim(),
          private: isPrivate,
          replyMode,
          stewardId: replyMode === 'steward' ? stewardId : '',
          projectIds,
        }),
      );
      saved = true;
      setTimeout(() => (saved = false), 2500);
    } catch (err) {
      error = err instanceof ApiError && err.conflict ? 'Someone changed this room meanwhile. Your edits were not saved; the latest settings are shown.' : errorMessage(err);
      if (err instanceof ApiError && err.conflict) {
        loadedVersion = -1;
        void app.refreshRoom(roomId);
      }
    } finally {
      saving = false;
    }
  }

  // ---- members ----
  let addId = $state('');
  let preview = $state<MembershipPreview | null>(null);
  let previewing = $state(false);
  let memberError = $state('');
  let removing = $state<string | null>(null);

  async function previewAdd() {
    if (!addId) return;
    previewing = true;
    memberError = '';
    try {
      preview = await api.previewMember(roomId, addId);
    } catch (err) {
      memberError = errorMessage(err);
    } finally {
      previewing = false;
    }
  }

  async function confirmAdd() {
    if (!preview) return;
    try {
      apply(await api.addMember(roomId, preview.engineerId));
      app.announce(`${app.engineerName(preview.engineerId)} added to ${room?.name}.`);
      preview = null;
      addId = '';
    } catch (err) {
      memberError = errorMessage(err);
    }
  }

  async function remove(id: string) {
    apply(await api.removeMember(roomId, id));
    app.announce(`${app.engineerName(id)} removed from ${room?.name}.`);
  }

  let confirmArchive = $state(false);
  async function archive() {
    if (!room) return;
    await api.updateRoom(room.id, { version: room.version, archived: true });
    app.data.rooms[room.id] = { ...room, archived: true };
    app.navigate('/overview');
  }
</script>

<RightPanel title="Room settings" {mode} onclose={() => app.closePanel()}>
  {#snippet subtitle()}{room?.name ?? ''}{/snippet}
  {#if !room}
    <p class="pad meta">This room isn't available.</p>
  {:else}
    <div class="pad">
      <label class="check mute">
        <input type="checkbox" checked={app.isMuted(roomId)} onchange={() => app.toggleMute(roomId)} />
        <span>Mute notifications<br /><span class="meta">Only for you. Questions and permission requests for you still notify; the work carries on.</span></span>
      </label>
      <section aria-labelledby="rs-members">
        <h3 id="rs-members">Engineers in this room</h3>
        {#if members.length === 0}
          <p class="meta">Bring a couple of engineers into this room, then tell them what you're working on.</p>
        {/if}
        <ul class="members">
          {#each members as e (e.id)}
            <li>
              <Avatar actor={{ kind: 'engineer', id: e.id }} size={28} />
              <span class="m-text"><strong>{e.name}</strong> <span class="meta">{e.role}</span></span>
              {#if room.kind !== 'dm'}
                <button class="btn btn-sm btn-quiet" onclick={() => (removing = e.id)}>Remove</button>
              {/if}
            </li>
          {/each}
        </ul>
        {#if room.kind !== 'dm' && others.length}
          <div class="add">
            <label class="field">
              <span class="label">Add an engineer</span>
              <select class="select" bind:value={addId} onchange={() => (preview = null)}>
                <option value="">Choose…</option>
                {#each others as e (e.id)}<option value={e.id}>{e.name} — {e.role}</option>{/each}
              </select>
            </label>
            <button class="btn btn-sm" disabled={!addId || previewing} onclick={previewAdd}>{previewing ? 'Checking…' : 'Review access'}</button>
          </div>
          {#if preview}
            <div class="notice attention preview" role="status">
              <div>
                <p><strong>Before adding {app.engineerName(preview.engineerId)}</strong></p>
                <p>{preview.explanation}</p>
                <p class="meta">{preview.visibleMessageCount} {preview.visibleMessageCount === 1 ? 'message' : 'messages'} in this room become visible to them.</p>
                <div class="row">
                  <button class="btn btn-sm btn-primary" onclick={confirmAdd}>Add {app.engineerName(preview.engineerId)}</button>
                  <button class="btn btn-sm" onclick={() => (preview = null)}>Cancel</button>
                </div>
              </div>
            </div>
          {/if}
        {/if}
        {#if memberError}<p class="form-error" role="alert">{memberError}</p>{/if}
      </section>

      <hr class="hairline" />

      <form
        class="form"
        onsubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        {#if room.kind === 'room'}
          <label class="field"><span class="label">Name</span><input class="input" bind:value={name} required /></label>
          <label class="field"><span class="label">Purpose</span><input class="input" bind:value={purpose} /></label>
          <label class="check"><input type="checkbox" bind:checked={isPrivate} /><span>Private room<br /><span class="meta">History here isn't carried into other rooms, even by engineers who are in both.</span></span></label>
        {/if}
        <fieldset class="fs">
          <legend class="label">Who replies to unaddressed messages</legend>
          <label class="check"><input type="radio" name="reply" value="quiet" bind:group={replyMode} /><span>Nobody — only engineers you mention reply</span></label>
          <label class="check"><input type="radio" name="reply" value="steward" bind:group={replyMode} /><span>A steward engineer answers</span></label>
          {#if replyMode === 'steward'}
            <label class="field">
              <span class="vh">Steward</span>
              <select class="select" bind:value={stewardId}>
                <option value="">Choose an engineer…</option>
                {#each members as e (e.id)}<option value={e.id}>{e.name}</option>{/each}
              </select>
            </label>
          {/if}
        </fieldset>
        {#if room.kind === 'room'}
          <fieldset class="fs">
            <legend class="label">Projects</legend>
            {#if Object.keys(app.data.projects).length === 0}<p class="meta">No projects yet. <a href="/projects">Create one</a>.</p>{/if}
            {#each Object.values(app.data.projects) as p (p.id)}
              <label class="check">
                <input type="checkbox" checked={projectIds.includes(p.id)} onchange={() => (projectIds = projectIds.includes(p.id) ? projectIds.filter((x) => x !== p.id) : [...projectIds, p.id])} />
                <span>{p.name} <span class="meta">{p.description}</span></span>
              </label>
            {/each}
          </fieldset>
        {/if}
        {#if error}<p class="form-error" role="alert">{error}</p>{/if}
        <div class="row">
          <button class="btn btn-primary btn-sm" type="submit" disabled={!dirty || saving}>{saving ? 'Saving…' : 'Save changes'}</button>
          {#if saved}<span class="meta" role="status">Saved.</span>{/if}
        </div>
      </form>

      {#if room.kind === 'room'}
        <hr class="hairline" />
        <button class="btn btn-sm btn-danger archive" onclick={() => (confirmArchive = true)}>Archive room</button>
      {/if}
    </div>
  {/if}
</RightPanel>

{#if removing}
  <ConfirmDialog
    title="Remove {app.engineerName(removing)}?"
    body="{app.engineerName(removing)} will stop receiving messages from {room?.name}. Their work already in progress continues unless you stop it."
    confirmLabel="Remove"
    danger
    onconfirm={() => remove(removing!)}
    onclose={() => (removing = null)}
  />
{/if}
{#if confirmArchive}
  <ConfirmDialog
    title="Archive {room?.name}?"
    body="The room leaves your sidebar. Its history, work and decisions are kept and still searchable."
    confirmLabel="Archive"
    danger
    onconfirm={archive}
    onclose={() => (confirmArchive = false)}
  />
{/if}

<style>
  .mute {
    margin-bottom: 16px;
  }
  .pad {
    padding: 16px 18px 28px;
    display: grid;
    gap: 16px;
    align-content: start;
  }
  h3 {
    font-size: 14px;
    margin-bottom: 8px;
  }
  .members {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 6px;
  }
  .members li {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .m-text {
    flex: 1;
    min-width: 0;
    font-size: 14px;
  }
  .add {
    display: flex;
    align-items: flex-end;
    gap: 8px;
    margin-top: 10px;
    flex-wrap: wrap;
  }
  .add .field {
    flex: 1;
    min-width: 180px;
  }
  .preview {
    margin-top: 10px;
  }
  .preview > div {
    display: grid;
    gap: 6px;
  }
  .form {
    display: grid;
    gap: 14px;
  }
  .fs {
    border: 0;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 6px;
  }
  .row {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .archive {
    justify-self: start;
  }
</style>
