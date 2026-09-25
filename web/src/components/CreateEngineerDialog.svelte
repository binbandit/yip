<script lang="ts">
  // New engineer from an editable role starting point. No invented biography
  // or claimed experience — just a role, what they're for, and instructions.
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

<Dialog title="New engineer" description="Start from a role and edit anything. Engineers are identified as AI engineers in their profile." {onclose} width={620}>
  <form id="new-eng" class="form" onsubmit={create}>
    <fieldset class="starts">
      <legend class="label">Starting point</legend>
      <div class="start-list">
        {#each STARTS as s, i (i)}
          <label class="start" class:on={start === i}>
            <input type="radio" name="start" checked={start === i} onchange={() => pick(i)} />
            <span>{s.role || 'Blank'}</span>
          </label>
        {/each}
      </div>
    </fieldset>
    <div class="two">
      <label class="field"><span class="label">Name</span><input class="input" bind:value={name} placeholder="e.g. Ada" /></label>
      <label class="field">
        <span class="label">Handle</span>
        <input class="input" bind:value={handle} oninput={() => (handleTouched = true)} autocapitalize="none" spellcheck="false" />
        <span class="hint">Mention them as @{handle || 'handle'}</span>
      </label>
    </div>
    <label class="field"><span class="label">Role</span><input class="input" bind:value={role} placeholder="e.g. Platform engineer" /></label>
    <label class="field"><span class="label">What they're for</span><input class="input" bind:value={description} /></label>
    <label class="field"><span class="label">Capabilities</span><input class="input" bind:value={tags} /><span class="hint">Comma-separated; colleagues use these to choose reviewers.</span></label>
    <label class="field"><span class="label">Standing instructions</span><textarea class="textarea" rows="4" bind:value={instructions}></textarea></label>
    <div class="field">
      <label class="label" for="new-eng-provider">Provider preference</label>
      <ProviderSelect id="new-eng-provider" value={provider} onchange={(v) => (provider = v)} />
    </div>
    {#if error}<p class="form-error" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn" onclick={onclose}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="new-eng" disabled={busy}>{busy ? 'Creating…' : 'Create engineer'}</button>
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: 14px;
  }
  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  .starts {
    border: 0;
    margin: 0;
    padding: 0;
  }
  .start-list {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 6px;
  }
  .start {
    position: relative;
    display: inline-flex;
    align-items: center;
    min-height: 34px;
    padding: 0 12px;
    border: 1px solid var(--line);
    border-radius: var(--r-pill);
    font-size: 14px;
    cursor: pointer;
  }
  .start input {
    position: absolute;
    opacity: 0;
    inset: 0;
    cursor: pointer;
  }
  .start.on {
    border-color: var(--accent);
    background: var(--accent-subtle);
    font-weight: 600;
  }
  .start:focus-within {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  @media (max-width: 560px) {
    .two {
      grid-template-columns: 1fr;
    }
  }
</style>
