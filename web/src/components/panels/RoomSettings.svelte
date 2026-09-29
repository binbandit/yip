<script lang="ts">
  // Room settings: name, purpose, privacy, reply mode, projects and members.
  // Adding a member first shows what history becomes visible to them.
  import { Button, CheckboxInput, CheckboxList, CheckboxListItem, Divider, Heading, Link, RadioList, RadioListItem, Selector, Switch, Text, TextInput } from '@astryx-svelte/core';
  import { app } from '../../lib/state/app.svelte';
  import { workspaceUrl } from '../../lib/workspace';
  import { api } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import { setRoomSnapshot } from '../../lib/state/data';
  import type { MembershipPreview, Room } from '../../lib/api/types.gen';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import Avatar from '../Avatar.svelte';
  import Notice from '../Notice.svelte';
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

  // The room name is still checked by the browser before saving.
  const requiredHint = { required: true };

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
    <div class="pad"><Text as="p" type="supporting">This room isn't available.</Text></div>
  {:else}
    <div class="pad">
      <div class="mute">
        <Switch
          label="Mute notifications"
          description="Only for you. Questions and permission requests for you still notify; the work carries on."
          value={app.isMuted(roomId)}
          onChange={() => app.toggleMute(roomId)}
        />
      </div>
      <section class="group" aria-labelledby="rs-members">
        <Heading level={3} id="rs-members">Engineers in this room</Heading>
        {#if members.length === 0}
          <Text as="p" type="supporting">Bring a couple of engineers into this room, then tell them what you're working on.</Text>
        {/if}
        <ul class="members">
          {#each members as e (e.id)}
            <li>
              <Avatar actor={{ kind: 'engineer', id: e.id }} size={32} />
              <span class="m-text"><strong>{e.name}</strong> <Text type="supporting">{e.role}</Text></span>
              {#if room.kind !== 'dm'}
                <Button size="sm" variant="ghost" label="Remove" onclick={() => (removing = e.id)} />
              {/if}
            </li>
          {/each}
        </ul>
        {#if room.kind !== 'dm' && others.length}
          <div class="add">
            <div class="add-field">
              <Selector
                label="Add an engineer"
                placeholder="Choose…"
                width="100%"
                value={addId}
                options={others.map((e) => ({ value: e.id, label: `${e.name} — ${e.role}` }))}
                onChange={(v: string) => {
                  addId = v;
                  preview = null;
                }}
              />
            </div>
            <Button label={previewing ? 'Checking…' : 'Review access'} isDisabled={!addId || previewing} onclick={previewAdd} />
          </div>
          {#if preview}
            <Notice tone="warning" role="status" title="Before adding {app.engineerName(preview.engineerId)}" description={preview.explanation}>
              <div class="preview">
                <Text as="p" type="supporting"
                  >{preview.visibleMessageCount} {preview.visibleMessageCount === 1 ? 'message' : 'messages'} in this room become visible to them.</Text
                >
                <div class="row">
                  <Button size="sm" variant="primary" label="Add {app.engineerName(preview.engineerId)}" onclick={confirmAdd} />
                  <Button size="sm" label="Cancel" onclick={() => (preview = null)} />
                </div>
              </div>
            </Notice>
          {/if}
        {/if}
        {#if memberError}<Notice tone="danger" role="alert">{memberError}</Notice>{/if}
      </section>

      <Divider />

      <form
        class="form"
        onsubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        {#if room.kind === 'room'}
          <TextInput label="Name" width="100%" {...requiredHint} bind:value={name} />
          <TextInput label="Purpose" width="100%" bind:value={purpose} />
          <CheckboxInput
            label="Private room"
            description="History here isn't carried into other rooms, even by engineers who are in both."
            value={isPrivate}
            onChange={(v) => (isPrivate = v)}
          />
        {/if}
        <div class="group">
          <RadioList label="Who replies to unaddressed messages" value={replyMode} onChange={(v) => (replyMode = v)}>
            <RadioListItem value="quiet" label="Nobody — only engineers you mention reply" />
            <RadioListItem value="steward" label="A steward engineer answers" />
          </RadioList>
          {#if replyMode === 'steward'}
            <div class="steward">
              <Selector
                label="Steward"
                isLabelHidden
                placeholder="Choose an engineer…"
                width="100%"
                value={stewardId}
                options={members.map((e) => ({ value: e.id, label: e.name }))}
                onChange={(v: string) => (stewardId = v)}
              />
            </div>
          {/if}
        </div>
        {#if room.kind === 'room'}
          {#if Object.keys(app.data.projects).length === 0}
            <fieldset class="group">
              <legend class="legend">Projects</legend>
              <Text as="p" type="supporting">No projects yet. <Link href={workspaceUrl('/projects')} type="inherit" hasUnderline>Create one</Link>.</Text>
            </fieldset>
          {:else}
            <CheckboxList label="Projects" value={projectIds} onChange={(v) => (projectIds = v)}>
              {#each Object.values(app.data.projects) as p (p.id)}
                <CheckboxListItem value={p.id} label={p.name} description={p.description || undefined} />
              {/each}
            </CheckboxList>
          {/if}
        {/if}
        {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
        <div class="row">
          <Button size="sm" variant="primary" type="submit" label={saving ? 'Saving…' : 'Save changes'} isDisabled={!dirty || saving} />
          {#if saved}<Text type="supporting" role="status">Saved.</Text>{/if}
        </div>
      </form>

      {#if room.kind === 'room'}
        <Divider />
        <div class="archive"><Button size="sm" variant="destructive" label="Archive room" onclick={() => (confirmArchive = true)} /></div>
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
  .pad {
    padding: var(--spacing-4) var(--spacing-4) var(--spacing-7);
    display: grid;
    gap: var(--spacing-4);
    align-content: start;
  }
  .mute {
    margin-bottom: var(--spacing-1);
  }
  .group {
    display: grid;
    gap: var(--spacing-2);
    min-width: 0;
  }
  /* Section titles in a drawer sit below its 17px title. */
  .group :global(h3.astryx-heading) {
    font-size: var(--text-heading-4-size);
    line-height: var(--text-heading-4-leading);
  }
  .members {
    display: grid;
    gap: var(--spacing-1-5);
  }
  .members li {
    display: flex;
    align-items: center;
    gap: var(--spacing-3);
  }
  .m-text {
    flex: 1;
    min-width: 0;
  }
  .add {
    display: flex;
    align-items: flex-end;
    gap: var(--spacing-2);
    margin-top: var(--spacing-2);
    flex-wrap: wrap;
  }
  .add-field {
    flex: 1;
    min-width: 180px;
  }
  .preview {
    display: grid;
    gap: var(--spacing-2);
  }
  .form {
    display: grid;
    gap: var(--spacing-4);
  }
  .steward {
    padding-left: var(--spacing-6);
  }
  .legend {
    margin-bottom: var(--spacing-1);
    font-weight: var(--font-weight-medium);
  }
  .row {
    display: flex;
    gap: var(--spacing-2);
    align-items: center;
  }
  .archive {
    justify-self: start;
  }
</style>
