<script lang="ts">
  // New engineer from an editable role starting point. No invented biography
  // or claimed experience — just a role, what they're for, and instructions.
  import { Button, FieldLabel, RadioList, RadioListItem, Text, TextArea, TextInput } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import Dialog from './Dialog.svelte';
  import ProviderSelect from './ProviderSelect.svelte';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();

  const STARTS = [
    {
      role: 'Platform engineer',
      description: 'Builds and maintains backend services and shared foundations.',
      tags: 'backend, infrastructure, reliability',
      instructions: 'Preserve established contracts. Prefer small, well-tested changes. Get an independent review for security-sensitive code.',
    },
    {
      role: 'Security engineer',
      description: 'Reviews changes for security defects and checks the evidence behind claims.',
      tags: 'security, review, auth',
      instructions: 'Review the actual revision and the surrounding code. Distinguish blocking defects from suggestions. Never approve what you could not check.',
    },
    {
      role: 'Frontend engineer',
      description: 'Works on user interfaces, accessibility and client code.',
      tags: 'ui, accessibility, typescript',
      instructions: 'Match the existing design system. Check keyboard and screen-reader behaviour. Include evidence for visual changes.',
    },
    {
      role: 'Test engineer',
      description: 'Reproduces problems and makes sure fixes stay fixed.',
      tags: 'testing, ci, regression',
      instructions: 'Reproduce the problem first. Add a regression test for every fix. Report exactly which commands ran and their results.',
    },
    {
      role: 'Reverse engineer',
      description: 'Makes unfamiliar systems understandable from their code.',
      tags: 'tracing, documentation',
      instructions: 'Map behaviour from the code itself and cite source locations. Ask only for what you genuinely cannot find.',
    },
    { role: '', description: '', tags: '', instructions: '' },
  ];

  let start = $state(0);
  let name = $state('');
  let handle = $state('');
  let handleTouched = $state(false);
  let role = $state(STARTS[0].role);
  let description = $state(STARTS[0].description);
  let tags = $state(STARTS[0].tags);
  let instructions = $state(STARTS[0].instructions);
  const readyProvider = app.data.providers.find((p) => p.readyNodes.length && (!p.fake || app.data.demo));
  let provider = $state(readyProvider?.provider ?? 'claude');
  let busy = $state(false);
  let error = $state('');
  // Input hints TextInput forwards to its <input> but doesn't type.
  const handleHints = { autocapitalize: 'none', spellcheck: false };

  function pick(i: number) {
    start = i;
    role = STARTS[i].role;
    description = STARTS[i].description;
    tags = STARTS[i].tags;
    instructions = STARTS[i].instructions;
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

<Dialog title="New engineer" description="Start from a role and edit anything. Engineers are identified as AI engineers in their profile." {onclose} width={620} purpose="form">
  <form id="new-eng" class="form" onsubmit={create} novalidate>
    <div class="starts">
      <RadioList label="Starting point" orientation="horizontal" value={String(start)} onChange={(v) => pick(Number(v))} htmlName="start">
        {#each STARTS as s, i (i)}
          <RadioListItem label={s.role || 'Blank'} value={String(i)} />
        {/each}
      </RadioList>
    </div>
    <div class="two">
      <TextInput label="Name" bind:value={name} placeholder="e.g. Ada" width="100%" hasAutoFocus />
      <!-- The mention preview sits under the field, so Name and Handle line up. -->
      <div class="handle">
        <TextInput label="Handle" {...handleHints} bind:value={handle} onChange={() => (handleTouched = true)} width="100%" />
        <Text as="p" type="supporting">Mention them as @{handle || 'handle'}</Text>
      </div>
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
  /* Six starting points wrap onto a second line rather than overflow. */
  .starts :global([role='radiogroup']) {
    flex-wrap: wrap;
    column-gap: var(--spacing-4);
    row-gap: var(--spacing-1);
  }
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
