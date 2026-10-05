<script lang="ts">
  import { Button, Selector, Text } from '@astryx-svelte/core';
  import { untrack } from 'svelte';
  import { api } from '../../lib/api/endpoints';
  import { errorMessage } from '../../lib/api/client';
  import type { ProviderProfile } from '../../lib/api/types.gen';
  import { providerProfiles } from '../../lib/state/profiles.svelte';
  import { billingLabel } from '../../lib/util/labels';
  import Notice from '../Notice.svelte';

  let { profile, canEdit, onstate, onsaved }: {
    profile: ProviderProfile;
    canEdit: boolean;
    onsaved: (profile: ProviderProfile) => void;
    onstate: (dirty: boolean, busy: boolean) => void;
  } = $props();
  let saved = $state(untrack(() => profile));
  let limit = $state(untrack(() => String(profile.maxConcurrency)));
  let busy = $state(false);
  let error = $state('');
  let complete = $state(false);
  const dirty = $derived(Number(limit) !== saved.maxConcurrency);
  $effect(() => onstate(dirty, busy));
  $effect(() => {
    if (!busy && !dirty && profile.maxConcurrency !== saved.maxConcurrency) {
      saved = profile;
      limit = String(profile.maxConcurrency);
      complete = false;
    }
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!canEdit || !dirty || busy) return;
    busy = true;
    error = '';
    complete = false;
    try {
      saved = await api.setProviderConcurrency(saved.id, Number(limit));
      providerProfiles.replace(saved);
      onsaved(saved);
      limit = String(saved.maxConcurrency);
      complete = true;
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }
</script>

<form class="setting" aria-label="Concurrency for {saved.label}" aria-busy={busy} onsubmit={save}>
  <h3>{saved.label}</h3>
  <Text as="p" type="supporting">{saved.provider} · {billingLabel(saved.billing)} · Saved limit: {profile.maxConcurrency}</Text>
  <Selector label="Runs at once for {saved.label}" value={limit} options={Array.from({ length: 16 }, (_, i) => String(i + 1))}
    isDisabled={!canEdit || busy} width="100%" onChange={(value: string) => { limit = value; complete = false; }} />
  {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  <div class="actions">
    <Button label={busy ? 'Saving…' : 'Save limit'} type="submit" variant="primary" size="sm" isDisabled={!canEdit || !dirty || busy} />
    <Button label="Cancel" size="sm" isDisabled={!dirty || busy} onclick={() => { limit = String(saved.maxConcurrency); error = ''; complete = false; }} />
    {#if dirty}<Text type="supporting" role="status">Unsaved change</Text>
    {:else if complete}<Text type="supporting" role="status">Limit saved.</Text>{/if}
  </div>
</form>

<style>
  .setting { display: grid; gap: var(--spacing-3); padding: var(--spacing-4) 0; border-top: 1px solid var(--color-border); }
  h3 { font-size: var(--font-size-md); overflow-wrap: anywhere; }
  .actions { display: flex; align-items: center; gap: var(--spacing-2); flex-wrap: wrap; }
</style>
