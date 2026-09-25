<script lang="ts">
  // An engineer's profile: role and instructions (versioned), capabilities,
  // provider preference with readiness, rooms, active work, and decisions.
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { Decision, EngineerVersion, Job, UpdateEngineerRequest } from '../lib/api/types.gen';
  import { isLiveJob } from '../lib/state/data';
  import { jobShape, jobStateLabel, jobTone } from '../lib/util/labels';
  import { atTime, relative } from '../lib/util/time';
  import Avatar from '../components/Avatar.svelte';
  import StateIcon from '../components/StateIcon.svelte';
  import MessageBody from '../components/MessageBody.svelte';
  import ProviderSelect from '../components/ProviderSelect.svelte';
  import ConfirmDialog from '../components/ConfirmDialog.svelte';

  interface Props {
    id: string;
  }
  let { id }: Props = $props();

  const e = $derived(app.data.engineers[id]);
  let versions = $state<EngineerVersion[]>([]);
  let jobs = $state<Job[]>([]);
  let decisions = $state<Decision[]>([]);
  let loadError = $state('');

  async function load() {
    try {
      const res = await api.engineer(id);
      app.data.engineers[id] = res.engineer;
      versions = (res.versions ?? []).sort((a, b) => b.versionNo - a.versionNo);
    } catch (err) {
      loadError = errorMessage(err);
    }
    api
      .jobs({ owner: id })
      .then((js) => {
        jobs = js ?? [];
        for (const j of jobs) {
          const c = app.data.jobs[j.id];
          if (!c || j.version >= c.version) app.data.jobs[j.id] = j;
        }
      })
      .catch(() => {});
    api
      .decisions()
      .then((ds) => (decisions = (ds ?? []).filter((d) => d.createdBy.kind === 'engineer' && d.createdBy.id === id)))
      .catch(() => {});
  }
  onMount(() => void load());

  const rooms = $derived((e?.roomIds ?? []).map((r) => app.data.rooms[r]).filter(Boolean));
  // Projects this engineer is permitted to work in, from the projects' grants.
  const ACTION_LABELS: Record<string, string> = { push: 'push', open_pr: 'open PRs', publish_review: 'publish reviews', merge: 'merge' };
  const permitted = $derived(
    Object.values(app.data.projects)
      .map((p) => ({ project: p, grant: (p.grants ?? []).find((g) => g.engineerId === id) }))
      .filter((x) => x.grant && x.grant.access !== 'none')
      .sort((a, b) => a.project.name.localeCompare(b.project.name)),
  );
  const liveJobs = $derived(
    Object.values(app.data.jobs).filter((j) => j.ownerId === id && j.kind !== 'reply' && j.kind !== 'review' && isLiveJob(j)),
  );
  const recent = $derived(jobs.map((j) => app.data.jobs[j.id] ?? j).filter((j) => !isLiveJob(j) && j.kind !== 'review').slice(0, 8));

  // ---- editing ----
  let editingProfile = $state(false);
  let name = $state('');
  let role = $state('');
  let description = $state('');
  let tags = $state('');
  let editingInstructions = $state(false);
  let instructions = $state('');
  let saving = $state(false);
  let error = $state('');
  let confirmArchive = $state(false);

  function startProfile() {
    if (!e) return;
    name = e.name;
    role = e.role;
    description = e.description;
    tags = e.capabilityTags.join(', ');
    editingProfile = true;
  }

  async function patch(req: Omit<UpdateEngineerRequest, 'version'>, done: () => void) {
    if (!e) return;
    saving = true;
    error = '';
    try {
      const next = await api.updateEngineer(id, { version: e.version, ...req });
      app.data.engineers[id] = next;
      done();
      const res = await api.engineer(id);
      versions = (res.versions ?? []).sort((a, b) => b.versionNo - a.versionNo);
    } catch (err) {
      error = err instanceof ApiError && err.conflict ? 'This profile changed since you opened it. Your edit was not saved — reload the latest and try again.' : errorMessage(err);
    } finally {
      saving = false;
    }
  }

  function saveProfile(ev: SubmitEvent) {
    ev.preventDefault();
    void patch(
      {
        name: name.trim(),
        role: role.trim(),
        description: description.trim(),
        capabilityTags: tags
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
      },
      () => (editingProfile = false),
    );
  }

  function saveInstructions(ev: SubmitEvent) {
    ev.preventDefault();
    void patch({ instructions: instructions.trim() }, () => (editingInstructions = false));
  }
</script>

<div class="screen">
  <div class="screen-inner">
    {#if !e}
      <h1 class="screen-title" data-screen-title tabindex="-1">{loadError ? 'Engineer not found' : 'Loading…'}</h1>
      {#if loadError}<p class="screen-sub">{loadError} <a href="/engineers">All engineers</a></p>{/if}
    {:else}
      <p class="crumb"><a href="/engineers">Engineers</a></p>
      <header class="head">
        <Avatar actor={{ kind: 'engineer', id: e.id }} size={64} />
        <div class="id">
          <h1 class="screen-title" data-screen-title tabindex="-1">{e.name}</h1>
          <p class="screen-sub">{e.role} · AI engineer · @{e.handle}{#if e.archived}{' · '}archived{/if}</p>
        </div>
        {#if !editingProfile}<button class="btn" onclick={startProfile}>Edit profile</button>{/if}
      </header>
      {#if error}<p class="form-error" role="alert">{error}</p>{/if}

      {#if editingProfile}
        <form class="panel-box form" onsubmit={saveProfile}>
          <div class="two">
            <label class="field"><span class="label">Name</span><input class="input" bind:value={name} /><span class="hint">Renaming keeps the same engineer and their message history.</span></label>
            <label class="field"><span class="label">Role</span><input class="input" bind:value={role} /></label>
          </div>
          <label class="field"><span class="label">What they're for</span><input class="input" bind:value={description} /></label>
          <label class="field"><span class="label">Capabilities</span><input class="input" bind:value={tags} /><span class="hint">Comma-separated.</span></label>
          <div class="row">
            <button class="btn btn-primary btn-sm" type="submit" disabled={saving}>Save</button>
            <button class="btn btn-sm" type="button" onclick={() => (editingProfile = false)}>Cancel</button>
          </div>
        </form>
      {:else}
        {#if e.description}<p class="desc">{e.description}</p>{/if}
        {#if e.capabilityTags.length}
          <ul class="tags" aria-label="Capabilities">{#each e.capabilityTags as t (t)}<li class="chip">{t}</li>{/each}</ul>
        {/if}
      {/if}

      <div class="cols">
        <div class="col">
          <section class="section" aria-labelledby="eng-instr">
            <div class="section-head">
              <h2 class="section-title" id="eng-instr">Standing instructions <span class="meta">· version {e.versionNo}</span></h2>
              {#if !editingInstructions}
                <button
                  class="btn btn-sm"
                  onclick={() => {
                    instructions = e.instructions;
                    editingInstructions = true;
                  }}>Edit</button
                >
              {/if}
            </div>
            {#if editingInstructions}
              <form class="form" onsubmit={saveInstructions}>
                <label class="field">
                  <span class="vh">Standing instructions</span>
                  <textarea class="textarea" rows="7" bind:value={instructions}></textarea>
                </label>
                <p class="notice">Saving creates version {e.versionNo + 1}. Work already running keeps the instructions it started with; new work uses the new version.</p>
                <div class="row">
                  <button class="btn btn-primary btn-sm" type="submit" disabled={saving || instructions.trim() === e.instructions}>Save as version {e.versionNo + 1}</button>
                  <button class="btn btn-sm" type="button" onclick={() => (editingInstructions = false)}>Cancel</button>
                </div>
              </form>
            {:else}
              <div class="instr"><MessageBody message={{ body: e.instructions || 'No standing instructions.', mentions: [] }} /></div>
            {/if}
            {#if versions.length > 1}
              <details class="history">
                <summary>Version history ({versions.length})</summary>
                <ol>
                  {#each versions as v (v.id)}
                    <li>
                      <p><strong>Version {v.versionNo}</strong> <span class="meta">· {atTime(v.createdAt)}{v.versionNo === e.versionNo ? ' · current' : ''}</span></p>
                      <p class="meta">{v.role}{v.name !== e.name ? ` · named ${v.name}` : ''}</p>
                      <div class="instr small"><MessageBody message={{ body: v.instructions || '—', mentions: [] }} /></div>
                    </li>
                  {/each}
                </ol>
              </details>
            {/if}
          </section>

          <section class="section" aria-labelledby="eng-work">
            <h2 class="section-title" id="eng-work">Active and queued work</h2>
            {#if liveJobs.length === 0}
              <p class="meta">Nothing in progress.</p>
            {:else}
              <ul class="list">
                {#each liveJobs as j (j.id)}
                  <li>
                    <button class="work" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                      <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />
                      <span class="w-title">{j.title}</span>
                      <span class="meta">{jobStateLabel(j)} · {app.data.rooms[j.source.roomId]?.name ?? ''}</span>
                    </button>
                  </li>
                {/each}
              </ul>
            {/if}
            {#if recent.length}
              <h3 class="sub">Recently</h3>
              <ul class="list">
                {#each recent as j (j.id)}
                  <li>
                    <button class="work" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                      <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} />
                      <span class="w-title">{j.title}</span>
                      <span class="meta">{jobStateLabel(j)} · {relative(j.completedAt ?? j.updatedAt, app.now)}</span>
                    </button>
                  </li>
                {/each}
              </ul>
            {/if}
          </section>

          <section class="section" aria-labelledby="eng-dec">
            <h2 class="section-title" id="eng-dec">Decisions they recorded</h2>
            {#if decisions.length === 0}
              <p class="meta">None yet.</p>
            {:else}
              <ul class="list">
                {#each decisions as d (d.id)}
                  <li>
                    <button class="link-btn" onclick={() => app.openPanel({ kind: 'decision', id: d.id })}>{d.title}</button>
                    <span class="meta">· {d.status} · {d.sources.length} {d.sources.length === 1 ? 'source' : 'sources'}</span>
                  </li>
                {/each}
              </ul>
            {/if}
          </section>
        </div>

        <div class="col side">
          <section class="section" aria-labelledby="eng-rooms">
            <h2 class="section-title" id="eng-rooms">Rooms</h2>
            {#if rooms.length === 0}<p class="meta">Not in any room yet. Add them from a room's settings.</p>{/if}
            <ul class="list">{#each rooms as r (r.id)}<li><a href="/rooms/{r.id}">{r.kind === 'dm' ? 'Direct messages' : r.name}</a>{#if r.private}{' '}<span class="meta">· private</span>{/if}</li>{/each}</ul>
          </section>
          <section class="section" aria-labelledby="eng-proj">
            <h2 class="section-title" id="eng-proj">Projects they can work on</h2>
            {#if permitted.length === 0}<p class="meta">No project access yet. Grant it from a project's page.</p>{/if}
            <ul class="list">
              {#each permitted as x (x.project.id)}
                <li>
                  <a href="/projects/{x.project.id}">{x.project.name}</a>{' '}<span class="meta"
                    >· {x.grant?.access === 'write' ? 'can change code' : 'read only'}{x.grant?.actions.length
                      ? ` · can ${x.grant.actions.map((a) => ACTION_LABELS[a] ?? a).join(', ')}`
                      : ''}</span
                  >
                </li>
              {/each}
            </ul>
          </section>
          <section class="section" aria-labelledby="eng-prov">
            <h2 class="section-title" id="eng-prov">Provider preference</h2>
            <label class="vh" for="eng-provider">Provider</label>
            <ProviderSelect id="eng-provider" value={e.provider.provider} onchange={(v) => patch({ provider: { ...e.provider, provider: v } }, () => {})} />
            <p class="meta note">Which model runs a job is shown in the job's run details, not in conversation.</p>
          </section>
          <section class="section">
            {#if e.archived}
              <button class="btn btn-sm" onclick={() => patch({ archived: false }, () => {})}>Restore engineer</button>
            {:else}
              <button class="btn btn-sm btn-danger" onclick={() => (confirmArchive = true)}>Archive engineer</button>
            {/if}
          </section>
        </div>
      </div>
    {/if}
  </div>
</div>

{#if confirmArchive && e}
  <ConfirmDialog
    title="Archive {e.name}?"
    body="{e.name} stops receiving new work and can't be mentioned. Their history, decisions and past work stay attributed to them."
    confirmLabel="Archive"
    danger
    onconfirm={() => patch({ archived: true }, () => {})}
    onclose={() => (confirmArchive = false)}
  />
{/if}

<style>
  .crumb {
    font-size: 13px;
    margin-bottom: 10px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 16px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }
  .id {
    flex: 1;
    min-width: 200px;
  }
  .desc {
    max-width: 70ch;
    margin-bottom: 10px;
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .form {
    display: grid;
    gap: 12px;
    padding: 16px;
    margin-bottom: 12px;
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .row {
    display: flex;
    gap: 8px;
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: 32px;
  }
  .instr {
    padding: 12px 14px;
    border-radius: var(--r-artifact);
    background: var(--surface-subtle);
    font-size: 14.5px;
  }
  .instr.small {
    padding: 8px 10px;
    font-size: 13.5px;
  }
  .history {
    margin-top: 10px;
  }
  .history summary {
    cursor: pointer;
    font-size: 14px;
    font-weight: 560;
    min-height: 32px;
    display: list-item;
  }
  .history ol {
    list-style: none;
    padding: 0;
    margin: 8px 0 0;
    display: grid;
    gap: 12px;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 4px;
    font-size: 14px;
  }
  .work {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    column-gap: 8px;
    align-items: center;
    width: 100%;
    padding: 6px 8px;
    border: 0;
    border-radius: 8px;
    background: none;
    color: var(--ink);
    text-align: left;
    cursor: pointer;
    font: inherit;
  }
  .work:hover {
    background: var(--hover);
  }
  .work .meta {
    grid-column: 2;
  }
  .w-title {
    font-weight: 600;
  }
  .sub {
    font-size: 13px;
    color: var(--ink-secondary);
    margin: 14px 0 4px;
  }
  .note {
    margin-top: 8px;
  }
  @media (max-width: 1000px) {
    .cols {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
  @media (max-width: 560px) {
    .two {
      grid-template-columns: 1fr;
    }
  }
</style>
