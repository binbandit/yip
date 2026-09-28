<script lang="ts">
  // Provider preference with honest readiness: which machines have it signed
  // in, and how it's billed. Provider/model is configuration, not identity.
  import { app } from '../lib/state/app.svelte';
  import { billingLabel, providerLabel } from '../lib/util/labels';
  import { providerReadiness } from '../lib/util/providerReadiness';

  interface Props {
    value: string;
    onchange: (v: string) => void;
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
  const current = $derived(options.find((p) => p.provider === value));
  const readiness = $derived.by(() => {
    if (!current) return 'Choose how this engineer runs.';
    const { ready, reason } = current.availability;
    if (!ready.length) return `${current.label || providerLabel(current.provider)}: ${reason} Work will wait.`;
    const names = ready.map(({ node }) => node.name).join(', ');
    const billing = [...new Set(ready.map(({ provider }) => billingLabel(provider.billing)))].join(', ');
    return `Ready on ${names} · ${billing}${current.fake ? ' · scripted demo behaviour, no model is called' : ''}`;
  });
</script>

<div class="field">
  <select class="select" {id} {value} onchange={(e) => onchange((e.target as HTMLSelectElement).value)}>
    {#each options as p (p.provider)}
      <option value={p.provider}>{p.label || providerLabel(p.provider)}{p.availability.ready.length ? '' : ' - not ready'}</option>
    {/each}
  </select>
  <span class="hint">{readiness}</span>
</div>
