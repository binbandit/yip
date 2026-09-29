<script lang="ts">
  // A project: repositories, who may read or change them (and which exact
  // actions they may take), review policy and checks, linked rooms, open work
  // and decisions.
  import { onMount } from 'svelte';
  import { Button, Card, CheckboxInput, Code, CodeBlock, FileInput, Icon, Link, List, ListItem, MetadataList, MetadataListItem, RadioList, RadioListItem, Selector, Table, TableBody, TableCell, TableHeader, TableHeaderCell, TableRow, Text, TextArea, TextInput, VisuallyHidden } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import { Plus } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { atTime } from '../lib/util/time';
  import { api } from '../lib/api/endpoints';
  import { ApiError, errorMessage } from '../lib/api/client';
  import type { Decision, Job, Project, Repo } from '../lib/api/types.gen';
  import { isLiveJob, newer } from '../lib/state/data';
  import { GRANT_ACTIONS, jobShape, jobStateLabel, jobTone } from '../lib/util/labels';
  import Avatar from '../components/Avatar.svelte';
  import StateIcon from '../components/StateIcon.svelte';
  import MessageBody from '../components/MessageBody.svelte';
  import Screen from '../components/Screen.svelte';
  import ScreenSection from '../components/ScreenSection.svelte';

  interface Props {
    id: string;
  }
  let { id }: Props = $props();
  const p = $derived(app.data.projects[id]);
  let jobs = $state<Job[]>([]);
  let decisions = $state<Decision[]>([]);
  let loadError = $state('');

  // Commands, URLs and repository names are typed exactly; keep the browser from "correcting" them.
  const literalHints = { spellcheck: false };
  const ACCESS_OPTIONS = [
    { value: 'none', label: 'No access' },
    { value: 'read', label: 'Can read' },
    { value: 'write', label: 'Can change' },
  ];
  const FORGE_OPTIONS = [
    { value: 'github', label: 'GitHub' },
    { value: 'none', label: 'None' },
  ];

  onMount(() => {
    api
      .project(id)
      .then(setProject)
      .catch((e) => (loadError = errorMessage(e)));
    api
      .jobs({ project: id })
      .then((js) => {
        jobs = js ?? [];
        for (const j of jobs) if (newer(app.data.jobs[j.id], j)) app.data.jobs[j.id] = j;
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

  function saveError(err: unknown): string {
    return err instanceof ApiError && err.conflict ? 'This project changed meanwhile; reload and try again.' : errorMessage(err);
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
      aboutError = saveError(err);
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

  // ---- import a repository from a folder (git bundle) ----
  let importForm = $state<null | { repoId: string; name: string; branch: string; file: File | null }>(null);
  let importError = $state('');
  let importBusy = $state(false);
  function startImport(r?: Repo) {
    importError = '';
    repoForm = null;
    importForm = { repoId: r?.id ?? '', name: r?.name ?? '', branch: '', file: null };
  }
  async function saveImport(e: SubmitEvent) {
    e.preventDefault();
    if (!importForm) return;
    if (!importForm.repoId && !importForm.name.trim()) {
      importError = 'Name the repository.';
      return;
    }
    if (!importForm.file) {
      importError = 'Choose the bundle file you created.';
      return;
    }
    importBusy = true;
    importError = '';
    try {
      setProject(await api.importRepo(id, importForm.file, { name: importForm.name.trim(), branch: importForm.branch.trim(), repoId: importForm.repoId }));
      app.toast(importForm.repoId ? 'Updated from the new bundle. Machines pick it up on their next run.' : 'Imported. Machines build their copy from it.');
      importForm = null;
    } catch (err) {
      importError = errorMessage(err);
    } finally {
      importBusy = false;
    }
  }
  const bundleCmd = $derived(`git -C /path/to/${importForm?.name.trim() || 'repo'} bundle create ${importForm?.name.trim() || 'repo'}.bundle --all`);

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
  function addCheck() {
    if (policyDraft && newCheck.trim()) {
      policyDraft.checks = [...policyDraft.checks, newCheck.trim()];
      newCheck = '';
    }
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
      policyError = saveError(err);
    }
  }
</script>

{#if !p}
  {#snippet notFound()}{loadError} <Link hasUnderline href="/projects">All projects</Link>{/snippet}
  <Screen title={loadError ? 'Project not found' : 'Loading…'} subtitle={loadError ? notFound : undefined} />
{:else}
  {#snippet editAbout()}
    <Button
      label="Edit"
      onclick={() => {
        name = p.name;
        description = p.description;
        instructions = p.instructions;
        editingAbout = true;
      }}
    />
  {/snippet}
  <Screen title={p.name} subtitle={p.description || undefined} actions={editingAbout ? undefined : editAbout}>
    {#snippet crumb()}<Link href="/projects">Projects</Link>{/snippet}

    {#if editingAbout}
      <Card>
        <form class="form" onsubmit={saveAbout}>
          <TextInput label="Name" bind:value={name} />
          <TextInput label="Description" bind:value={description} />
          <TextArea label="Instructions for engineers" rows={4} bind:value={instructions} />
          {#if aboutError}<Notice tone="danger" role="alert">{aboutError}</Notice>{/if}
          <div class="row">
            <Button label="Save" variant="primary" size="sm" type="submit" />
            <Button label="Cancel" size="sm" onclick={() => (editingAbout = false)} />
          </div>
        </form>
      </Card>
    {:else if p.instructions}
      <!-- The first section sits right under the header, without a section's top margin. -->
      <div class="first">
        <ScreenSection title="Instructions" id="p-instr">
          <div class="instr"><MessageBody message={{ body: p.instructions, mentions: [] }} /></div>
        </ScreenSection>
      </div>
    {/if}

    <ScreenSection title="Repositories" id="p-repos">
      {#snippet end()}
        {#if !repoForm && !importForm}
          <Button label="Import from a folder" size="sm" variant="ghost" onclick={() => startImport()} />
          <Button label="Add repository" size="sm" onclick={() => editRepo()}>
            {#snippet icon()}<Icon icon={Plus} size="sm" />{/snippet}
          </Button>
        {/if}
      {/snippet}
      <div class="stack">
        {#if p.repos.length === 0 && !repoForm}
          <Text as="p" type="supporting">
            No repositories yet. Add one by a remote URL your machines can reach, or import one from a folder on this computer.
          </Text>
        {/if}
        {#if p.repos.length}
          <ul class="repos">
            {#each p.repos as r (r.id)}
              <li>
                <div class="r-main">
                  <p class="r-name">{r.name} <Text type="supporting" weight="normal">· {r.defaultBranch}</Text></p>
                  {#if r.sourceBundleId && !r.remoteUrl}
                    <Text as="p" type="supporting">Imported from a folder{r.importedAt ? ` · ${atTime(r.importedAt)}` : ''}</Text>
                    <Text as="p" type="supporting">No remote: engineers publish revisions here; there's nothing to push to.</Text>
                  {:else}
                    <p class="r-url">{r.remoteUrl}</p>
                    <Text as="p" type="supporting">{r.forge === 'github' ? `GitHub${r.forgeRepo ? ` · ${r.forgeRepo}` : ''}` : 'No forge connected'}</Text>
                  {/if}
                </div>
                {#if r.sourceBundleId && !r.remoteUrl}
                  <div class="row">
                    <Button label="Import a newer bundle" size="sm" variant="ghost" onclick={() => startImport(r)} />
                    <Button label="Add a remote" size="sm" variant="ghost" onclick={() => editRepo(r)} />
                  </div>
                {:else}
                  <Button label="Edit" size="sm" variant="ghost" onclick={() => editRepo(r)} />
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        {#if importForm}
          <Card>
            <form class="form" onsubmit={saveImport}>
              <Text as="p">
                For code that isn't on a remote your machines can reach. In the folder, make a bundle of its history, then choose it here. Only
                committed work is included — commit anything you want the team to see first.
              </Text>
              <CodeBlock code={bundleCmd} width="100%" size="sm" isWrapped />
              <div class="two">
                {#if !importForm.repoId}
                  <TextInput label="Name" bind:value={importForm.name} placeholder="atlas" {...literalHints} />
                {/if}
                <TextInput label="Default branch" bind:value={importForm.branch} placeholder="from the bundle" {...literalHints} />
              </div>
              <FileInput
                label="Bundle file"
                description="Up to 512 MB. Machines build their copy from it; nothing is pushed anywhere."
                accept=".bundle,application/octet-stream"
                value={importForm.file}
                onChange={(f) => {
                  if (importForm) importForm.file = Array.isArray(f) ? (f[0] ?? null) : f;
                }}
              />
              {#if importError}<Notice tone="danger" role="alert">{importError}</Notice>{/if}
              <div class="row">
                <Button
                  label={importForm.repoId ? 'Import newer bundle' : 'Import repository'}
                  variant="primary"
                  size="sm"
                  type="submit"
                  isLoading={importBusy}
                />
                <Button label="Cancel" size="sm" onclick={() => (importForm = null)} />
              </div>
            </form>
          </Card>
        {/if}
        {#if repoForm}
          <Card>
            <form class="form" onsubmit={saveRepo}>
              <div class="two">
                <TextInput label="Name" bind:value={repoForm.name} placeholder="atlas" {...literalHints} />
                <TextInput label="Default branch" bind:value={repoForm.defaultBranch} {...literalHints} />
              </div>
              <TextInput
                label="Remote URL"
                class="mono"
                bind:value={repoForm.remoteUrl}
                placeholder="git@github.com:acme/atlas.git"
                description="Must be reachable from your machines — they clone it themselves."
                {...literalHints}
              />
              <div class="two">
                <Selector
                  label="Forge"
                  width="100%"
                  options={FORGE_OPTIONS}
                  value={repoForm.forge}
                  onChange={(v: string) => {
                    if (repoForm) repoForm.forge = v;
                  }}
                />
                {#if repoForm.forge === 'github'}
                  <TextInput label="Owner/name" class="mono" bind:value={repoForm.forgeRepo} placeholder="acme/atlas" {...literalHints} />
                {/if}
              </div>
              {#if repoError}<Notice tone="danger" role="alert">{repoError}</Notice>{/if}
              <div class="row">
                <Button label={repoForm.id === 'new' ? 'Add repository' : 'Save'} variant="primary" size="sm" type="submit" isLoading={repoBusy} />
                <Button label="Cancel" size="sm" onclick={() => (repoForm = null)} />
              </div>
            </form>
          </Card>
        {/if}
      </div>
    </ScreenSection>

    <ScreenSection title="Access" id="p-access">
      <div class="stack">
        <Text as="p" type="supporting">Engineers act only within these grants. Anything else is asked for, exactly, in the conversation.</Text>
        {#if engineers.length === 0}
          <Text as="p" type="supporting">No engineers yet.</Text>
        {:else}
          <div class="grants-frame">
            <Table class="grants" density="compact">
              <VisuallyHidden as="caption">Access to {p.name} by engineer</VisuallyHidden>
              <TableHeader>
                <TableRow isHeaderRow>
                  <TableHeaderCell scope="col">Engineer</TableHeaderCell>
                  <TableHeaderCell scope="col">Access</TableHeaderCell>
                  {#each GRANT_ACTIONS as a (a.id)}<TableHeaderCell scope="col" class="c">{a.label}</TableHeaderCell>{/each}
                  <TableHeaderCell scope="col"><VisuallyHidden>Status</VisuallyHidden></TableHeaderCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {#each engineers as e (e.id)}
                  {@const g = grantFor(e.id)}
                  {@const access = g?.access || 'none'}
                  {@const actions = g?.actions ?? []}
                  <TableRow>
                    <TableHeaderCell scope="row"><span class="who"><Avatar actor={{ kind: 'engineer', id: e.id }} size={24} />{e.name}</span></TableHeaderCell>
                    <TableCell>
                      <Selector
                        label="{e.name}'s access"
                        isLabelHidden
                        size="sm"
                        width={136}
                        options={ACCESS_OPTIONS}
                        value={access}
                        onChange={(v: string) => {
                          // The selector also reports re-picking the current access; only save a change.
                          if (v !== access) void setGrant(e.id, v, actions);
                        }}
                      />
                    </TableCell>
                    {#each GRANT_ACTIONS as a (a.id)}
                      <TableCell class="c">
                        <CheckboxInput
                          label="{e.name}: {a.label}"
                          isLabelHidden
                          value={actions.includes(a.id)}
                          isDisabled={access === 'none' || (access === 'read' && a.id !== 'publish_review')}
                          onChange={(checked) => setGrant(e.id, access, checked ? [...actions, a.id] : actions.filter((x) => x !== a.id))}
                        />
                      </TableCell>
                    {/each}
                    <TableCell class="status" aria-live="polite">{grantStatus[e.id] ?? ''}</TableCell>
                  </TableRow>
                {/each}
              </TableBody>
            </Table>
          </div>
        {/if}
      </div>
    </ScreenSection>

    <ScreenSection title="Policy" id="p-policy">
      {#snippet end()}
        {#if policySaved}<Text type="supporting" role="status">Saved.</Text>{/if}
        {#if !policyDraft}<Button label="Edit policy" size="sm" onclick={editPolicy} />{/if}
      {/snippet}
      {#if policyDraft}
        <Card>
          <form class="form" onsubmit={savePolicy}>
            <CheckboxInput
              label="Require a colleague's approval of the final revision"
              value={policyDraft.requirePeerReview}
              onChange={(on) => {
                if (policyDraft) policyDraft.requirePeerReview = on;
              }}
            />
            <CheckboxInput
              label="Require my acceptance"
              description="Work waits as “ready for your review” until you accept the exact revision. Off by default — engineers finish on their own."
              value={policyDraft.requireHumanReview}
              onChange={(on) => {
                if (policyDraft) policyDraft.requireHumanReview = on;
              }}
            />
            <CheckboxInput
              label="Publish automatically when granted"
              description="Pushes and pull requests still need the matching grant above."
              value={policyDraft.autoPublish}
              onChange={(on) => {
                if (policyDraft) policyDraft.autoPublish = on;
              }}
            />
            <fieldset class="fs">
              <legend class="legend">Checks every change must pass</legend>
              {#each policyDraft.checks as c, i (i)}
                <div class="check-row">
                  <Code>{c}</Code>
                  <Button
                    label="Remove"
                    size="sm"
                    variant="ghost"
                    onclick={() => {
                      if (policyDraft) policyDraft.checks = policyDraft.checks.filter((_, j) => j !== i);
                    }}
                  />
                </div>
              {/each}
              <div class="check-row">
                <TextInput label="New check command" isLabelHidden class="mono" size="sm" bind:value={newCheck} placeholder="e.g. go test ./..." {...literalHints} />
                <Button label="Add" size="sm" isDisabled={!newCheck.trim()} onclick={addCheck} />
              </div>
            </fieldset>
            <RadioList
              label="Where work runs"
              value={policyDraft.executionProfile}
              onChange={(v) => {
                if (policyDraft) policyDraft.executionProfile = v;
              }}
            >
              <RadioListItem label="Directly on the machine (native)" value="native" />
              <RadioListItem label="In a container" value="container" description="Only machines with a container profile available can run it." />
            </RadioList>
            <TextInput
              label="What a machine needs"
              class="mono"
              bind:value={requiresText}
              placeholder="e.g. go, docker, os:darwin"
              description="Work in this project only goes to machines that report these tools or run this system. Machines list what they have."
              {...literalHints}
            />
            {#if policyError}<Notice tone="danger" role="alert">{policyError}</Notice>{/if}
            <div class="row">
              <Button label="Save policy" variant="primary" size="sm" type="submit" />
              <Button label="Cancel" size="sm" onclick={() => (policyDraft = null)} />
            </div>
          </form>
        </Card>
      {:else}
        <MetadataList label={app.narrow ? { position: 'top' } : { position: 'start', width: 130 }}>
          <MetadataListItem label="Peer review">{p.policy.requirePeerReview ? 'Required — a colleague approves the final revision' : 'Not required'}</MetadataListItem>
          <MetadataListItem label="Your review">{p.policy.requireHumanReview ? 'Required — you accept the exact revision' : 'Not required'}</MetadataListItem>
          <MetadataListItem label="Checks">
            {#if p.policy.checks?.length}<span class="checks">{#each p.policy.checks as c (c)}<Code>{c}</Code>{/each}</span>{:else}None{/if}
          </MetadataListItem>
          <MetadataListItem label="Runs">{p.policy.executionProfile === 'container' ? 'In a container' : 'Directly on the machine'}</MetadataListItem>
          <MetadataListItem label="Machine needs">{#if p.policy.requires?.length}{p.policy.requires.map(requiresLabel).join(', ')}{:else}Nothing specific{/if}</MetadataListItem>
          <MetadataListItem label="Publishing">{p.policy.autoPublish ? 'Automatic where granted' : 'Only when explicitly asked'}</MetadataListItem>
        </MetadataList>
      {/if}
    </ScreenSection>

    <div class="cols">
      <ScreenSection title="Open work" id="p-work">
        {#if openWork.length === 0}
          <Text as="p" type="supporting">Nothing open{jobs.length ? '' : ' yet'}.</Text>
        {:else}
          <List density="compact">
            {#each openWork as j (j.id)}
              <ListItem description="{jobStateLabel(j)} · {app.engineerName(j.ownerId)}" onclick={() => app.openPanel({ kind: 'job', id: j.id })}>
                {#snippet label()}{j.title}{/snippet}
                {#snippet startContent()}<StateIcon shape={jobShape(j.state)} tone={jobTone(j.state)} live={j.state === 'running'} />{/snippet}
              </ListItem>
            {/each}
          </List>
        {/if}
      </ScreenSection>
      <ScreenSection title="Rooms" id="p-rooms">
        {#if rooms.length === 0}
          <Text as="p" type="supporting">Not linked to a room. Link it from a room's settings.</Text>
        {:else}
          <ul class="links">{#each rooms as r (r.id)}<li><Link hasUnderline href="/rooms/{r.id}">{r.name}</Link></li>{/each}</ul>
        {/if}
      </ScreenSection>
    </div>

    <ScreenSection title="Decisions" id="p-dec">
      {#if decisions.length === 0}
        <Text as="p" type="supporting">None recorded yet.</Text>
      {:else}
        <List density="compact">
          {#each decisions as d (d.id)}
            <ListItem description={d.status} onclick={() => app.openPanel({ kind: 'decision', id: d.id })}>
              {#snippet label()}{d.title}{/snippet}
            </ListItem>
          {/each}
        </List>
      {/if}
    </ScreenSection>
  </Screen>
{/if}

<style>
  .first > :global(.section) {
    margin-top: 0;
  }
  .stack {
    display: grid;
    gap: var(--spacing-3);
  }
  .instr {
    max-width: 80ch;
    padding: var(--spacing-3) var(--spacing-4);
    border-radius: var(--radius-container);
    background: var(--color-background-muted);
  }
  .form {
    display: grid;
    gap: var(--spacing-3);
  }
  .form :global(.mono input) {
    font-family: var(--font-family-code);
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
  .repos {
    display: grid;
    gap: var(--spacing-2);
  }
  .repos li {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--spacing-3);
    padding: var(--spacing-3) var(--spacing-4);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
  }
  .r-main {
    display: grid;
    gap: var(--spacing-0-5);
    min-width: 0;
  }
  .r-name {
    font-weight: var(--font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .r-url {
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }
  .grants-frame {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    overflow: hidden;
  }
  /* Cells normally shrink and clip to their column; here each column fits its label and control whole, and the table's own scroll wrapper scrolls sideways when they don't fit. */
  .grants-frame :global(th),
  .grants-frame :global(td) {
    max-width: none;
    overflow: visible;
    white-space: nowrap;
  }
  .grants-frame :global(thead th) {
    background: var(--color-background-muted);
  }
  .grants-frame :global(tbody th) {
    color: var(--color-text-primary);
    font-size: var(--font-size-base);
  }
  .grants-frame :global(.c) {
    text-align: center;
  }
  .grants-frame :global(.c .astryx-checkbox-input) {
    display: flex;
    justify-content: center;
  }
  .grants-frame :global(.status) {
    min-width: 80px;
    font-size: var(--text-supporting-size);
    color: var(--color-text-secondary);
  }
  .who {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-2);
  }
  .fs {
    display: grid;
    gap: var(--spacing-1-5);
  }
  .legend {
    margin-bottom: var(--spacing-1);
    font-size: var(--text-label-size);
    line-height: var(--text-label-leading);
    font-weight: var(--font-weight-medium);
    color: var(--color-text-secondary);
  }
  .check-row {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
  }
  .check-row > :global(.astryx-field) {
    flex: 1;
    min-width: 0;
  }
  .checks {
    display: inline-flex;
    flex-wrap: wrap;
    gap: var(--spacing-1);
  }
  .cols {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--spacing-6);
  }
  .links {
    display: grid;
    gap: var(--spacing-1);
  }
  @media (max-width: 768px) {
    .two,
    .cols {
      grid-template-columns: 1fr;
    }
  }
</style>
