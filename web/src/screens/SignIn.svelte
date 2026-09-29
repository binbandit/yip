<script lang="ts">
  import { Button, Card, Center, Heading, Text, TextInput, VStack } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import Wordmark from '../components/Wordmark.svelte';

  // TextInput forwards unknown attributes to its <input> but types only the
  // generic HTML ones, so input-specific hints for password managers and
  // mobile keyboards go in through a spread.
  const handleHints = { autocomplete: 'username', autocapitalize: 'none', spellcheck: false };
  const passwordHints = { autocomplete: 'current-password' };

  let handle = $state('');
  let password = $state('');
  let busy = $state(false);
  let error = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!handle.trim() || !password) {
      error = 'Enter your handle and password.';
      return;
    }
    busy = true;
    error = '';
    try {
      await app.signIn(handle.trim().replace(/^@/, ''), password);
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<main class="auth yip-frame" aria-labelledby="signin-title">
  <Center minHeight="100%" padding={4}>
    <Card width="min(400px, 100%)" padding={8} elevation="low">
      <VStack gap={5}>
        <Wordmark size={30} />
        <Heading level={1} id="signin-title">Sign in to {app.setupOrgName || 'your workspace'}</Heading>
        <form onsubmit={submit} novalidate>
          <VStack gap={4} align="start">
            <TextInput
              label="Handle"
              htmlName="handle"
              {...handleHints}
              width="100%"
              hasAutoFocus
              bind:value={handle}
              status={error && !handle.trim() ? { type: 'error' } : undefined}
            />
            <TextInput
              label="Password"
              type="password"
              htmlName="password"
              {...passwordHints}
              width="100%"
              bind:value={password}
              status={error && !password ? { type: 'error' } : undefined}
            />
            {#if error}
              <div class="error"><Notice tone="danger" role="alert">{error}</Notice></div>
            {/if}
            <Button label={busy ? 'Signing in…' : 'Sign in'} variant="primary" type="submit" isLoading={busy} />
          </VStack>
        </form>
        <Text type="supporting" as="p">Your workspace runs on your own hub.</Text>
      </VStack>
    </Card>
  </Center>
</main>

<style>
  .auth {
    height: 100%;
    overflow: auto;
  }
  .error {
    align-self: stretch;
  }
</style>
