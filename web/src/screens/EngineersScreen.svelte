<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { isLiveJob } from '../lib/state/data';
  import { providerLabel } from '../lib/util/labels';
  import Avatar from '../components/Avatar.svelte';
  import Icon from '../components/Icon.svelte';
  import CreateEngineerDialog from '../components/CreateEngineerDialog.svelte';

  let creating = $state(false);
  const engineers = $derived(Object.values(app.data.engineers).sort((a, b) => Number(a.archived) - Number(b.archived) || a.name.localeCompare(b.name)));
  function activeCount(id: string) {
    return Object.values(app.data.jobs).filter((j) => j.ownerId === id && j.kind !== 'reply' && isLiveJob(j)).length;
  }
  function ready(provider: string) {
    return (app.data.providers.find((p) => p.provider === provider)?.readyNodes.length ?? 0) > 0;
  }
</script>

<div class="screen">
  <div class="screen-inner">
    <header class="screen-head">
      <div>
        <h1 class="screen-title" data-screen-title tabindex="-1">Engineers</h1>
        <p class="screen-sub">The same engineers in every room. Each keeps one identity, role and history.</p>
      </div>
      <button class="btn btn-primary" onclick={() => (creating = true)}><Icon name="plus" size={16} />New engineer</button>
    </header>

    {#if engineers.length === 0}
      <div class="empty">
        <p><strong>No engineers yet.</strong></p>
        <p>Create an engineer with a name, a role and standing instructions, then bring them into a room.</p>
        <button class="btn btn-primary" onclick={() => (creating = true)}>New engineer</button>
      </div>
    {:else}
      <ul class="grid">
        {#each engineers as e (e.id)}
          {@const n = activeCount(e.id)}
          <li class="card-item" class:archived={e.archived}>
            <a class="card-link" href="/engineers/{e.id}">
              <div class="top">
                <Avatar actor={{ kind: 'engineer', id: e.id }} size={44} />
                <div class="id">
                  <p class="name">{e.name}{#if e.archived}<span class="meta"> · archived</span>{/if}</p>
                  <p class="role">{e.role}</p>
                </div>
              </div>
              {#if e.description}<p class="desc">{e.description}</p>{/if}
              {#if e.capabilityTags.length}
                <ul class="tags">{#each e.capabilityTags.slice(0, 5) as t (t)}<li class="chip">{t}</li>{/each}</ul>
              {/if}
              <p class="facts meta">
                {e.roomIds.length} {e.roomIds.length === 1 ? 'room' : 'rooms'} · {n ? `${n} active` : 'no active work'} ·
                {providerLabel(e.provider.provider)}{ready(e.provider.provider) ? '' : ' (not ready)'}
              </p>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>

{#if creating}<CreateEngineerDialog onclose={() => (creating = false)} />{/if}

<style>
  .grid {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 12px;
  }
  .card-link {
    display: grid;
    gap: 10px;
    height: 100%;
    padding: 16px;
    border: 1px solid var(--line);
    border-radius: 14px;
    color: var(--ink);
    text-decoration: none;
    transition: border-color var(--t-fast) var(--ease);
  }
  .card-link:hover {
    border-color: var(--control-edge);
  }
  .archived .card-link {
    background: var(--surface-subtle);
  }
  .top {
    display: flex;
    gap: 12px;
    align-items: center;
  }
  .name {
    font-weight: 680;
    font-size: 16px;
  }
  .role {
    color: var(--ink-secondary);
    font-size: 14px;
  }
  .desc {
    font-size: 14px;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    list-style: none;
    margin: 0;
    padding: 0;
  }
</style>
