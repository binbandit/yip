<script lang="ts">
  import { Button, Link, Switch, Text } from '@astryx-svelte/core';
  import { untrack } from 'svelte';
  import { api } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import type { Engineer } from '../../lib/api/types.gen';
  import { newer } from '../../lib/state/data';
  import { app } from '../../lib/state/app.svelte';
  import { workspaceUrl } from '../../lib/workspace';
  import Notice from '../Notice.svelte';

  let { engineer, canEdit, onstate }: {
    engineer: Engineer;
    canEdit: boolean;
    onstate: (dirty: boolean, busy: boolean) => void;
  } = $props();
  let saved = $state(untrack(() => engineer));
  let allowed = $state(untrack(() => !!engineer.provider.allowApiBilling));
  let busy = $state(false);
  let error = $state('');
  let conflict = $state(false);
  let complete = $state(false);
  const dirty = $derived(allowed !== !!saved.provider.allowApiBilling);
  $effect(() => onstate(dirty, busy));
  $effect(() => {
    if (!busy && !dirty && engineer.version > saved.version) {
      saved = engineer;
      allowed = !!engineer.provider.allowApiBilling;
      complete = false;
    }
  });

  function cancel() {
    allowed = !!saved.provider.allowApiBilling;
    error = '';
    conflict = false;
    complete = false;
  }

  async function reload() {
    busy = true;
    error = '';
    try {
      saved = (await api.engineer(saved.id)).engineer;
      if (newer(app.data.engineers[saved.id], saved)) app.data.engineers[saved.id] = saved;
      cancel();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!canEdit || !dirty || busy || conflict) return;
    busy = true;
    error = '';
    complete = false;
    try {
      saved = await api.updateEngineer(saved.id, {
        version: saved.version,
        provider: { ...saved.provider, allowApiBilling: allowed },
      });
      if (newer(app.data.engineers[saved.id], saved)) app.data.engineers[saved.id] = saved;
      allowed = !!saved.provider.allowApiBilling;
      complete = true;
    } catch (err) {
      error = errorMessage(err);
      conflict = err instanceof ApiError && err.status === 409;
    } finally {
      busy = false;
    }
  }
</script>

<form class="setting" id="billing-{saved.id}" aria-label="API billing for {saved.name}" aria-busy={busy} onsubmit={save}>
  <div class="heading">
    <h3><Link hasUnderline href={workspaceUrl(`/engineers/${saved.id}`)}>{saved.name}</Link></h3>
    <Text type="supporting">{saved.provider.provider}{saved.archived ? ' · Archived' : ''}</Text>
  </div>
  <Switch label="Allow API-billed runs for {saved.name}" value={allowed} isDisabled={!canEdit || busy} onChange={(value) => { allowed = value; complete = false; }}
    description="Permits this engineer to use API-billed accounts for their chosen provider and configured alternatives." />
  <Text as="p" type="supporting">Saved permission: {engineer.provider.allowApiBilling ? 'API billing allowed' : 'API billing blocked'}.</Text>
  {#if dirty && allowed}<Notice>Saving may let queued work start using paid API accounts. Usage is charged by the provider separately from a subscription. This is not a spending limit.</Notice>{/if}
  {#if dirty && !allowed}<Notice>Future runs will avoid accounts known to use API billing. Runs already in progress keep the permission they started with.</Notice>{/if}
  {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  <div class="actions">
    <Button label={busy ? 'Saving…' : 'Save permission'} type="submit" variant="primary" size="sm" isDisabled={!canEdit || !dirty || busy || conflict} />
    <Button label="Cancel" size="sm" onclick={cancel} isDisabled={!dirty || busy} />
    {#if conflict}<Button label="Reload saved permission" size="sm" onclick={reload} isDisabled={busy} />{/if}
    {#if dirty}<Text type="supporting" role="status">Unsaved change</Text>
    {:else if complete}<Text type="supporting" role="status">Permission saved.</Text>{/if}
  </div>
</form>

<style>
  .setting { display: grid; gap: var(--spacing-3); padding: var(--spacing-4) 0; border-top: 1px solid var(--color-border); scroll-margin-top: var(--spacing-4); }
  .heading { display: flex; align-items: baseline; justify-content: space-between; gap: var(--spacing-2); flex-wrap: wrap; }
  h3 { font-size: var(--font-size-md); overflow-wrap: anywhere; }
  .actions { display: flex; align-items: center; gap: var(--spacing-2); flex-wrap: wrap; }
</style>
