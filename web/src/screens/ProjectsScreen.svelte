<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import { isLiveJob } from '../lib/state/data';
  import Icon from '../components/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';

  const projects = $derived(Object.values(app.data.projects).sort((a, b) => a.name.localeCompare(b.name)));
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

<div class="screen">
  <div class="screen-inner">
    <header class="screen-head">
      <div>
        <h1 class="screen-title" data-screen-title tabindex="-1">Projects</h1>
        <p class="screen-sub">Repositories, access, review policy and the checks work must pass.</p>
      </div>
      <button class="btn btn-primary" onclick={() => (creating = true)}><Icon name="plus" size={16} />New project</button>
    </header>

    {#if projects.length === 0}
      <div class="empty">
        <p><strong>No projects yet.</strong></p>
        <p>You can talk in rooms now. Connect a project when you want the team to inspect or change code.</p>
        <button class="btn btn-primary" onclick={() => (creating = true)}>New project</button>
      </div>
    {:else}
      <ul class="list">
        {#each projects as p (p.id)}
          {@const n = openCount(p.id)}
          <li>
            <a class="row" href="/projects/{p.id}">
              <span class="ic"><Icon name="folder" /></span>
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
              <Icon name="chevronRight" />
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

{#if creating}
  <Dialog title="New project" onclose={() => (creating = false)} width={560}>
    <form id="new-project" class="form" onsubmit={create}>
      <label class="field"><span class="label">Name</span><input class="input" bind:value={name} placeholder="e.g. Atlas" /></label>
      <label class="field"><span class="label">Description</span><input class="input" bind:value={description} /></label>
      <label class="field"
        ><span class="label">Instructions for engineers</span><textarea class="textarea" rows="3" bind:value={instructions} placeholder="Contracts to preserve, commands to run, where docs live…"></textarea></label
      >
      <label class="check"><input type="checkbox" bind:checked={peer} /><span>Require a colleague's review before work is complete</span></label>
      {#if app.rooms.filter((r) => r.kind === 'room').length}
        <fieldset class="fs">
          <legend class="label">Link to rooms</legend>
          {#each app.rooms.filter((r) => r.kind === 'room') as r (r.id)}
            <label class="check"
              ><input type="checkbox" checked={roomIds.includes(r.id)} onchange={() => (roomIds = roomIds.includes(r.id) ? roomIds.filter((x) => x !== r.id) : [...roomIds, r.id])} />{r.name}</label
            >
          {/each}
        </fieldset>
      {/if}
      <p class="meta">Add repositories and access after creating it.</p>
      {#if error}<p class="form-error" role="alert">{error}</p>{/if}
    </form>
    {#snippet footer()}
      <button class="btn" onclick={() => (creating = false)}>Cancel</button>
      <button class="btn btn-primary" type="submit" form="new-project" disabled={busy}>{busy ? 'Creating…' : 'Create project'}</button>
    {/snippet}
  </Dialog>
{/if}

<style>
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    border: 1px solid var(--line);
    border-radius: 14px;
  }
  .list li + li {
    border-top: 1px solid var(--line-soft);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
    color: var(--ink);
    text-decoration: none;
  }
  .row:hover {
    background: var(--hover);
  }
  .list li:first-child .row {
    border-radius: 14px 14px 0 0;
  }
  .list li:last-child .row {
    border-radius: 0 0 14px 14px;
  }
  .ic {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: var(--surface-subtle);
    color: var(--ink-secondary);
    flex: none;
  }
  .main {
    flex: 1;
    min-width: 0;
    display: grid;
    gap: 2px;
  }
  .name {
    font-weight: 650;
    font-size: 16px;
  }
  .desc {
    font-size: 14px;
  }
  .form {
    display: grid;
    gap: 14px;
  }
  .fs {
    border: 0;
    padding: 0;
    margin: 0;
    display: grid;
    gap: 4px;
  }
</style>
