<script lang="ts">
  import { onDestroy } from 'svelte';
  import { Button, CheckboxInput, Selector, Text, TextArea } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { del, errorMessage, get, post } from '../lib/api/client';
  import type { EngineerDraft, EngineerDraftFields } from '../lib/api/types.gen';
  import { billingLabel, providerLabel } from '../lib/util/labels';
  import { workspaceUrl } from '../lib/workspace';
  import Notice from './Notice.svelte';

  interface Props {
    disabled?: boolean;
    fields: EngineerDraftFields;
    onapply: (fields: EngineerDraftFields, before: EngineerDraftFields) => void;
    onbusy: (busy: boolean) => void;
  }
  let { fields, onapply, onbusy, disabled = false }: Props = $props();
  // Keep cancellation bound to the workspace where the request began.
  const endpoint = new URL(workspaceUrl('/v1/engineer-drafts'), window.location.origin).href;
  let description = $state('');
  let selection = $state('');
  let model = $state('');
  let allowApiBilling = $state(false);
  let busy = $state(false);
  let stopping = $state(false);
  let status = $state('');
  let error = $state('');
  let activeID = '';
  let generation = 0;
  let destroyed = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const choices = $derived(Object.values(app.data.nodes).filter((n) => !n.revokedAt && n.status !== 'revoked').flatMap((node) =>
    node.providers.map((provider) => ({ node, provider, value: `${node.id}/${provider.provider}/${provider.profileId}` }))));
  const selected = $derived(choices.find((c) => c.value === selection));
  const unavailable = $derived(!selected ? 'Choose a machine and provider.'
    : selected.node.status !== 'online' || selected.node.draining || selected.node.capacity.diskPressure ? 'This machine is unavailable for new work.'
      : selected.provider.authState !== 'ready' ? 'This provider is not configured on this machine. Check Connections.'
        : !selected.provider.capabilities.readOnly || !selected.provider.capabilities.engineerDrafts ? 'This provider cannot enforce tool-free drafting. Choose another provider or edit the fields yourself.'
          : !selected.node.profiles.some((p) => p.name === 'native' && p.available && p.engineerDrafts) ? 'Drafting is unavailable for this runner’s execution profile.'
            : selected.provider.billing === 'api' && !allowApiBilling ? 'Allow this provider’s API billing before generating.' : '');

  function setBusy(value: boolean) { busy = value; onbusy(value); }
  function choose(value: string) { selection = value; model = ''; allowApiBilling = false; }
  function current(token: number) { return !destroyed && token === generation; }
  function isActive(draft: EngineerDraft) { return ['offered', 'preparing', 'running', 'stopping'].includes(draft.state); }

  async function poll(id: string, token: number, before: EngineerDraftFields) {
    try {
      const draft = await get<EngineerDraft>(`${endpoint}/${id}`);
      if (!current(token)) return;
      error = '';
      status = draft.detail;
      if (isActive(draft)) {
        timer = setTimeout(() => void poll(id, token, before), 500);
        return;
      }
      activeID = '';
      setBusy(false);
      if (!stopping && draft.state === 'succeeded' && draft.fields) onapply(draft.fields, before);
      else if (stopping && draft.state === 'succeeded') status = 'Draft finished. Its result was discarded; your editable fields are unchanged.';
      else if (draft.state !== 'cancelled') error = draft.detail;
      stopping = false;
    } catch (err) {
      if (!current(token)) return;
      error = `${errorMessage(err)} The draft may still be running; checking again.`;
      timer = setTimeout(() => void poll(id, token, before), 2000);
    }
  }

  async function generate() {
    if (disabled || busy || !selected || unavailable || !description.trim()) return;
    const before = { ...fields, capabilityTags: [...fields.capabilityTags] };
    const token = ++generation;
    error = ''; status = 'Starting draft…'; stopping = false;
    setBusy(true);
    try {
      const draft = await post<EngineerDraft>(endpoint, { description: description.trim(), nodeId: selected.node.id,
        provider: { provider: selected.provider.provider, profileId: selected.provider.profileId, model, allowApiBilling } });
      if (!current(token)) { await del(`${endpoint}/${draft.id}`); return; }
      activeID = draft.id;
      if (stopping) await requestStop(draft.id, token);
      await poll(draft.id, token, before);
    } catch (err) {
      if (!current(token)) return;
      error = errorMessage(err); status = ''; stopping = false; setBusy(false);
    }
  }

  async function cancel() {
    stopping = true;
    status = 'Stopping draft…';
    if (!activeID) return; // A pending create is cancelled as soon as it returns.
    await requestStop(activeID, generation);
  }

  async function requestStop(id: string, token: number) {
    try { await del(`${endpoint}/${id}`); }
    catch (err) {
      if (current(token)) error = `${errorMessage(err)} Cancellation is not confirmed. Checking the draft; its result will be discarded.`;
    }
  }

  onDestroy(() => {
    destroyed = true; generation++;
    clearTimeout(timer);
    if (activeID) void del(`${endpoint}/${activeID}`).catch(() => {});
  });
</script>

<details class="draft">
  <summary>Draft from a description</summary>
  <div class="fields">
    <Text as="p" type="supporting">Describe the engineer you need. Your selected provider will suggest editable fields. Review them and add a name before creating.</Text>
    <TextArea label="Describe the engineer" bind:value={description} rows={3} width="100%" isDisabled={busy || disabled} {...{ maxlength: 4000 }} />
    <Selector label="Draft on" width="100%" value={selection} onChange={choose} isDisabled={busy || disabled}
      options={[{ value: '', label: 'Choose machine and provider' }, ...choices.map((c) => ({ value: c.value,
        label: `${c.node.name} · ${providerLabel(c.provider.provider)} · ${c.provider.account || c.provider.profileId}${c.node.status !== 'online' ? ' · offline' : ''}` }))]} />
    {#if selected}
      <Text as="p" type="supporting">{selected.provider.authDetail} {billingLabel(selected.provider.billing)}. Uses this machine’s existing provider configuration.</Text>
      {#if (selected.provider.models ?? []).length}
        <Selector label="Draft model" width="100%" value={model} onChange={(value: string) => (model = value)} isDisabled={busy || disabled}
          options={[{ value: '', label: 'Configured default model' }, ...(selected.provider.models ?? []).map((m) => ({ value: m.id, label: m.label || m.id }))]} />
      {/if}
      {#if selected.provider.billing === 'api'}
        <CheckboxInput label="Allow API billing for this draft" value={allowApiBilling} onChange={(value: boolean) => (allowApiBilling = value)} isDisabled={busy || disabled} />
      {/if}
    {/if}
    {#if unavailable}<Text as="p" type="supporting">{unavailable}</Text>{/if}
    <div class="actions">
      <Button label={busy ? 'Drafting…' : 'Generate draft'} onclick={generate} isDisabled={disabled || busy || !!unavailable || !description.trim()} />
      {#if busy}<Button label={stopping ? 'Stopping…' : 'Stop drafting'} onclick={cancel} isDisabled={stopping} />{/if}
    </div>
    {#if status}<Text as="p" type="supporting" role="status">{status}</Text>{/if}
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  </div>
</details>

<style>
  .draft { border: 1px solid var(--color-border); border-radius: var(--radius-element); padding: var(--spacing-3); }
  summary { cursor: pointer; font-weight: 600; }
  .fields { display: grid; gap: var(--spacing-3); padding-top: var(--spacing-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--spacing-2); }
</style>
