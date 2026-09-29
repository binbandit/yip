<script lang="ts">
  // Pair a machine: a one-time enrollment command, the hub fingerprint to
  // verify, and when the token expires. The token is shown only once.
  import { Button, Code, CodeBlock, Icon, MetadataList, MetadataListItem, Text, TextInput } from '@astryx-svelte/core';
  import { Copy } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Enrollment } from '../lib/api/types.gen';
  import { atTime, relative } from '../lib/util/time';
  import { authStateLabel, billingLabel, providerLabel } from '../lib/util/labels';
  import { SIGN_IN_COMMANDS } from '../lib/util/machines';
  import Dialog from './Dialog.svelte';
  import Notice from './Notice.svelte';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();
  let name = $state('');
  let busy = $state(false);
  let error = $state('');
  let enrollment = $state<Enrollment | null>(null);
  let copied = $state('');
  // Machines already here when the command was made; a new one is the pairing.
  let known = new Set<string>();
  const paired = $derived.by(() => {
    if (!enrollment) return undefined;
    const fresh = Object.values(app.data.nodes).filter((n) => !known.has(n.id));
    return fresh.find((n) => n.name === enrollment!.name) ?? fresh[0];
  });
  const SIGN_IN = SIGN_IN_COMMANDS;
  const realProviders = $derived((paired?.providers ?? []).filter((p) => p.provider !== 'fake' && p.authState !== 'not_installed'));

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!name.trim()) {
      error = 'Name the machine, e.g. “Studio mini”.';
      return;
    }
    busy = true;
    error = '';
    try {
      known = new Set(Object.keys(app.data.nodes));
      enrollment = await api.createEnrollment({ name: name.trim() });
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = what;
      app.announce(`${what} copied.`);
      setTimeout(() => (copied = ''), 2000);
    } catch {
      app.toast("Couldn't copy — select the text and copy it manually.", 'error');
    }
  }
</script>

<!-- The pairing command is shown only once, so a stray backdrop click must not discard it. -->
<Dialog title={enrollment ? `Pair ${enrollment.name}` : 'Add a machine'} {onclose} width={620} purpose="form">
  {#if !enrollment}
    <form id="add-machine" class="form" onsubmit={create} novalidate>
      <p>An always-on machine keeps work running when this laptop is closed. It runs engineers with the providers signed in on it.</p>
      <TextInput label="Machine name" bind:value={name} placeholder="e.g. Studio mini" width="100%" hasAutoFocus />
      {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
    </form>
  {:else}
    <div class="steps">
      <p>Run this on <strong>{enrollment.name}</strong>. It installs nothing else and pairs over a verified connection.</p>
      <div class="copy-block">
        <CodeBlock code={enrollment.command} hasCopyButton={false} size="sm" width="100%" isWrapped aria-label="Pairing command" />
        <Button label={copied === 'Command' ? 'Copied' : 'Copy command'} size="sm" onclick={() => copy(enrollment!.command, 'Command')}>
          {#snippet icon()}<Icon icon={Copy} size="sm" />{/snippet}
        </Button>
      </div>
      <div class="facts">
        <MetadataList>
          <MetadataListItem label="Hub fingerprint">
            <span class="fp">
              <Code>{enrollment.hubFingerprint}</Code>
              <Button label="Copy fingerprint" size="sm" variant="ghost" onclick={() => copy(enrollment!.hubFingerprint, 'Fingerprint')}>
                {#snippet icon()}<Icon icon={Copy} size="sm" />{/snippet}
                {copied === 'Fingerprint' ? 'Copied' : 'Copy'}
              </Button>
            </span>
          </MetadataListItem>
          <MetadataListItem label="Hub address"><Code>{enrollment.hubUrl}</Code></MetadataListItem>
          <MetadataListItem label="Expires">{relative(enrollment.expiresAt, app.now)} ({atTime(enrollment.expiresAt)})</MetadataListItem>
        </MetadataList>
      </div>
      <Notice
        tone="warning"
        title="Check that the fingerprint the machine prints matches this one before confirming."
        description="This command is shown only once; if it expires, add the machine again."
      />
      <p>
        Then start it with <Code size="inherit">yip runner</Code>. Started from a terminal, it stops when that terminal closes; to keep it running, install it as a background service with
        <Code size="inherit">yip service install runner</Code> and check it comes back after a restart.
      </p>
      <div class="watch" role="status">
        {#if !paired}
          <StateIcon shape="circle" size={16} />
          <span>Waiting for {enrollment.name} to connect… this updates by itself.</span>
        {:else}
          <StateIcon shape="check-filled" tone="success" size={16} />
          <div class="paired">
            <strong>{paired.name} is paired{paired.status === 'online' ? ' and connected' : ''}.</strong>
            {#if realProviders.length === 0}
              <Text type="supporting">No Codex, Claude Code or Cursor found on it yet. Install one and sign in with its own tool; yip picks it up on the next check.</Text>
            {:else}
              <ul>
                {#each realProviders as p (p.provider)}
                  <li>
                    {providerLabel(p.provider)} · {authStateLabel(p.authState, p.authDetail)}{#if p.authState === 'ready'}{' · '}{billingLabel(p.billing)}{#if p.account}{' · '}{p.account}{/if}
                    {:else if SIGN_IN[p.provider]}{' · run '}<Code size="inherit">{SIGN_IN[p.provider]}</Code>{' on it'}{/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  {/if}
  {#snippet footer()}
    {#if enrollment}
      <Button label={paired ? 'Done' : 'Close'} variant="primary" onclick={onclose} />
    {:else}
      <Button label="Cancel" onclick={onclose} />
      <Button label={busy ? 'Creating…' : 'Create pairing command'} variant="primary" type="submit" form="add-machine" isLoading={busy} />
    {/if}
  {/snippet}
</Dialog>

<style>
  .form,
  .steps {
    display: grid;
    gap: var(--spacing-4);
  }
  .copy-block {
    display: grid;
    gap: var(--spacing-2);
    justify-items: start;
  }
  /* One click selects the whole command, for when copying to the clipboard fails. */
  .copy-block :global(code) {
    user-select: all;
  }
  .facts :global(dl) {
    grid-template-columns: 130px minmax(0, 1fr);
    row-gap: var(--spacing-1-5);
  }
  .facts :global(dt) {
    font-weight: var(--font-weight-normal);
  }
  .facts :global(dd) {
    min-width: 0;
  }
  /* A long fingerprint wraps beside its Copy button rather than pushing it to a line of its own. */
  .fp {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: var(--spacing-2);
    min-width: 0;
  }
  .fp :global(code) {
    justify-self: start;
    margin-top: var(--spacing-0-5);
  }
  .fp :global(code),
  .facts :global(code) {
    overflow-wrap: anywhere;
  }
  .watch {
    display: flex;
    gap: var(--spacing-2);
    align-items: flex-start;
    padding: var(--spacing-2) var(--spacing-3);
    border-radius: var(--radius-element);
    background: var(--color-background-muted);
  }
  .watch > :global(svg) {
    margin-top: 2px;
  }
  .paired {
    display: grid;
    gap: var(--spacing-1);
  }
  .paired ul {
    margin: 0;
    padding-left: var(--spacing-4);
  }
  @media (max-width: 560px) {
    .facts :global(dl) {
      grid-template-columns: minmax(0, 1fr);
      row-gap: 0;
    }
    .facts :global(dd:not(:last-child)) {
      margin-bottom: var(--spacing-1-5);
    }
  }
</style>
