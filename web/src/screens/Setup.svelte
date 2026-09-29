<script lang="ts">
  import { Button, Card, Center, Heading, Text, TextInput, VStack } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import Wordmark from '../components/Wordmark.svelte';

  // TextInput forwards unknown attributes to its <input> but types only the
  // generic HTML ones, so input-specific hints go in through spreads.
  const codeHints = { autocomplete: 'one-time-code', spellcheck: false };
  const nameHints = { autocomplete: 'name' };
  const handleHints = { autocomplete: 'username', autocapitalize: 'none', spellcheck: false };
  const passwordHints = { autocomplete: 'new-password' };

  let code = $state('');
  let orgName = $state('');
  let name = $state('');
  let handle = $state('');
  let password = $state('');
  let confirm = $state('');
  let busy = $state(false);
  let error = $state('');
  let handleTouched = $state(false);

  const suggestedHandle = $derived(
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 24),
  );
  $effect(() => {
    if (!handleTouched) handle = suggestedHandle;
  });

  const problems = $derived.by(() => {
    const p: Record<string, string> = {};
    if (!code.trim()) p.code = 'Enter the one-time setup code shown where the hub is running.';
    if (!orgName.trim()) p.orgName = 'Name your workspace.';
    if (!name.trim()) p.name = 'Enter your name.';
    if (!/^[a-z0-9][a-z0-9._-]{1,31}$/.test(handle)) p.handle = 'Use 2–32 lowercase letters, numbers, dots, dashes or underscores.';
    if (password.length < 10) p.password = 'Use at least 10 characters.';
    if (confirm !== password) p.confirm = "The passwords don't match.";
    return p;
  });
  let submitted = $state(false);
  const status = (field: string) => (submitted && problems[field] ? { type: 'error' as const, message: problems[field] } : undefined);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    submitted = true;
    if (Object.keys(problems).length) {
      error = 'Check the highlighted fields.';
      return;
    }
    busy = true;
    error = '';
    try {
      await app.completeSetup({ bootstrapSecret: code.trim(), orgName: orgName.trim(), name: name.trim(), handle, password });
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<main class="auth yip-frame" aria-labelledby="setup-title">
  <Center minHeight="100%" padding={4}>
    <Card width="min(560px, 100%)" padding={8} elevation="low">
      <VStack gap={5}>
        <Wordmark size={30} />
        <VStack gap={2}>
          <Heading level={1} id="setup-title">Set up your workspace</Heading>
          <Text as="p" display="block" color="secondary">
            yip keeps your conversations, work records and decisions on this hub. Engineers do their work on machines you pair next. If the hub
            runs on a laptop, it stops being reachable while the laptop sleeps — an always-on machine is the better home.
          </Text>
        </VStack>
        <form onsubmit={submit} novalidate>
          <TextInput
            label="One-time setup code"
            class="code"
            {...codeHints}
            width="100%"
            bind:value={code}
            description={status('code') ? undefined : 'Printed in the terminal where you started the hub.'}
            status={status('code')}
          />
          <TextInput
            label="Workspace name"
            width="100%"
            placeholder="e.g. Brayden's workspace"
            bind:value={orgName}
            status={status('orgName')}
          />
          <!-- Name and handle stack: the handle's hint sits above its input, which would misalign a pair. -->
          <TextInput label="Your name" {...nameHints} width="100%" bind:value={name} status={status('name')} />
          <TextInput
            label="Handle"
            {...handleHints}
            width="100%"
            bind:value={handle}
            onChange={() => (handleTouched = true)}
            description={status('handle') ? undefined : 'Engineers mention you as @' + (handle || 'handle') + '.'}
            status={status('handle')}
          />
          <div class="row">
            <TextInput label="Password" type="password" {...passwordHints} width="100%" bind:value={password} status={status('password')} />
            <TextInput label="Confirm password" type="password" {...passwordHints} width="100%" bind:value={confirm} status={status('confirm')} />
          </div>
          {#if error}
            <Notice tone="danger" role="alert">{error}</Notice>
          {/if}
          <div>
            <Button label="Create workspace" variant="primary" type="submit" isLoading={busy} />
          </div>
        </form>
      </VStack>
    </Card>
  </Center>
</main>

<style>
  .auth {
    height: 100%;
    overflow: auto;
  }
  form {
    display: grid;
    gap: var(--spacing-4);
  }
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--spacing-4);
    align-items: start;
  }
  form :global(.code input) {
    font-family: var(--font-family-code);
  }
  @media (max-width: 560px) {
    .row {
      grid-template-columns: 1fr;
    }
    .auth :global(.astryx-card) {
      padding: var(--spacing-6) var(--spacing-5);
    }
  }
</style>
