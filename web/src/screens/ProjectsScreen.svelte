<script lang="ts">
  import { Button, CheckboxInput, CheckboxList, CheckboxListItem, EmptyState, Icon, Text, TextArea, TextInput } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import { ChevronRight, Folder, Plus } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import { isLiveJob } from '../lib/state/data';
  import Dialog from '../components/Dialog.svelte';
  import Screen from '../components/Screen.svelte';

  const projects = $derived(Object.values(app.data.projects).sort((a, b) => a.name.localeCompare(b.name)));
  const linkableRooms = $derived(app.rooms.filter((r) => r.kind === 'room'));
  let creating = $state(false);
  let name = $state('');
  let description = $state('');
  let instructions = $state('');
  let peer = $state(true);
  let roomIds = $state<string[]>([]);
  let busy = $state(false);
  let error = $state('');

  function openCount(pid: string) {
    return Object.values(app.data.jobs).filter((j) => j.projectId === pid && j.kind !== 'reply' && j.kind !== 'review' && isLiveJob(j)).length;
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!name.trim()) {
      error = 'Give the project a name.';
      return;
    }
    busy = true;
    error = '';
    try {
      const p = await api.createProject({
        name: name.trim(),
        description: description.trim(),
        instructions: instructions.trim(),
        policy: { requirePeerReview: peer, requireHumanReview: false, autoPublish: false, checks: [], executionProfile: 'native' },
        roomIds,
      });
      app.data.projects[p.id] = p;
      creating = false;
      app.go({ name: 'project', id: p.id });
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

<Screen title="Projects" subtitle="Repositories, access, review policy and the checks work must pass.">
  {#snippet actions()}
    <Button label="New project" variant="primary" onclick={() => (creating = true)}>
      {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
    </Button>
  {/snippet}

  {#if projects.length === 0}
    <EmptyState title="No projects yet." headingLevel={2} description="You can talk in rooms now. Connect a project when you want the team to inspect or change code.">
      {#snippet actions()}<Button label="New project" variant="primary" onclick={() => (creating = true)} />{/snippet}
    </EmptyState>
  {:else}
    <ul class="list">
      {#each projects as p (p.id)}
        {@const n = openCount(p.id)}
        <li>
          <a class="row" href="/projects/{p.id}">
            <span class="ic"><Icon icon={Folder} size="md" /></span>
            <span class="main">
              <span class="name">{p.name}</span>
              {#if p.description}<span class="desc">{p.description}</span>{/if}
              <span class="meta">
                {p.repos.length} {p.repos.length === 1 ? 'repository' : 'repositories'} ·
                {p.roomIds.length} {p.roomIds.length === 1 ? 'room' : 'rooms'} ·
                {n ? `${n} open` : 'no open work'} ·
                {p.policy.requireHumanReview ? 'your review required' : p.policy.requirePeerReview ? 'peer review required' : 'no review required'}
              </span>
            </span>
            <Icon icon={ChevronRight} size="sm" color="secondary" />
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</Screen>

{#if creating}
  <Dialog title="New project" onclose={() => (creating = false)} width={560} purpose="form">
    <form id="new-project" class="form" onsubmit={create}>
      <TextInput label="Name" bind:value={name} placeholder="e.g. Atlas" status={error && !name.trim() ? { type: 'error' } : undefined} />
      <TextInput label="Description" bind:value={description} />
      <TextArea label="Instructions for engineers" rows={3} bind:value={instructions} placeholder="Contracts to preserve, commands to run, where docs live…" />
      <CheckboxInput label="Require a colleague's review before work is complete" value={peer} onChange={(on) => (peer = on)} />
      {#if linkableRooms.length}
        <CheckboxList label="Link to rooms" density="compact" value={roomIds} onChange={(ids) => (roomIds = ids)}>
          {#each linkableRooms as r (r.id)}<CheckboxListItem label={r.name} value={r.id} />{/each}
        </CheckboxList>
      {/if}
      <Text as="p" type="supporting">Add repositories and access after creating it.</Text>
      {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
    </form>
    {#snippet footer()}
      <Button label="Cancel" onclick={() => (creating = false)} />
      <Button label="Create project" variant="primary" type="submit" form="new-project" isLoading={busy} />
    {/snippet}
  </Dialog>
{/if}

<style>
  .list {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    overflow: hidden;
  }
  .list li + li {
    border-top: 1px solid var(--color-border);
  }
  .row {
    display: flex;
    align-items: center;
    gap: var(--spacing-3);
    padding: var(--spacing-3) var(--spacing-4);
    color: var(--color-text-primary);
    text-decoration: none;
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .row:hover {
    background: var(--color-overlay-hover);
  }
  .row:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: -2px;
    border-radius: var(--radius-container);
  }
  .ic {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border-radius: var(--radius-element);
    background: var(--color-background-muted);
    color: var(--color-icon-secondary);
    flex: none;
  }
  .main {
    flex: 1;
    min-width: 0;
    display: grid;
    gap: var(--spacing-0-5);
  }
  .name {
    font-size: var(--font-size-lg);
    font-weight: var(--font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .desc {
    overflow-wrap: anywhere;
  }
  .meta {
    font-size: var(--text-supporting-size);
    line-height: var(--text-supporting-leading);
    color: var(--color-text-secondary);
  }
  .form {
    display: grid;
    gap: var(--spacing-4);
  }
</style>
