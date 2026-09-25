<script lang="ts">
  // A quick look at an engineer from anywhere: the same identity in every room.
  import { app } from '../../lib/state/app.svelte';
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
      <p class="meta">This engineer isn't available.</p>
    {:else}
      <div class="id">
        <Avatar actor={{ kind: 'engineer', id: e.id }} size={56} />
        <div>
          <p class="name">{e.name}</p>
          <p class="meta">{e.role} · AI engineer · @{e.handle}</p>
        </div>
      </div>
      {#if e.description}<p>{e.description}</p>{/if}
      {#if e.capabilityTags.length}
        <ul class="tags">{#each e.capabilityTags as t (t)}<li class="chip">{t}</li>{/each}</ul>
      {/if}
      <section>
        <h3>Standing instructions <span class="meta">· version {e.versionNo}</span></h3>
        <div class="instr"><MessageBody message={{ body: e.instructions || 'None yet.', mentions: [] }} /></div>
      </section>
      <section>
        <h3>Rooms</h3>
        {#if rooms.length === 0}<p class="meta">Not in any of your rooms.</p>{/if}
        <ul class="plain">{#each rooms as r (r.id)}<li><a href="/rooms/{r.id}">{r.kind === 'dm' ? 'Direct messages' : r.name}</a></li>{/each}</ul>
      </section>
      <section>
        <h3>Active work</h3>
        {#if work.length === 0}<p class="meta">Nothing in progress.</p>{/if}
        <ul class="plain">
          {#each work as j (j.id)}
            <li>
              <button class="work" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />
                <span class="truncate">{j.title}</span>{' '}<span class="meta">· {jobStateLabel(j)}</span>
              </button>
            </li>
          {/each}
        </ul>
      </section>
      <p class="meta">Provider preference: {providerLabel(e.provider.provider)}{e.provider.model ? ` · ${e.provider.model}` : ''}</p>
      <a class="btn btn-sm" href="/engineers/{e.id}">Open full profile</a>
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: 16px 18px 24px;
    display: grid;
    gap: 14px;
    align-content: start;
  }
  .id {
    display: flex;
    gap: 12px;
    align-items: center;
  }
  .name {
    font-size: 18px;
    font-weight: 680;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  h3 {
    font-size: 14px;
    margin-bottom: 6px;
  }
  .instr {
    padding: 8px 10px;
    border-radius: var(--r-control);
    background: var(--surface-subtle);
    font-size: 14px;
  }
  .plain {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
  }
  .work {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 0;
    border: 0;
    background: none;
    color: var(--ink);
    font: inherit;
    font-size: 14px;
    text-align: left;
    cursor: pointer;
  }
  .work:hover .truncate {
    text-decoration: underline;
  }
  .btn {
    justify-self: start;
  }
</style>
