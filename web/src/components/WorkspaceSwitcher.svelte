<script lang="ts">
  import { Button, DropdownMenu, Icon, Text, TextInput } from '@astryx-svelte/core';
  import { Check, Plus, RefreshCw } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import Dialog from './Dialog.svelte';
  import Notice from './Notice.svelte';

  let { compact = false }: { compact?: boolean } = $props();

  const current = $derived(app.workspaces.find((workspace) => workspace.path === app.currentWorkspacePath));
  const activeName = $derived(app.data.org?.name || current?.name || 'Workspace');
  // The current workspace remains visible and usable even when discovery fails.
  const workspaces = $derived(current ? app.workspaces : [
    { id: 'current', name: activeName, path: app.currentWorkspacePath },
    ...app.workspaces,
  ]);

  let creating = $state(false);
  let name = $state('');
  let busy = $state(false);
  let error = $state('');
  const id = $props.id();
  const formId = `${id}-create-workspace`;
  const nameHints = { maxlength: 80 };

  function openCreate() {
    name = '';
    error = '';
    creating = true;
  }

  function closeCreate() {
    if (!busy) creating = false;
  }

  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    error = '';
    const trimmed = name.trim();
    if (!trimmed) {
      error = 'Enter a workspace name.';
      return;
    }
    if (trimmed.length > 80) {
      error = 'Use 80 characters or fewer.';
      return;
    }
    busy = true;
    try {
      await app.addWorkspace(trimmed);
      creating = false;
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

{#snippet identity()}
  <span class="identity">
    <span class="initial" aria-hidden="true">{Array.from(activeName.trim())[0]?.toLocaleUpperCase() || 'W'}</span>
    <span class="name" title={activeName}>{activeName}</span>
  </span>
{/snippet}
{#snippet checkIcon()}<Icon icon={Check} size="sm" />{/snippet}
{#snippet plusIcon()}<Icon icon={Plus} size="sm" />{/snippet}
{#snippet retryIcon()}<Icon icon={RefreshCw} size="sm" />{/snippet}

<div class="switcher" class:compact>
  <DropdownMenu
    button={{ label: `Workspace: ${activeName}`, variant: 'ghost', size: 'lg', width: '100%', children: identity }}
    alignment="start"
    menuWidth="min(320px, calc(100vw - 24px))"
    onOpenChange={(open) => { if (open) void app.refreshWorkspaces(); }}
    items={[
      ...workspaces.map((workspace) => ({
        id: workspace.id,
        label: workspace.name,
        description: workspace.path === app.currentWorkspacePath ? 'Current workspace' : undefined,
        endContent: workspace.path === app.currentWorkspacePath ? checkIcon : undefined,
        onClick: () => {
          if (workspace.path !== app.currentWorkspacePath) app.switchWorkspace(workspace);
        },
      })),
      ...(app.workspacesLoading ? [{ label: 'Loading workspaces…', isDisabled: true }] : []),
      ...(app.workspacesError ? [{
        label: app.workspacesLoading ? 'Retrying…' : 'Retry loading workspaces',
        description: app.workspacesError,
        icon: retryIcon,
        isDisabled: app.workspacesLoading,
        hasCloseOnSelect: false,
        onClick: () => void app.refreshWorkspaces(),
      }] : []),
      { type: 'divider' },
      { label: 'Create workspace', icon: plusIcon, onClick: openCreate },
    ]}
  />
</div>

{#if creating}
  <Dialog title="Create workspace" onclose={closeCreate} width={480} purpose={busy ? 'required' : 'form'}>
    <form id={formId} class="form" onsubmit={create} novalidate aria-busy={busy}>
      <TextInput label="Workspace name" bind:value={name} placeholder="e.g. Design studio" description="Choose a name for any team, project, or purpose." {...nameHints} isRequired isReadOnly={busy} hasAutoFocus width="100%" />
      <Text as="p" type="supporting">Chats, rooms, engineers, projects and machine pairings stay separate. Signing in is shared.</Text>
      <Text as="p" type="supporting">Your new workspace starts empty. Pair a machine there to get started.</Text>
      {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
    </form>
    {#snippet footer()}
      <Button label="Cancel" onclick={closeCreate} isDisabled={busy} />
      <Button label={busy ? 'Creating…' : 'Create workspace'} variant="primary" type="submit" form={formId} isLoading={busy} />
    {/snippet}
  </Dialog>
{/if}

<style>
  .switcher {
    width: 100%;
    min-width: 0;
  }
  .compact {
    width: min(220px, calc(100vw - 120px));
  }
  .identity {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    width: 100%;
    min-width: 0;
    text-align: start;
  }
  .initial {
    display: grid;
    place-items: center;
    flex: none;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-inner);
    background: var(--color-overlay-pressed);
    color: var(--color-text-primary);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
  }
  .form {
    display: grid;
    gap: var(--spacing-4);
  }
</style>
