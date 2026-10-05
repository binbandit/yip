<script lang="ts">
  // New engineer from an editable role starting point. No invented biography
  // or claimed experience — just a role, what they're for, and instructions.
  import { Button, FieldLabel, Selector, Text, TextArea, TextInput } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import { engineerPresets } from '../lib/engineer-presets';
  import Dialog from './Dialog.svelte';
  import ProviderSelect from './ProviderSelect.svelte';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();

  let preset = $state(engineerPresets[0].id);
  let name = $state('');
  let handle = $state('');
  let handleTouched = $state(false);
  let role = $state(engineerPresets[0].role);
  let description = $state(engineerPresets[0].description);
  let tags = $state(engineerPresets[0].tags);
  let instructions = $state(engineerPresets[0].instructions);
  const readyProvider = app.data.providers.find((p) => p.readyNodes.length);
  let provider = $state(readyProvider?.provider ?? 'claude');
  let busy = $state(false);
  let error = $state('');
  // Input hints TextInput forwards to its <input> but doesn't type.
  const handleHints = { autocapitalize: 'none', spellcheck: false };

  function pickPreset(id: string) {
    const selected = engineerPresets.find((p) => p.id === id);
    if (!selected) return;
    const previous = engineerPresets.find((p) => p.id === preset)!;
    if (role === previous.role) role = selected.role;
    if (description === previous.description) description = selected.description;
    if (tags === previous.tags) tags = selected.tags;
    if (instructions === previous.instructions) instructions = selected.instructions;
    preset = id;
  }

  const selectedPreset = $derived(engineerPresets.find((p) => p.id === preset)!);
  const fieldsEdited = $derived(role !== selectedPreset.role || description !== selectedPreset.description || tags !== selectedPreset.tags || instructions !== selectedPreset.instructions);

  function resetPreset() {
    role = selectedPreset.role;
    description = selectedPreset.description;
    tags = selectedPreset.tags;
    instructions = selectedPreset.instructions;
  }

  $effect(() => {
    if (!handleTouched)
      handle = name
        .trim()
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '');
  });

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!name.trim() || !role.trim()) {
      error = 'Give the engineer a name and a role.';
      return;
    }
    busy = true;
    error = '';
    try {
      const eng = await api.createEngineer({
        name: name.trim(),
        handle: handle.trim(),
        role: role.trim(),
        description: description.trim(),
        instructions: instructions.trim(),
        capabilityTags: tags
          .split(',')
          .map((t) => t.trim())
          .filter(Boolean),
        provider: { provider },
      });
      app.data.engineers[eng.id] = eng;
      onclose();
      app.go({ name: 'engineer', id: eng.id });
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<Dialog title="New engineer" description="Choose a premade engineer, give them a name and edit any detail. Engineers are identified as AI engineers in their profile." {onclose} width={620} purpose="form">
  <form id="new-eng" class="form" onsubmit={create} novalidate>
    <div class="two">
      <TextInput label="Name" bind:value={name} placeholder="e.g. Ada" width="100%" hasAutoFocus />
      <!-- The mention preview sits under the field, so Name and Handle line up. -->
      <div class="handle">
        <TextInput label="Handle" {...handleHints} bind:value={handle} onChange={() => (handleTouched = true)} width="100%" />
        <Text as="p" type="supporting">Mention them as @{handle || 'handle'}</Text>
      </div>
    </div>
    <div class="presets">
      <Selector label="Starting point" description="Presets fill the details below. Your edits are kept when you switch." width="100%" options={engineerPresets.map((p) => ({ value: p.id, label: p.label }))} value={preset} onChange={pickPreset} />
      <Text as="p" type="supporting">{selectedPreset.summary}</Text>
      {#if fieldsEdited}
        <div><Button label={preset === 'custom' ? 'Clear custom fields' : 'Reset to preset'} onclick={resetPreset} /></div>
      {/if}
    </div>
    <TextInput label="Role" bind:value={role} placeholder="e.g. Platform engineer" width="100%" />
    <TextInput label="What they're for" bind:value={description} width="100%" />
    <TextInput label="Capabilities" bind:value={tags} description="Comma-separated; colleagues use these to choose reviewers." width="100%" />
    <TextArea label="Standing instructions" rows={4} bind:value={instructions} width="100%" />
    <div class="provider">
      <FieldLabel label="Provider preference" inputID="new-eng-provider" />
      <ProviderSelect id="new-eng-provider" value={provider} onchange={(v) => (provider = v)} />
    </div>
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  </form>
  {#snippet footer()}
    <Button label="Cancel" onclick={onclose} />
    <Button label={busy ? 'Creating…' : 'Create engineer'} variant="primary" type="submit" form="new-eng" isLoading={busy} />
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: var(--spacing-4);
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--spacing-3);
    align-items: start;
  }
  .presets,
  .handle,
  .provider {
    display: grid;
    gap: var(--spacing-1-5);
  }
  @media (max-width: 560px) {
    .two {
      grid-template-columns: 1fr;
    }
  }
</style>
