<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { api, exportUrl } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Diagnostics } from '../lib/api/types.gen';
  import { atTime, bytes } from '../lib/util/time';
  import StateIcon from '../components/StateIcon.svelte';
  import Icon from '../components/Icon.svelte';

  const prefs = $derived(app.data.preferences);
  let diag = $state<Diagnostics | null>(null);
  let diagError = $state('');
  let diagLoading = $state(false);
  const notifyPermission = $state(typeof Notification === 'undefined' ? 'unsupported' : Notification.permission);
  let permission = $state<string>(notifyPermission);

  async function loadDiagnostics() {
    diagLoading = true;
    diagError = '';
    try {
      diag = await api.diagnostics();
    } catch (err) {
      diagError = errorMessage(err);
    } finally {
      diagLoading = false;
    }
  }

  async function askPermission() {
    if (typeof Notification === 'undefined') return;
    permission = await Notification.requestPermission();
  }
</script>

<div class="screen">
  <div class="screen-inner narrow">
    <header class="screen-head">
      <div>
        <h1 class="screen-title" data-screen-title tabindex="-1">Settings</h1>
        <p class="screen-sub">{app.me?.name} · @{app.me?.handle} · {app.data.org?.name}</p>
      </div>
    </header>

    <section class="group" aria-labelledby="set-appearance">
      <h2 id="set-appearance" class="section-title">Appearance</h2>
      <fieldset class="opts">
        <legend class="vh">Appearance</legend>
        {#each [['system', 'Match this device'], ['day', 'Day'], ['night', 'Night']] as [v, label] (v)}
          <label class="opt"><input type="radio" name="theme" value={v} checked={app.theme === v} onchange={() => app.setPreferences({ theme: v })} />{label}</label>
        {/each}
      </fieldset>
      <fieldset class="opts">
        <legend class="label">Density</legend>
        <label class="opt"><input type="radio" name="density" checked={prefs.density !== 'compact'} onchange={() => app.setPreferences({ density: 'comfortable' })} />Comfortable</label>
        <label class="opt"><input type="radio" name="density" checked={prefs.density === 'compact'} onchange={() => app.setPreferences({ density: 'compact' })} />Compact</label>
      </fieldset>
    </section>

    <section class="group" aria-labelledby="set-keys">
      <h2 id="set-keys" class="section-title">Sending messages</h2>
      <fieldset class="opts col">
        <legend class="vh">Send key</legend>
        <label class="opt"><input type="radio" name="send" checked={prefs.sendKey !== 'mod-enter'} onchange={() => app.setPreferences({ sendKey: 'enter' })} /><span><kbd>Enter</kbd> sends, <kbd>Shift</kbd>+<kbd>Enter</kbd> adds a line</span></label>
        <label class="opt"><input type="radio" name="send" checked={prefs.sendKey === 'mod-enter'} onchange={() => app.setPreferences({ sendKey: 'mod-enter' })} /><span><kbd>⌘/Ctrl</kbd>+<kbd>Enter</kbd> sends, <kbd>Enter</kbd> adds a line</span></label>
      </fieldset>
    </section>

    <section class="group" aria-labelledby="set-notify">
      <h2 id="set-notify" class="section-title">Notifications</h2>
      <p class="meta">Only while this tab is in the background. Muting never changes what engineers do.</p>
      <fieldset class="opts col">
        <legend class="vh">Notify me about</legend>
        <label class="opt"><input type="radio" name="notify" checked={prefs.notify === 'mentions' || !prefs.notify} onchange={() => app.setPreferences({ notify: 'mentions' })} /><span>Questions for me, results and permission requests</span></label>
        <label class="opt"><input type="radio" name="notify" checked={prefs.notify === 'all'} onchange={() => app.setPreferences({ notify: 'all' })} /><span>Every message from engineers</span></label>
        <label class="opt"><input type="radio" name="notify" checked={prefs.notify === 'none'} onchange={() => app.setPreferences({ notify: 'none' })} /><span>Nothing</span></label>
      </fieldset>
      {#if permission === 'default' && prefs.notify !== 'none'}
        <button class="btn btn-sm" onclick={askPermission}>Allow browser notifications</button>
      {:else if permission === 'denied'}
        <p class="meta">Browser notifications are blocked for this site in your browser settings.</p>
      {:else if permission === 'unsupported'}
        <p class="meta">This browser doesn't support notifications.</p>
      {/if}
    </section>

    <section class="group" aria-labelledby="set-diag">
      <div class="section-head">
        <h2 id="set-diag" class="section-title">Diagnostics</h2>
        <button class="btn btn-sm" onclick={loadDiagnostics} disabled={diagLoading}><Icon name="refresh" size={15} />{diag ? 'Check again' : 'Run checks'}</button>
      </div>
      {#if diagError}<p class="notice danger" role="alert">{diagError}</p>{/if}
      {#if diag}
        <ul class="checks">
          {#each diag.checks ?? [] as c (c.name)}
            <li>
              <StateIcon shape={c.ok ? 'check-filled' : 'triangle'} tone={c.ok ? 'success' : 'danger'} />
              <span><strong>{c.name}</strong> — {c.ok ? 'OK' : 'Problem'}{c.detail ? `: ${c.detail}` : ''}</span>
            </li>
          {/each}
        </ul>
        <dl class="facts">
          <div><dt>Hub version</dt><dd>{diag.version}</dd></div>
          <div><dt>Data directory</dt><dd class="mono">{diag.dataDir}</dd></div>
          <div><dt>Database</dt><dd>{bytes(diag.dbSizeBytes)} · artifacts {bytes(diag.artifactBytes)} · {bytes(diag.diskFreeBytes)} free</dd></div>
          <div><dt>Work</dt><dd>{diag.activeRuns} running · {diag.queueDepth} queued · {diag.failedRuns24h} failed in 24h · {diag.pendingApprovals} permission requests waiting</dd></div>
          <div><dt>Connections</dt><dd>{diag.sseClients} open {diag.sseClients === 1 ? 'window' : 'windows'} · {diag.nodes?.length ?? 0} machines</dd></div>
          <div><dt>Last backup</dt><dd>{diag.lastBackupAt ? atTime(diag.lastBackupAt) : 'No backup recorded'}</dd></div>
        </dl>
      {:else if !diagError}
        <p class="meta">Checks the hub's storage, queue, machines and connections.</p>
      {/if}
    </section>

    <section class="group" aria-labelledby="set-export">
      <h2 id="set-export" class="section-title">Your data</h2>
      <p class="meta">Download rooms, messages, work records, decisions and files as documented JSON in a zip. Nothing leaves your hub otherwise.</p>
      <a class="btn btn-sm" href={exportUrl} download><Icon name="download" size={15} />Export everything</a>
    </section>

    <section class="group" aria-labelledby="set-session">
      <h2 id="set-session" class="section-title">Session</h2>
      <p class="meta">Signing out ends this browser's session. Engineers' work keeps running on your machines.</p>
      <button class="btn btn-sm" onclick={() => app.signOut()}><Icon name="logout" size={15} />Sign out</button>
      <p class="meta version">yip {app.data.version}</p>
    </section>
  </div>
</div>

<style>
  .narrow {
    max-width: 760px;
  }
  .group {
    display: grid;
    gap: 10px;
    padding: 22px 0;
    border-top: 1px solid var(--line);
    justify-items: start;
  }
  .group > * {
    max-width: 100%;
  }
  .section-head {
    width: 100%;
  }
  .opts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 18px;
    border: 0;
    margin: 0;
    padding: 0;
  }
  .opts.col {
    flex-direction: column;
  }
  .opts .label {
    width: 100%;
    margin-bottom: 4px;
  }
  .opt {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-height: 36px;
    font-size: 14.5px;
    cursor: pointer;
  }
  .opt input {
    width: 18px;
    height: 18px;
    accent-color: var(--accent);
    margin: 0;
  }
  .checks {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 6px;
    font-size: 14px;
  }
  .checks li {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .facts {
    margin: 6px 0 0;
    display: grid;
    gap: 6px;
    width: 100%;
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
    overflow-wrap: anywhere;
  }
  .version {
    margin-top: 12px;
  }
  @media (pointer: coarse) {
    .opt {
      min-height: 44px;
    }
  }
  @media (max-width: 560px) {
    .facts > div {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
</style>
