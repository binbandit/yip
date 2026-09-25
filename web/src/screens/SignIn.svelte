<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import Wordmark from '../components/Wordmark.svelte';

  let handle = $state('');
  let password = $state('');
  let busy = $state(false);
  let error = $state('');
  let handleEl: HTMLInputElement | undefined = $state();

  $effect(() => {
    handleEl?.focus();
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!handle.trim() || !password) {
      error = 'Enter your handle and password.';
      return;
    }
    busy = true;
    error = '';
    try {
      await app.signIn(handle.trim().replace(/^@/, ''), password);
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

<main class="auth" aria-labelledby="signin-title">
  <div class="panel">
    <Wordmark size={30} />
    <h1 id="signin-title">Sign in to {app.setupOrgName || 'your workspace'}</h1>
    <form onsubmit={submit} novalidate>
      <label class="field">
        <span class="label">Handle</span>
        <input
          class="input"
          name="handle"
          autocomplete="username"
          autocapitalize="none"
          spellcheck="false"
          bind:value={handle}
          bind:this={handleEl}
          aria-invalid={!!error && !handle.trim()}
        />
      </label>
      <label class="field">
        <span class="label">Password</span>
        <input class="input" type="password" name="password" autocomplete="current-password" bind:value={password} aria-invalid={!!error && !password} />
      </label>
      {#if error}
        <p class="form-error" role="alert">{error}</p>
      {/if}
      <button class="btn btn-primary submit" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
    </form>
    <p class="meta">Your workspace runs on your own hub. Work continues on your machines while you're signed out.</p>
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
    width: min(400px, 100%);
    display: grid;
    gap: 20px;
    padding: 32px;
    background: var(--surface);
    border-radius: var(--r-surface);
    box-shadow: var(--shadow-card);
  }
  h1 {
    font-size: 22px;
  }
  form {
    display: grid;
    gap: 16px;
  }
  .submit {
    justify-self: start;
  }
  @media (max-width: 480px) {
    .panel {
      padding: 24px 20px;
    }
  }
</style>
