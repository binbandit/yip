<script lang="ts">
  // Provider preference with honest readiness: which machines have it signed
  // in, and how it's billed. Provider/model is configuration, not identity.
  import { app } from '../lib/state/app.svelte';
  import { billingLabel, providerLabel } from '../lib/util/labels';

  interface Props {
    value: string;
    onchange: (v: string) => void;
    id?: string;
  }
  let { value, onchange, id }: Props = $props();

  const options = $derived(
    (app.data.providers.length ? app.data.providers : [{ provider: 'codex' }, { provider: 'claude' }, { provider: 'cursor' }].map((p) => ({ ...p, label: '', readyNodes: [], billing: 'unknown', fake: false }))).filter(
      (p) => !p.fake || app.data.demo || p.provider === value,
    ),
  );
  const current = $derived(options.find((p) => p.provider === value));
  const readiness = $derived.by(() => {
    if (!current) return 'Choose how this engineer runs.';
    const n = current.readyNodes.length;
    if (n === 0) return `${current.label || providerLabel(current.provider)} isn't signed in on any machine yet. Work will wait until a machine has it ready.`;
    const names = current.readyNodes.map((id) => app.nodeName(id) || 'a machine').join(', ');
    return `Ready on ${names} · ${billingLabel(current.billing)}${current.fake ? ' · scripted demo behaviour, no model is called' : ''}`;
  });
</script>

<div class="field">
  <select class="select" {id} {value} onchange={(e) => onchange((e.target as HTMLSelectElement).value)}>
    {#each options as p (p.provider)}
      <option value={p.provider}>{p.label || providerLabel(p.provider)}{p.readyNodes.length ? '' : ' — not ready'}</option>
    {/each}
  </select>
  <span class="hint">{readiness}</span>
</div>
