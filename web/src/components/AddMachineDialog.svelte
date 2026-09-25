<script lang="ts">
  // Pair a machine: a one-time enrollment command, the hub fingerprint to
  // verify, and when the token expires. The token is shown only once.
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Enrollment } from '../lib/api/types.gen';
  import { atTime, relative } from '../lib/util/time';
  import Dialog from './Dialog.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    onclose: () => void;
  }
  let { onclose }: Props = $props();
  let name = $state('');
  let busy = $state(false);
  let error = $state('');
  let enrollment = $state<Enrollment | null>(null);
  let copied = $state('');

  async function create(e: SubmitEvent) {
    e.preventDefault();
    if (!name.trim()) {
      error = 'Name the machine, e.g. “Studio mini”.';
      return;
    }
    busy = true;
    error = '';
    try {
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

<Dialog title={enrollment ? `Pair ${enrollment.name}` : 'Add a machine'} {onclose} width={620} dismissOnBackdrop={!enrollment}>
  {#if !enrollment}
    <form id="add-machine" class="form" onsubmit={create}>
      <p>An always-on machine keeps work running when this laptop is closed. It runs engineers with the providers signed in on it.</p>
      <label class="field">
        <span class="label">Machine name</span>
        <input class="input" bind:value={name} placeholder="e.g. Studio mini" />
      </label>
      {#if error}<p class="form-error" role="alert">{error}</p>{/if}
    </form>
  {:else}
    <div class="steps">
      <p>Run this on <strong>{enrollment.name}</strong>. It installs nothing else and pairs over a verified connection.</p>
      <div class="copy-block">
        <pre class="cmd" aria-label="Pairing command">{enrollment.command}</pre>
        <button class="btn btn-sm" onclick={() => copy(enrollment!.command, 'Command')}><Icon name="copy" size={15} />{copied === 'Command' ? 'Copied' : 'Copy command'}</button>
      </div>
      <dl class="facts">
        <div>
          <dt>Hub fingerprint</dt>
          <dd>
            <code class="fp">{enrollment.hubFingerprint}</code>
            <button class="btn btn-sm btn-quiet" onclick={() => copy(enrollment!.hubFingerprint, 'Fingerprint')}>{copied === 'Fingerprint' ? 'Copied' : 'Copy'}</button>
          </dd>
        </div>
        <div><dt>Hub address</dt><dd class="mono">{enrollment.hubUrl}</dd></div>
        <div><dt>Expires</dt><dd>{relative(enrollment.expiresAt, app.now)} ({atTime(enrollment.expiresAt)})</dd></div>
      </dl>
      <p class="notice attention">Check that the fingerprint the machine prints matches this one before confirming. This command is shown only once; if it expires, add the machine again.</p>
      <p class="meta">The machine appears in the list as soon as it connects.</p>
    </div>
  {/if}
  {#snippet footer()}
    {#if enrollment}
      <button class="btn btn-primary" onclick={onclose}>Done</button>
    {:else}
      <button class="btn" onclick={onclose}>Cancel</button>
      <button class="btn btn-primary" type="submit" form="add-machine" disabled={busy}>{busy ? 'Creating…' : 'Create pairing command'}</button>
    {/if}
  {/snippet}
</Dialog>

<style>
  .form,
  .steps {
    display: grid;
    gap: 14px;
  }
  .copy-block {
    display: grid;
    gap: 8px;
    justify-items: start;
  }
  .cmd {
    width: 100%;
    margin: 0;
    padding: 12px;
    border-radius: var(--r-artifact);
    background: var(--surface-subtle);
    border: 1px solid var(--line);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-size: 13px;
    user-select: all;
  }
  .facts {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .facts > div {
    display: grid;
    grid-template-columns: 130px minmax(0, 1fr);
    gap: 10px;
    font-size: 14px;
  }
  dt {
    color: var(--ink-secondary);
  }
  dd {
    margin: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    min-width: 0;
  }
  .fp {
    overflow-wrap: anywhere;
    font-size: 12.5px;
  }
  @media (max-width: 560px) {
    .facts > div {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
</style>
