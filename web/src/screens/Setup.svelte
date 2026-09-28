<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import Wordmark from '../components/Wordmark.svelte';

  // `yip hub` prints a link carrying the code in the fragment; take it and
  // drop it from the address bar.
  const linkedCode = new URLSearchParams(location.hash.slice(1)).get('code') ?? '';
  if (linkedCode) history.replaceState(history.state, '', location.pathname + location.search);
  let code = $state(linkedCode);
  let orgName = $state('');
  let name = $state('');
  let handle = $state('');
  let password = $state('');
  let confirm = $state('');
  let busy = $state(false);
  let error = $state('');
  let handleTouched = $state(false);

  const suggestedHandle = $derived(
    name
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 24),
  );
  $effect(() => {
    if (!handleTouched) handle = suggestedHandle;
  });

  const problems = $derived.by(() => {
    const p: Record<string, string> = {};
    if (!code.trim()) p.code = 'Enter the one-time setup code shown where the hub is running.';
    if (!orgName.trim()) p.orgName = 'Name your workspace.';
    if (!name.trim()) p.name = 'Enter your name.';
    if (!/^[a-z0-9][a-z0-9._-]{1,31}$/.test(handle)) p.handle = 'Use 2–32 lowercase letters, numbers, dots, dashes or underscores.';
    if (password.length < 10) p.password = 'Use at least 10 characters.';
    if (confirm !== password) p.confirm = "The passwords don't match.";
    return p;
  });
  let submitted = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    submitted = true;
    if (Object.keys(problems).length) {
      error = 'Check the highlighted fields.';
      return;
    }
    busy = true;
    error = '';
    try {
      await app.completeSetup({ bootstrapSecret: code.trim(), orgName: orgName.trim(), name: name.trim(), handle, password });
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<main class="auth" aria-labelledby="setup-title">
  <div class="panel">
    <Wordmark size={30} />
    <div>
      <h1 id="setup-title">Set up your workspace</h1>
      <p class="muted intro">
        yip keeps your conversations, work records and decisions on this hub. Engineers do their work on machines you pair next. If the hub
        runs on a laptop, it stops being reachable while the laptop sleeps — an always-on machine is the better home.
      </p>
    </div>
    <form onsubmit={submit} novalidate>
      <label class="field">
        <span class="label">One-time setup code</span>
        <input class="input mono" autocomplete="one-time-code" spellcheck="false" bind:value={code} aria-invalid={submitted && !!problems.code} aria-describedby="code-hint" />
        <span class="hint" id="code-hint">{submitted && problems.code ? problems.code : 'Printed in the terminal where you started the hub.'}</span>
      </label>
      <label class="field">
        <span class="label">Workspace name</span>
        <input class="input" bind:value={orgName} placeholder="e.g. Brayden's workspace" aria-invalid={submitted && !!problems.orgName} />
        {#if submitted && problems.orgName}<span class="hint">{problems.orgName}</span>{/if}
      </label>
      <div class="row">
        <label class="field">
          <span class="label">Your name</span>
          <input class="input" autocomplete="name" bind:value={name} aria-invalid={submitted && !!problems.name} />
          {#if submitted && problems.name}<span class="hint">{problems.name}</span>{/if}
        </label>
        <label class="field">
          <span class="label">Handle</span>
          <input
            class="input"
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
            bind:value={handle}
            oninput={() => (handleTouched = true)}
            aria-invalid={submitted && !!problems.handle}
            aria-describedby="handle-hint"
          />
          <span class="hint" id="handle-hint">{submitted && problems.handle ? problems.handle : 'Engineers mention you as @' + (handle || 'handle') + '.'}</span>
        </label>
      </div>
      <div class="row">
        <label class="field">
          <span class="label">Password</span>
          <input class="input" type="password" autocomplete="new-password" bind:value={password} aria-invalid={submitted && !!problems.password} />
          {#if submitted && problems.password}<span class="hint">{problems.password}</span>{/if}
        </label>
        <label class="field">
          <span class="label">Confirm password</span>
          <input class="input" type="password" autocomplete="new-password" bind:value={confirm} aria-invalid={submitted && !!problems.confirm} />
          {#if submitted && problems.confirm}<span class="hint">{problems.confirm}</span>{/if}
        </label>
      </div>
      {#if error}<p class="form-error" role="alert">{error}</p>{/if}
      <button class="btn btn-primary submit" type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create workspace'}</button>
    </form>
  </div>
</main>

<style>
  .auth {
    min-height: 100%;
    height: 100%;
    overflow: auto;
    display: grid;
    place-items: center;
    padding: 24px 16px;
    background: var(--frame);
  }
  .panel {
    width: min(560px, 100%);
    display: grid;
    gap: 20px;
    padding: 32px;
    background: var(--surface);
    border-radius: var(--r-surface);
    box-shadow: var(--shadow-card);
  }
  h1 {
    font-family: var(--font-brand);
    font-size: 26px;
    font-weight: 600;
  }
  .intro {
    margin-top: 8px;
  }
  form {
    display: grid;
    gap: 16px;
  }
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
  }
  .submit {
    justify-self: start;
  }
  @media (max-width: 560px) {
    .row {
      grid-template-columns: 1fr;
    }
    .panel {
      padding: 24px 20px;
    }
  }
</style>
