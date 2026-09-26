<script lang="ts">
  // A project: repositories, who may read or change them (and which exact
  // actions they may take), review policy and checks, linked rooms, open work
  // and decisions.
  import { onMount } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { Decision, Job, Project, Repo } from '../lib/api/types.gen';
  import { isLiveJob } from '../lib/state/data';
  import { GRANT_ACTIONS, jobShape, jobStateLabel, jobTone } from '../lib/util/labels';
  import Avatar from '../components/Avatar.svelte';
  import StateIcon from '../components/StateIcon.svelte';
  import MessageBody from '../components/MessageBody.svelte';
  import Icon from '../components/Icon.svelte';

  interface Props {
    id: string;
  }
  let { id }: Props = $props();
  const p = $derived(app.data.projects[id]);
  let jobs = $state<Job[]>([]);
  let decisions = $state<Decision[]>([]);
  let loadError = $state('');

  onMount(() => {
    api
      .project(id)
      .then((proj) => (app.data.projects[id] = proj))
      .catch((e) => (loadError = errorMessage(e)));
    api
      .jobs({ project: id })
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
      .then((ds) => (decisions = (ds ?? []).filter((d) => d.scope.kind === 'project' && d.scope.id === id)))
      .catch(() => {});
  });

  function setProject(next: Project) {
    app.data.projects[next.id] = next;
  }

  const openWork = $derived(
    Object.values(app.data.jobs).filter((j) => j.projectId === id && j.kind !== 'reply' && j.kind !== 'review' && isLiveJob(j)),
  );
  const rooms = $derived(app.rooms.filter((r) => r.projectIds.includes(id)));
  const engineers = $derived(Object.values(app.data.engineers).filter((e) => !e.archived));

  // ---- about ----
  let editingAbout = $state(false);
  let name = $state('');
  let description = $state('');
  let instructions = $state('');
  let aboutError = $state('');
  async function saveAbout(e: SubmitEvent) {
    e.preventDefault();
    if (!p) return;
    aboutError = '';
    try {
      setProject(await api.updateProject(id, { version: p.version, name: name.trim(), description: description.trim(), instructions: instructions.trim() }));
      editingAbout = false;
    } catch (err) {
      aboutError = err instanceof ApiError && err.conflict ? 'This project changed meanwhile; reload and try again.' : errorMessage(err);
    }
  }

  // ---- repositories ----
  let repoForm = $state<null | { id: string | 'new'; name: string; remoteUrl: string; defaultBranch: string; forge: string; forgeRepo: string }>(null);
  let repoError = $state('');
  let repoBusy = $state(false);
  function editRepo(r?: Repo) {
    repoError = '';
    repoForm = r
      ? { id: r.id, name: r.name, remoteUrl: r.remoteUrl, defaultBranch: r.defaultBranch, forge: r.forge || 'none', forgeRepo: r.forgeRepo ?? '' }
      : { id: 'new', name: '', remoteUrl: '', defaultBranch: 'main', forge: 'github', forgeRepo: '' };
  }
  async function saveRepo(e: SubmitEvent) {
    e.preventDefault();
    if (!repoForm) return;
    if (!repoForm.name.trim() || !repoForm.remoteUrl.trim()) {
      repoError = 'A repository needs a name and a remote URL.';
      return;
    }
    if (repoForm.forge === 'github' && !/^[\w.-]+\/[\w.-]+$/.test(repoForm.forgeRepo.trim())) {
      repoError = 'Use owner/name for the GitHub repository, e.g. acme/atlas.';
      return;
    }
    repoBusy = true;
    repoError = '';
    try {
      setProject(
        await api.putRepo(id, repoForm.id, {
          name: repoForm.name.trim(),
          remoteUrl: repoForm.remoteUrl.trim(),
          defaultBranch: repoForm.defaultBranch.trim() || 'main',
          forge: repoForm.forge,
          forgeRepo: repoForm.forge === 'github' ? repoForm.forgeRepo.trim() : '',
        }),
      );
      repoForm = null;
    } catch (err) {
      repoError = errorMessage(err);
    } finally {
      repoBusy = false;
    }
  }

  // ---- grants ----
  let grantStatus = $state<Record<string, string>>({});
  function grantFor(engineerId: string) {
    return p?.grants.find((g) => g.engineerId === engineerId);
  }
  async function setGrant(engineerId: string, access: string, actions: string[]) {
    grantStatus[engineerId] = 'Saving…';
    try {
      setProject(await api.putGrant(id, engineerId, { access, actions: access === 'write' ? actions : actions.filter((a) => a === 'publish_review') }));
      grantStatus[engineerId] = 'Saved';
      setTimeout(() => {
        if (grantStatus[engineerId] === 'Saved') grantStatus[engineerId] = '';
      }, 2000);
    } catch (err) {
      grantStatus[engineerId] = errorMessage(err);
    }
  }

  // ---- policy ----
  let policyDraft = $state<Project['policy'] | null>(null);
  // What a machine needs for this project's work, typed as "go, docker, os:darwin".
  let requiresText = $state('');
  const requiresLabel = (r: string) => (r === 'os:darwin' ? 'macOS' : r === 'os:linux' ? 'Linux' : r);
  let newCheck = $state('');
  let policyError = $state('');
  let policySaved = $state(false);
  function editPolicy() {
    if (!p) return;
    policyDraft = { ...p.policy, checks: [...(p.policy.checks ?? [])] };
    requiresText = (p.policy.requires ?? []).join(', ');
  }
  async function savePolicy(e: SubmitEvent) {
    e.preventDefault();
    if (!p || !policyDraft) return;
    policyError = '';
    try {
      const requires = requiresText
        .split(/[,\s]+/)
        .map((x) => x.trim())
        .filter(Boolean);
      setProject(await api.updateProject(id, { version: p.version, policy: { ...policyDraft, requires } }));
      policyDraft = null;
      policySaved = true;
      setTimeout(() => (policySaved = false), 2500);
    } catch (err) {
      policyError = err instanceof ApiError && err.conflict ? 'This project changed meanwhile; reload and try again.' : errorMessage(err);
    }
  }
</script>

<div class="screen">
  <div class="screen-inner">
    {#if !p}
      <h1 class="screen-title" data-screen-title tabindex="-1">{loadError ? 'Project not found' : 'Loading…'}</h1>
      {#if loadError}<p class="screen-sub">{loadError} <a href="/projects">All projects</a></p>{/if}
    {:else}
      <p class="crumb"><a href="/projects">Projects</a></p>
      <header class="screen-head">
        <div>
          <h1 class="screen-title" data-screen-title tabindex="-1">{p.name}</h1>
          {#if p.description}<p class="screen-sub">{p.description}</p>{/if}
        </div>
        {#if !editingAbout}
          <button
            class="btn"
            onclick={() => {
              name = p.name;
              description = p.description;
              instructions = p.instructions;
              editingAbout = true;
            }}>Edit</button
          >
        {/if}
      </header>

      {#if editingAbout}
        <form class="panel-box form" onsubmit={saveAbout}>
          <label class="field"><span class="label">Name</span><input class="input" bind:value={name} /></label>
          <label class="field"><span class="label">Description</span><input class="input" bind:value={description} /></label>
          <label class="field"><span class="label">Instructions for engineers</span><textarea class="textarea" rows="4" bind:value={instructions}></textarea></label>
          {#if aboutError}<p class="form-error" role="alert">{aboutError}</p>{/if}
          <div class="row"><button class="btn btn-primary btn-sm" type="submit">Save</button><button class="btn btn-sm" type="button" onclick={() => (editingAbout = false)}>Cancel</button></div>
        </form>
      {:else if p.instructions}
        <section class="section first" aria-labelledby="p-instr">
          <h2 class="section-title" id="p-instr">Instructions</h2>
          <div class="instr"><MessageBody message={{ body: p.instructions, mentions: [] }} /></div>
        </section>
      {/if}

      <section class="section" aria-labelledby="p-repos">
        <div class="section-head">
          <h2 class="section-title" id="p-repos">Repositories</h2>
          {#if !repoForm}<button class="btn btn-sm" onclick={() => editRepo()}><Icon name="plus" size={15} />Add repository</button>{/if}
        </div>
        {#if p.repos.length === 0 && !repoForm}
          <p class="meta">No repositories yet. Add one by a remote URL your machines can reach.</p>
        {/if}
        <ul class="repos">
          {#each p.repos as r (r.id)}
            <li>
              <div>
                <p class="r-name">{r.name} <span class="meta">· {r.defaultBranch}</span></p>
                <p class="mono r-url">{r.remoteUrl}</p>
                <p class="meta">{r.forge === 'github' ? `GitHub${r.forgeRepo ? ` · ${r.forgeRepo}` : ''}` : 'No forge connected'}</p>
              </div>
              <button class="btn btn-sm btn-quiet" onclick={() => editRepo(r)}>Edit</button>
            </li>
          {/each}
        </ul>
        {#if repoForm}
          <form class="panel-box form" onsubmit={saveRepo}>
            <div class="two">
              <label class="field"><span class="label">Name</span><input class="input" bind:value={repoForm.name} placeholder="atlas" /></label>
              <label class="field"><span class="label">Default branch</span><input class="input" bind:value={repoForm.defaultBranch} /></label>
            </div>
            <label class="field">
              <span class="label">Remote URL</span>
              <input class="input mono" bind:value={repoForm.remoteUrl} placeholder="git@github.com:acme/atlas.git" spellcheck="false" />
              <span class="hint">Must be reachable from your machines — they clone it themselves.</span>
            </label>
            <div class="two">
              <label class="field">
                <span class="label">Forge</span>
                <select class="select" bind:value={repoForm.forge}><option value="github">GitHub</option><option value="none">None</option></select>
              </label>
              {#if repoForm.forge === 'github'}
                <label class="field"><span class="label">Owner/name</span><input class="input mono" bind:value={repoForm.forgeRepo} placeholder="acme/atlas" spellcheck="false" /></label>
              {/if}
            </div>
            {#if repoError}<p class="form-error" role="alert">{repoError}</p>{/if}
            <div class="row">
              <button class="btn btn-primary btn-sm" type="submit" disabled={repoBusy}>{repoForm.id === 'new' ? 'Add repository' : 'Save'}</button>
              <button class="btn btn-sm" type="button" onclick={() => (repoForm = null)}>Cancel</button>
            </div>
          </form>
        {/if}
      </section>

      <section class="section" aria-labelledby="p-access">
        <h2 class="section-title" id="p-access">Access</h2>
        <p class="meta lead">Engineers act only within these grants. Anything else is asked for, exactly, in the conversation.</p>
        {#if engineers.length === 0}
          <p class="meta">No engineers yet.</p>
        {:else}
          <div class="table-wrap">
            <table class="grants">
              <caption class="vh">Access to {p.name} by engineer</caption>
              <thead>
                <tr>
                  <th scope="col">Engineer</th>
                  <th scope="col">Access</th>
                  {#each GRANT_ACTIONS as a (a.id)}<th scope="col">{a.label}</th>{/each}
                  <th scope="col"><span class="vh">Status</span></th>
                </tr>
              </thead>
              <tbody>
                {#each engineers as e (e.id)}
                  {@const g = grantFor(e.id)}
                  {@const access = g?.access || 'none'}
                  {@const actions = g?.actions ?? []}
                  <tr>
                    <th scope="row"><span class="who"><Avatar actor={{ kind: 'engineer', id: e.id }} size={22} />{e.name}</span></th>
                    <td>
                      <select class="select sm" aria-label="{e.name}'s access" value={access} onchange={(ev) => setGrant(e.id, (ev.target as HTMLSelectElement).value, actions)}>
                        <option value="none">No access</option>
                        <option value="read">Can read</option>
                        <option value="write">Can change</option>
                      </select>
                    </td>
                    {#each GRANT_ACTIONS as a (a.id)}
                      <td class="c">
                        <input
                          type="checkbox"
                          aria-label="{e.name}: {a.label}"
                          checked={actions.includes(a.id)}
                          disabled={access === 'none' || (access === 'read' && a.id !== 'publish_review')}
                          onchange={(ev) =>
                            setGrant(e.id, access, (ev.target as HTMLInputElement).checked ? [...actions, a.id] : actions.filter((x) => x !== a.id))}
                        />
                      </td>
                    {/each}
                    <td class="status meta" aria-live="polite">{grantStatus[e.id] ?? ''}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>

      <section class="section" aria-labelledby="p-policy">
        <div class="section-head">
          <h2 class="section-title" id="p-policy">Policy</h2>
          {#if !policyDraft}<button class="btn btn-sm" onclick={editPolicy}>Edit policy</button>{/if}
          {#if policySaved}<span class="meta" role="status">Saved.</span>{/if}
        </div>
        {#if policyDraft}
          <form class="panel-box form" onsubmit={savePolicy}>
            <label class="check"><input type="checkbox" bind:checked={policyDraft.requirePeerReview} /><span>Require a colleague's approval of the final revision</span></label>
            <label class="check">
              <input type="checkbox" bind:checked={policyDraft.requireHumanReview} />
              <span>Require my acceptance<br /><span class="meta">Work waits as “ready for your review” until you accept the exact revision. Off by default — engineers finish on their own.</span></span>
            </label>
            <label class="check">
              <input type="checkbox" bind:checked={policyDraft.autoPublish} />
              <span>Publish automatically when granted<br /><span class="meta">Pushes and pull requests still need the matching grant above.</span></span>
            </label>
            <fieldset class="fs">
              <legend class="label">Checks every change must pass</legend>
              {#each policyDraft.checks as c, i (i)}
                <div class="check-row">
                  <code class="mono">{c}</code>
                  <button type="button" class="btn btn-sm btn-quiet" onclick={() => policyDraft && (policyDraft.checks = policyDraft.checks.filter((_, j) => j !== i))}>Remove</button>
                </div>
              {/each}
              <div class="check-row">
                <input class="input mono" bind:value={newCheck} placeholder="e.g. go test ./..." aria-label="New check command" spellcheck="false" />
                <button
                  type="button"
                  class="btn btn-sm"
                  disabled={!newCheck.trim()}
                  onclick={() => {
                    if (policyDraft && newCheck.trim()) {
                      policyDraft.checks = [...policyDraft.checks, newCheck.trim()];
                      newCheck = '';
                    }
                  }}>Add</button
                >
              </div>
            </fieldset>
            <fieldset class="fs">
              <legend class="label">Where work runs</legend>
              <label class="check"><input type="radio" value="native" bind:group={policyDraft.executionProfile} /><span>Directly on the machine (native)</span></label>
              <label class="check"><input type="radio" value="container" bind:group={policyDraft.executionProfile} /><span>In a container<br /><span class="meta">Only machines with a container profile available can run it.</span></span></label>
            </fieldset>
            <label class="field">
              <span class="label">What a machine needs</span>
              <input class="input mono" bind:value={requiresText} placeholder="e.g. go, docker, os:darwin" />
              <span class="meta">Work in this project only goes to machines that report these tools or run this system. Machines list what they have.</span>
            </label>
            {#if policyError}<p class="form-error" role="alert">{policyError}</p>{/if}
            <div class="row"><button class="btn btn-primary btn-sm" type="submit">Save policy</button><button class="btn btn-sm" type="button" onclick={() => (policyDraft = null)}>Cancel</button></div>
          </form>
        {:else}
          <dl class="policy">
            <div><dt>Peer review</dt><dd>{p.policy.requirePeerReview ? 'Required — a colleague approves the final revision' : 'Not required'}</dd></div>
            <div><dt>Your review</dt><dd>{p.policy.requireHumanReview ? 'Required — you accept the exact revision' : 'Not required'}</dd></div>
            <div><dt>Checks</dt><dd>{#if p.policy.checks?.length}{#each p.policy.checks as c (c)}<code class="mono">{c}</code> {/each}{:else}None{/if}</dd></div>
            <div><dt>Runs</dt><dd>{p.policy.executionProfile === 'container' ? 'In a container' : 'Directly on the machine'}</dd></div>
            <div><dt>Machine needs</dt><dd>{#if p.policy.requires?.length}{p.policy.requires.map(requiresLabel).join(', ')}{:else}Nothing specific{/if}</dd></div>
            <div><dt>Publishing</dt><dd>{p.policy.autoPublish ? 'Automatic where granted' : 'Only when explicitly asked'}</dd></div>
          </dl>
        {/if}
      </section>

      <div class="cols">
        <section class="section" aria-labelledby="p-work">
          <h2 class="section-title" id="p-work">Open work</h2>
          {#if openWork.length === 0}
            <p class="meta">Nothing open{jobs.length ? '' : ' yet'}.</p>
          {:else}
            <ul class="list">
              {#each openWork as j (j.id)}
                <li>
                  <button class="work" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                    <StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />
                    <span><strong>{j.title}</strong> <span class="meta">· {jobStateLabel(j)} · {app.engineerName(j.ownerId)}</span></span>
                  </button>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
        <section class="section" aria-labelledby="p-rooms">
          <h2 class="section-title" id="p-rooms">Rooms</h2>
          {#if rooms.length === 0}<p class="meta">Not linked to a room. Link it from a room's settings.</p>{/if}
          <ul class="list">{#each rooms as r (r.id)}<li><a href="/rooms/{r.id}">{r.name}</a></li>{/each}</ul>
        </section>
      </div>

      <section class="section" aria-labelledby="p-dec">
        <h2 class="section-title" id="p-dec">Decisions</h2>
        {#if decisions.length === 0}
          <p class="meta">None recorded yet.</p>
        {:else}
          <ul class="list">
            {#each decisions as d (d.id)}
              <li><button class="link-btn" onclick={() => app.openPanel({ kind: 'decision', id: d.id })}>{d.title}</button> <span class="meta">· {d.status}</span></li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  </div>
</div>

<style>
  .crumb {
    font-size: 13px;
    margin-bottom: 10px;
  }
  .first {
    margin-top: 0;
  }
  .instr {
    padding: 12px 14px;
    border-radius: var(--r-artifact);
    background: var(--surface-subtle);
    font-size: 14.5px;
    max-width: 80ch;
  }
  .form {
    display: grid;
    gap: 12px;
    padding: 16px;
    margin-top: 10px;
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
  .repos {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .repos li {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: 12px;
  }
  .r-name {
    font-weight: 650;
  }
  .r-url {
    overflow-wrap: anywhere;
    color: var(--ink-secondary);
  }
  .lead {
    margin-bottom: 10px;
  }
  .table-wrap {
    overflow-x: auto;
    border: 1px solid var(--line);
    border-radius: 12px;
  }
  .grants {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }
  .grants th,
  .grants td {
    padding: 8px 10px;
    text-align: left;
    border-top: 1px solid var(--line-soft);
    white-space: nowrap;
  }
  .grants thead th {
    border-top: 0;
    font-size: 12.5px;
    font-weight: 600;
    color: var(--ink-secondary);
    background: var(--surface-subtle);
  }
  .grants .c {
    text-align: center;
  }
  .grants input[type='checkbox'] {
    width: 18px;
    height: 18px;
    accent-color: var(--accent);
  }
  .who {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-weight: 600;
  }
  .select.sm {
    min-height: 32px;
    padding: 4px 8px;
    font-size: 13.5px;
    width: auto;
  }
  .status {
    min-width: 80px;
  }
  .fs {
    border: 0;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 6px;
  }
  .check-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .policy {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .policy > div {
    display: grid;
    grid-template-columns: 130px minmax(0, 1fr);
    gap: 10px;
    font-size: 14px;
  }
  .policy dt {
    color: var(--ink-secondary);
  }
  .policy dd {
    margin: 0;
  }
  .cols {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 24px;
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
    display: flex;
    align-items: center;
    gap: 8px;
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
  @media (max-width: 760px) {
    .two,
    .cols {
      grid-template-columns: 1fr;
    }
    .policy > div {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
</style>
