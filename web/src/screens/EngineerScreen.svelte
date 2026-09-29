<script lang="ts">
  // An engineer's profile: role and instructions (versioned), capabilities,
  // provider preference with readiness, rooms, active work, and decisions.
  import { onMount } from 'svelte';
  import { Button, Card, Collapsible, Link, List, ListItem, Selector, Switch, Text, TextArea, TextInput, Token } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { Decision, EngineerVersion, Job, ProviderProfile, UpdateEngineerRequest } from '../lib/api/types.gen';
  import { isLiveJob, newer } from '../lib/state/data';
  import { billingLabel, jobShape, jobStateLabel, jobTone } from '../lib/util/labels';
  import { atTime, relative } from '../lib/util/time';
  import { roomSettings } from '../lib/util/setup';
  import Avatar from '../components/Avatar.svelte';
  import Notice from '../components/Notice.svelte';
  import StateIcon from '../components/StateIcon.svelte';
  import MessageBody from '../components/MessageBody.svelte';
  import ProviderSelect from '../components/ProviderSelect.svelte';
  import ConfirmDialog from '../components/ConfirmDialog.svelte';
  import EngineerNotes from '../components/EngineerNotes.svelte';
  import Screen from '../components/Screen.svelte';
  import ScreenSection from '../components/ScreenSection.svelte';

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
        for (const j of jobs) if (newer(app.data.jobs[j.id], j)) app.data.jobs[j.id] = j;
      })
      .catch(() => {});
    api
      .decisions()
      .then((ds) => (decisions = (ds ?? []).filter((d) => d.createdBy.kind === 'engineer' && d.createdBy.id === id)))
      .catch(() => {});
  }
  onMount(() => void load());

  const rooms = $derived((e?.roomIds ?? []).map((r) => app.data.rooms[r]).filter(Boolean));
  const availableRooms = $derived(Object.values(app.data.rooms).filter((r) => r.kind === 'room' && !r.archived));
  const availableProjects = $derived(Object.values(app.data.projects));
  // Provider details: the models machines report for this provider, and the
  // signed-in accounts (profiles) it could run on.
  let profiles = $state<ProviderProfile[]>([]);
  $effect(() => {
    void api.providerProfiles().then((p) => (profiles = p), () => {});
  });
  const providerModels = $derived.by(() => {
    const seen = new Map<string, string>();
    for (const n of Object.values(app.data.nodes)) {
      for (const inst of n.providers ?? []) {
        if (inst.provider !== e?.provider.provider) continue;
        for (const m of inst.models ?? []) if (!seen.has(m.id)) seen.set(m.id, m.label || m.id);
      }
    }
    return [...seen].map(([id, label]) => ({ id, label }));
  });
  const accounts = $derived(profiles.filter((p) => p.provider === e?.provider.provider));
  // The model and account choices, with "" standing for the provider's default
  // and any signed-in account. A model no machine reports any more stays listed
  // so the current choice is never shown as something else.
  const modelOptions = $derived.by(() => {
    const model = e?.provider.model;
    const reported = providerModels.map((m) => ({ value: m.id, label: m.label }));
    const stale = model && !providerModels.some((m) => m.id === model) ? [{ value: model, label: model }] : [];
    return [{ value: '', label: "The provider's default" }, ...reported, ...stale];
  });
  const accountOptions = $derived([
    { value: '', label: 'Any signed-in account' },
    ...accounts.map((p) => ({ value: p.id, label: `${p.label} · ${billingLabel(p.billing)}` })),
  ]);
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

{#if !e}
  {#snippet notFound()}{loadError} <Link hasUnderline href="/engineers">All engineers</Link>{/snippet}
  <Screen title={loadError ? 'Engineer not found' : 'Loading…'} subtitle={loadError ? notFound : undefined} />
{:else}
  {#snippet editProfile()}<Button label="Edit profile" onclick={startProfile} />{/snippet}
  <Screen title={e.name} subtitle="{e.role} · AI engineer · @{e.handle}{e.archived ? ' · archived' : ''}" actions={editingProfile ? undefined : editProfile}>
    {#snippet crumb()}<Link href="/engineers">Engineers</Link>{/snippet}
    {#snippet leading()}<Avatar actor={{ kind: 'engineer', id: e.id }} size={64} />{/snippet}
    {#if error}<div class="alert"><Notice tone="danger" role="alert">{error}</Notice></div>{/if}

    {#if editingProfile}
      <div class="block">
        <Card>
          <form class="form" onsubmit={saveProfile}>
            <div class="two">
              <TextInput label="Name" bind:value={name} description="Renaming keeps the same engineer and their message history." />
              <TextInput label="Role" bind:value={role} />
            </div>
            <TextInput label="What they're for" bind:value={description} />
            <TextInput label="Capabilities" bind:value={tags} description="Comma-separated." />
            <div class="row">
              <Button label="Save" variant="primary" size="sm" type="submit" isLoading={saving} />
              <Button label="Cancel" size="sm" onclick={() => (editingProfile = false)} />
            </div>
          </form>
        </Card>
      </div>
    {:else}
      {#if e.description}<p class="desc">{e.description}</p>{/if}
      {#if e.capabilityTags.length}
        <ul class="tags" aria-label="Capabilities">{#each e.capabilityTags as t (t)}<li><Token label={t} size="sm" /></li>{/each}</ul>
      {/if}
    {/if}

    <div class="cols">
      <div class="col">
        <ScreenSection title="Standing instructions" id="eng-instr">
          {#snippet end()}
            <Text type="supporting">version {e.versionNo}</Text>
            {#if !editingInstructions}
              <Button
                label="Edit"
                size="sm"
                onclick={() => {
                  instructions = e.instructions;
                  editingInstructions = true;
                }}
              />
            {/if}
          {/snippet}
          {#if editingInstructions}
            <form class="form" onsubmit={saveInstructions}>
              <TextArea label="Standing instructions" isLabelHidden rows={7} bind:value={instructions} />
              <Notice
                title="Saving creates version {e.versionNo + 1}."
                description="Work already running keeps the instructions it started with; new work uses the new version."
              />
              <div class="row">
                <Button
                  label="Save as version {e.versionNo + 1}"
                  variant="primary"
                  size="sm"
                  type="submit"
                  isLoading={saving}
                  isDisabled={instructions.trim() === e.instructions}
                />
                <Button label="Cancel" size="sm" onclick={() => (editingInstructions = false)} />
              </div>
            </form>
          {:else}
            <div class="instr"><MessageBody message={{ body: e.instructions || 'No standing instructions.', mentions: [] }} /></div>
          {/if}
          {#if versions.length > 1}
            <div class="history">
              <Collapsible trigger="Version history ({versions.length})" defaultIsOpen={false}>
                <ol class="versions">
                  {#each versions as v (v.id)}
                    <li>
                      <p>
                        <Text weight="semibold">Version {v.versionNo}</Text>
                        <Text type="supporting">· {atTime(v.createdAt)}{v.versionNo === e.versionNo ? ' · current' : ''}</Text>
                      </p>
                      <Text as="p" type="supporting">{v.role}{v.name !== e.name ? ` · named ${v.name}` : ''}</Text>
                      <div class="instr small"><MessageBody message={{ body: v.instructions || '—', mentions: [] }} /></div>
                    </li>
                  {/each}
                </ol>
              </Collapsible>
            </div>
          {/if}
        </ScreenSection>

        <ScreenSection title="Active and queued work" id="eng-work">
          {#if liveJobs.length === 0}
            <Text as="p" type="supporting">Nothing in progress.</Text>
          {:else}
            <List density="compact">
              {#each liveJobs as j (j.id)}
                <ListItem description="{jobStateLabel(j)} · {app.data.rooms[j.source.roomId]?.name ?? ''}" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                  {#snippet label()}{j.title}{/snippet}
                  {#snippet startContent()}<StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />{/snippet}
                </ListItem>
              {/each}
            </List>
          {/if}
          {#if recent.length}
            <h3 class="sub">Recently</h3>
            <List density="compact">
              {#each recent as j (j.id)}
                <ListItem description="{jobStateLabel(j)} · {relative(j.completedAt ?? j.updatedAt, app.now)}" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                  {#snippet label()}{j.title}{/snippet}
                  {#snippet startContent()}<StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} />{/snippet}
                </ListItem>
              {/each}
            </List>
          {/if}
        </ScreenSection>

        <EngineerNotes engineerId={id} name={e.name} />

        <ScreenSection title="Decisions they recorded" id="eng-dec">
          {#if decisions.length === 0}
            <Text as="p" type="supporting">None yet.</Text>
          {:else}
            <List density="compact">
              {#each decisions as d (d.id)}
                <ListItem
                  description="{d.status} · {d.sources.length} {d.sources.length === 1 ? 'source' : 'sources'}"
                  onclick={() => app.openPanel({ kind: 'decision', id: d.id })}
                >
                  {#snippet label()}{d.title}{/snippet}
                </ListItem>
              {/each}
            </List>
          {/if}
        </ScreenSection>
      </div>

      <div class="col side">
        <ScreenSection title="Rooms" id="eng-rooms">
          {#if rooms.length === 0}
            <div class="setup">
              <Text as="p" type="supporting">Invite {e.name} into a conversation. Choose them in the room's settings.</Text>
              {#if availableRooms.length === 1}
                <p><Link hasUnderline href={roomSettings(availableRooms[0])}>Set up {availableRooms[0].name}</Link></p>
              {:else if availableRooms.length > 1}
                <Collapsible trigger="Choose a room" defaultIsOpen={false}>
                  <ul class="links">{#each availableRooms as r (r.id)}<li><Link hasUnderline href={roomSettings(r)}>{r.name}</Link></li>{/each}</ul>
                </Collapsible>
              {/if}
              <div><Button label="Create a room" size="sm" onclick={() => (app.createRoom = { kind: 'room' })} /></div>
            </div>
          {:else}
            <ul class="links">
              {#each rooms as r (r.id)}
                <li><Link hasUnderline href="/rooms/{r.id}">{r.kind === 'dm' ? 'Direct messages' : r.name}</Link>{#if r.private}{' '}<Text type="supporting">· private</Text>{/if}</li>
              {/each}
            </ul>
          {/if}
        </ScreenSection>
        <ScreenSection title="Projects they can work on" id="eng-proj">
          {#if permitted.length === 0}
            <div class="setup">
              <Text as="p" type="supporting">You can talk now. For repository work, choose what {e.name} can access.</Text>
              {#if availableProjects.length === 1}
                <p><Link hasUnderline href="/projects/{availableProjects[0].id}#p-access">Choose access to {availableProjects[0].name}</Link></p>
              {:else if availableProjects.length > 1}
                <Collapsible trigger="Choose a project" defaultIsOpen={false}>
                  <ul class="links">{#each availableProjects as p (p.id)}<li><Link hasUnderline href="/projects/{p.id}#p-access">Choose access to {p.name}</Link></li>{/each}</ul>
                </Collapsible>
              {:else}
                <div><Button label="Connect a project" size="sm" href="/projects" /></div>
              {/if}
            </div>
          {:else}
            <ul class="links">
              {#each permitted as x (x.project.id)}
                <li>
                  <Link hasUnderline href="/projects/{x.project.id}">{x.project.name}</Link>{' '}<Text type="supporting"
                    >· {x.grant?.access === 'write' ? 'can change code' : 'read only'}{x.grant?.actions.length
                      ? ` · can ${x.grant.actions.map((a) => ACTION_LABELS[a] ?? a).join(', ')}`
                      : ''}</Text
                  >
                </li>
              {/each}
            </ul>
          {/if}
        </ScreenSection>
        <ScreenSection title="Provider preference" id="eng-prov">
          <div class="prov">
            <ProviderSelect
              value={e.provider.provider}
              profileId={e.provider.profileId}
              allowApiBilling={e.provider.allowApiBilling}
              onchange={(v) => {
                // Selectors report re-picking the current option too; only real changes are
                // saved here and below (a provider re-pick would otherwise reset model and account).
                if (v !== e.provider.provider) void patch({ provider: { ...e.provider, provider: v, model: '', profileId: '' } }, () => {});
              }}
            />
            {#if e.provider.provider !== 'fake'}
              <div class="prov-grid">
                <Selector
                  label="Model"
                  width="100%"
                  options={modelOptions}
                  value={e.provider.model ?? ''}
                  onChange={(v: string) => {
                    if (v !== (e.provider.model ?? '')) void patch({ provider: { ...e.provider, model: v } }, () => {});
                  }}
                />
                <Selector
                  label="Account"
                  width="100%"
                  options={accountOptions}
                  value={e.provider.profileId ?? ''}
                  onChange={(v: string) => {
                    if (v !== (e.provider.profileId ?? '')) void patch({ provider: { ...e.provider, profileId: v } }, () => {});
                  }}
                />
              </div>
              <Switch
                label="Allow runs billed to an API key"
                description="Off: this engineer only uses subscription sign-ins and waits rather than falling back to paid API usage."
                value={!!e.provider.allowApiBilling}
                onChange={(on) => patch({ provider: { ...e.provider, allowApiBilling: on } }, () => {})}
              />
            {/if}
            <Text as="p" type="supporting">Which model ran a job is shown in the job's run details, not in conversation.</Text>
          </div>
        </ScreenSection>
        <div class="lifecycle">
          {#if e.archived}
            <Button label="Restore engineer" size="sm" onclick={() => patch({ archived: false }, () => {})} />
          {:else}
            <Button label="Archive engineer" size="sm" variant="destructive" onclick={() => (confirmArchive = true)} />
          {/if}
        </div>
      </div>
    </div>
  </Screen>
{/if}

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
  .alert,
  .block {
    margin-bottom: var(--spacing-3);
  }
  .desc {
    max-width: 70ch;
    margin-bottom: var(--spacing-3);
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
  }
  .form {
    display: grid;
    gap: var(--spacing-3);
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    align-items: end;
    gap: var(--spacing-3);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-2);
  }
  .cols {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 300px;
    gap: var(--spacing-8);
  }
  .col {
    min-width: 0;
  }
  .instr {
    padding: var(--spacing-3) var(--spacing-4);
    border-radius: var(--radius-container);
    background: var(--color-background-muted);
  }
  .instr.small {
    padding: var(--spacing-2) var(--spacing-3);
    font-size: var(--font-size-sm);
  }
  .history {
    margin-top: var(--spacing-2);
  }
  .versions {
    display: grid;
    gap: var(--spacing-3);
    padding-top: var(--spacing-2);
  }
  .versions li {
    display: grid;
    gap: var(--spacing-1);
  }
  .sub {
    margin: var(--spacing-4) 0 var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    color: var(--color-text-secondary);
  }
  .setup {
    display: grid;
    justify-items: start;
    gap: var(--spacing-3);
  }
  .prov {
    display: grid;
    gap: var(--spacing-3);
  }
  .links {
    display: grid;
    gap: var(--spacing-1);
  }
  .prov-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--spacing-3);
  }
  .lifecycle {
    margin-top: var(--spacing-8);
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
