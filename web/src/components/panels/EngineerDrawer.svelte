<script lang="ts">
  // A quick look at an engineer from anywhere: the same identity in every room.
  import { Button, Heading, Link, List, ListItem, Text, Token } from '@astryx-svelte/core';
  import { app } from '../../lib/state/app.svelte';
  import { workspaceUrl } from '../../lib/workspace';
  import { isLiveJob } from '../../lib/state/data';
  import { jobShape, jobStateLabel, jobTone, providerLabel } from '../../lib/util/labels';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import Avatar from '../Avatar.svelte';
  import StateIcon from '../StateIcon.svelte';
  import MessageBody from '../MessageBody.svelte';

  interface Props {
    engineerId: string;
    mode: PanelMode;
  }
  let { engineerId, mode }: Props = $props();
  const e = $derived(app.data.engineers[engineerId]);
  const rooms = $derived((e?.roomIds ?? []).map((id) => app.data.rooms[id]).filter(Boolean));
  const work = $derived(Object.values(app.data.jobs).filter((j) => j.ownerId === engineerId && j.kind !== 'reply' && isLiveJob(j)));
</script>

<RightPanel title={e?.name ?? 'Engineer'} {mode} onclose={() => app.closePanel()}>
  {#snippet subtitle()}{e?.role ?? ''}{/snippet}
  <div class="pad">
    {#if !e}
      <Text as="p" type="supporting">This engineer isn't available.</Text>
    {:else}
      <div class="id">
        <Avatar actor={{ kind: 'engineer', id: e.id }} size={60} />
        <div class="id-text">
          <Text as="p" type="large">{e.name}</Text>
          <Text as="p" type="supporting">{e.role} · AI engineer · @{e.handle}</Text>
        </div>
      </div>
      {#if e.description}<p>{e.description}</p>{/if}
      {#if e.capabilityTags.length}
        <ul class="tags">{#each e.capabilityTags as t (t)}<li><Token label={t} size="sm" /></li>{/each}</ul>
      {/if}
      <section>
        <Heading level={3}>Standing instructions <Text type="supporting">· version {e.versionNo}</Text></Heading>
        <div class="instr"><MessageBody message={{ body: e.instructions || 'None yet.', mentions: [] }} /></div>
      </section>
      <section>
        <Heading level={3}>Rooms</Heading>
        {#if rooms.length === 0}<Text as="p" type="supporting">Not in any of your rooms.</Text>{/if}
        <ul class="plain">{#each rooms as r (r.id)}<li><Link hasUnderline href={workspaceUrl(`/rooms/${r.id}`)}>{r.kind === 'dm' ? 'Direct messages' : r.name}</Link></li>{/each}</ul>
      </section>
      <section>
        <Heading level={3}>Active work</Heading>
        {#if work.length === 0}
          <Text as="p" type="supporting">Nothing in progress.</Text>
        {:else}
          <div class="work">
            <List density="compact">
              {#each work as j (j.id)}
                <ListItem label={j.title} description={jobStateLabel(j)} onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                  {#snippet startContent()}<StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />{/snippet}
                </ListItem>
              {/each}
            </List>
          </div>
        {/if}
      </section>
      <Text as="p" type="supporting">Provider preference: {providerLabel(e.provider.provider)}{e.provider.model ? ` · ${e.provider.model}` : ''}</Text>
      <div class="profile"><Button size="sm" label="Open full profile" href={workspaceUrl(`/engineers/${e.id}`)} /></div>
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: var(--spacing-4) var(--spacing-4) var(--spacing-6);
    display: grid;
    gap: var(--spacing-4);
    align-content: start;
  }
  .id {
    display: flex;
    gap: var(--spacing-3);
    align-items: center;
  }
  .id-text {
    display: grid;
    gap: var(--spacing-0-5);
    min-width: 0;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
  }
  section {
    display: grid;
    gap: var(--spacing-1-5);
  }
  /* Section titles in a drawer sit below its 17px title. */
  section :global(h3.astryx-heading) {
    font-size: var(--text-heading-4-size);
    line-height: var(--text-heading-4-leading);
  }
  .instr {
    padding: var(--spacing-2) var(--spacing-3);
    border-radius: var(--radius-element);
    background: var(--color-background-muted);
  }
  .plain {
    display: grid;
    gap: var(--spacing-1);
  }
  /* Rows keep their hover wash but line up with the text above them. */
  .work {
    margin-inline: calc(-1 * var(--spacing-2));
  }
  .profile {
    justify-self: start;
  }
</style>
