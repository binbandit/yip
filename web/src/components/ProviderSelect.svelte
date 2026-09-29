<script lang="ts">
  // Provider preference with honest readiness: which machines have it signed
  // in, and how it's billed. Provider/model is configuration, not identity.
  import { Selector, Text } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { billingLabel, providerLabel } from '../lib/util/labels';
  import { providerReadiness } from '../lib/util/providerReadiness';

  interface Props {
    value: string;
    onchange: (v: string) => void;
    /** Lands on the trigger, so a caller's `<label for={id}>` still points at the control. */
    id?: string;
    profileId?: string;
    allowApiBilling?: boolean;
  }
  let { value, onchange, id, profileId, allowApiBilling = false }: Props = $props();

  const options = $derived(
    (app.data.providers.length ? app.data.providers : [{ provider: 'codex' }, { provider: 'claude' }, { provider: 'cursor' }].map((p) => ({ ...p, label: '', readyNodes: [], billing: 'unknown', fake: false }))).filter(
      (p) => !p.fake || app.data.demo || p.provider === value,
    ).map((p) => ({ ...p, availability: providerReadiness(Object.values(app.data.nodes), { provider: p.provider, profileId: p.provider === value ? profileId : undefined, allowApiBilling }) })),
  );
  const choices = $derived(
    options.map((p) => ({ value: p.provider, label: `${p.label || providerLabel(p.provider)}${p.availability.ready.length ? '' : ' - not ready'}` })),
  );
  const current = $derived(options.find((p) => p.provider === value));
  const readiness = $derived.by(() => {
    if (!current) return 'Choose how this engineer runs.';
    const { ready, reason } = current.availability;
    if (!ready.length) return `${current.label || providerLabel(current.provider)}: ${reason} Work will wait.`;
    const names = ready.map(({ node }) => node.name).join(', ');
    const billing = [...new Set(ready.map(({ provider }) => billingLabel(provider.billing)))].join(', ');
    return `Ready on ${names} · ${billing}${current.fake ? ' · scripted demo behaviour, no model is called' : ''}`;
  });
  const idAttr = $derived(id ? { id } : {});
</script>

<div class="provider">
  <Selector label="Provider preference" isLabelHidden width="100%" options={choices} {value} onChange={(v: string) => onchange(v)} {...idAttr} />
  <Text as="p" display="block" type="supporting" class="hint">{readiness}</Text>
</div>

<style>
  .provider {
    display: grid;
    gap: var(--spacing-1-5);
    min-width: 0;
  }
</style>
