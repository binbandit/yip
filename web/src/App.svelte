<script lang="ts">
  import { app } from './lib/state/app.svelte';
  import Shell from './screens/Shell.svelte';
  import SignIn from './screens/SignIn.svelte';
  import Setup from './screens/Setup.svelte';
  import Toasts from './components/Toasts.svelte';
  import Wordmark from './components/Wordmark.svelte';
</script>

{#if app.phase === 'ready'}
  <Shell />
{:else if app.phase === 'setup'}
  <Setup />
{:else if app.phase === 'signin'}
  <SignIn />
{:else if app.phase === 'error'}
  <main class="boot" aria-labelledby="boot-title">
    <Wordmark size={28} />
    <h1 id="boot-title">Can't reach your workspace.</h1>
    <p class="muted">{app.bootError || 'The hub did not respond.'} Drafts you were writing are saved on this device.</p>
    <button class="btn btn-primary" onclick={() => app.boot()}>Try again</button>
  </main>
{:else}
  <main class="boot" aria-busy="true" aria-label="Loading your workspace">
    <Wordmark size={28} />
    <p class="muted">Opening your workspace…</p>
  </main>
{/if}

<div class="vh" role="status" aria-live="polite" aria-atomic="true">{app.announcement}</div>
<Toasts />

<style>
  .boot {
    min-height: 100%;
    display: grid;
    place-content: center;
    justify-items: start;
    gap: 14px;
    padding: 24px;
    max-width: 480px;
    margin: 0 auto;
    background: var(--frame);
  }
  .boot h1 {
    font-size: 22px;
  }
</style>
