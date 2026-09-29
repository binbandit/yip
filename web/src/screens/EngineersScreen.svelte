<script lang="ts">
  import { Button, EmptyState, Icon, Text, Token } from '@astryx-svelte/core';
  import { Plus } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import { isLiveJob } from '../lib/state/data';
  import { providerLabel } from '../lib/util/labels';
  import Avatar from '../components/Avatar.svelte';
  import Screen from '../components/Screen.svelte';
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

<Screen title="Engineers" subtitle="The same engineers in every room. Each keeps one identity, role and history.">
  {#snippet actions()}
    <Button label="New engineer" variant="primary" onclick={() => (creating = true)}>
      {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
    </Button>
  {/snippet}

  {#if engineers.length === 0}
    <EmptyState title="No engineers yet." headingLevel={2} description="Create an engineer with a name, a role and standing instructions, then bring them into a room.">
      {#snippet actions()}<Button label="New engineer" variant="primary" onclick={() => (creating = true)} />{/snippet}
    </EmptyState>
  {:else}
    <ul class="grid">
      {#each engineers as e (e.id)}
        {@const n = activeCount(e.id)}
        <li class:archived={e.archived}>
          <a class="card-link" href={workspaceUrl(`/engineers/${e.id}`)}>
            <div class="top">
              <Avatar actor={{ kind: 'engineer', id: e.id }} size={48} />
              <div class="id">
                <p class="name">{e.name}{#if e.archived}{' '}<Text type="supporting">· archived</Text>{/if}</p>
                <Text as="p" color="secondary">{e.role}</Text>
              </div>
            </div>
            {#if e.description}<Text as="p">{e.description}</Text>{/if}
            {#if e.capabilityTags.length}
              <ul class="tags">{#each e.capabilityTags.slice(0, 5) as t (t)}<li><Token label={t} size="sm" /></li>{/each}</ul>
            {/if}
            <Text as="p" type="supporting">
              {e.roomIds.length} {e.roomIds.length === 1 ? 'room' : 'rooms'} · {n ? `${n} active` : 'no active work'} ·
              {providerLabel(e.provider.provider)}{ready(e.provider.provider) ? '' : ' (not ready)'}
            </Text>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</Screen>

{#if creating}<CreateEngineerDialog onclose={() => (creating = false)} />{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: var(--spacing-3);
  }
  .card-link {
    display: grid;
    align-content: start;
    gap: var(--spacing-3);
    height: 100%;
    padding: var(--spacing-4);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    background: var(--color-background-card);
    color: var(--color-text-primary);
    text-decoration: none;
    overflow-wrap: anywhere;
    transition: border-color var(--duration-fast) var(--ease-standard);
  }
  .card-link:hover {
    border-color: var(--color-border-emphasized);
  }
  .card-link:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  .archived .card-link {
    background: var(--color-background-muted);
  }
  .top {
    display: flex;
    gap: var(--spacing-3);
    align-items: center;
  }
  .id {
    display: grid;
    gap: var(--spacing-0-5);
    min-width: 0;
  }
  .name {
    font-size: var(--font-size-lg);
    font-weight: var(--font-weight-semibold);
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1);
  }
</style>
