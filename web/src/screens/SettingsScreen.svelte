<script lang="ts">
  import { Button, Code, Icon, MetadataList, MetadataListItem, RadioList, RadioListItem, SegmentedControl, SegmentedControlItem, Text, TextArea } from '@astryx-svelte/core';
  import Notice from '../components/Notice.svelte';
  import { Download, LogOut, RefreshCw } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { api, exportUrl } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { DiagnosticBundle, Diagnostics } from '../lib/api/types.gen';
  import { atTime, bytes } from '../lib/util/time';
  import StateIcon from '../components/StateIcon.svelte';
  import Screen from '../components/Screen.svelte';
  import ScreenSection from '../components/ScreenSection.svelte';

  const prefs = $derived(app.data.preferences);
  // Unknown or missing stored values read as the default choice.
  const density = $derived(prefs.density === 'compact' ? 'compact' : 'comfortable');
  const sendKey = $derived(prefs.sendKey === 'mod-enter' ? 'mod-enter' : 'enter');
  const notify = $derived(prefs.notify === 'all' || prefs.notify === 'none' ? prefs.notify : 'mentions');
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

  // The debug bundle is opt-in and shown in full before it's saved.
  let bundle = $state<DiagnosticBundle | null>(null);
  let bundleError = $state('');
  const bundleText = $derived(bundle ? JSON.stringify(bundle, null, 2) : '');
  async function prepareBundle() {
    bundleError = '';
    try {
      bundle = await api.diagnosticBundle();
    } catch (err) {
      bundleError = errorMessage(err);
    }
  }
  function saveBundle() {
    const url = URL.createObjectURL(new Blob([bundleText], { type: 'application/json' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = `yip-diagnostics-${new Date().toISOString().slice(0, 10)}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  async function askPermission() {
    if (typeof Notification === 'undefined') return;
    permission = await Notification.requestPermission();
  }
</script>

<Screen title="Settings" subtitle="{app.me?.name} · @{app.me?.handle} · {app.data.org?.name}" width={760}>
  <div class="groups">
    <ScreenSection title="Appearance" id="set-appearance" class="group">
      <div class="body">
        <SegmentedControl label="Appearance" value={app.theme} onChange={(v) => app.setPreferences({ theme: v })}>
          <SegmentedControlItem value="system" label="Match this device" />
          <SegmentedControlItem value="day" label="Day" />
          <SegmentedControlItem value="night" label="Night" />
        </SegmentedControl>
        <div class="setting">
          <Text as="p" display="block" type="label">Density</Text>
          <SegmentedControl label="Density" value={density} onChange={(v) => app.setPreferences({ density: v })}>
            <SegmentedControlItem value="comfortable" label="Comfortable" />
            <SegmentedControlItem value="compact" label="Compact" />
          </SegmentedControl>
        </div>
      </div>
    </ScreenSection>

    <ScreenSection title="Sending messages" id="set-keys" class="group">
      <RadioList label="Send key" isLabelHidden value={sendKey} onChange={(v) => app.setPreferences({ sendKey: v })}>
        <RadioListItem value="enter" label="Enter sends, Shift+Enter adds a line" />
        <RadioListItem value="mod-enter" label="⌘/Ctrl+Enter sends, Enter adds a line" />
      </RadioList>
    </ScreenSection>

    <ScreenSection title="Notifications" id="set-notify" class="group">
      <div class="body">
        <Text as="p" display="block" type="supporting">
          Only while this tab is in the background. Failed work and work stuck until someone acts always count. Mute a single room from its settings; muting never changes what engineers do.
        </Text>
        <RadioList label="Notify me about" isLabelHidden value={notify} onChange={(v) => app.setPreferences({ notify: v })}>
          <RadioListItem value="mentions" label="Questions for me, results, failures and permission requests" />
          <RadioListItem value="all" label="Every message from engineers" />
          <RadioListItem value="none" label="Nothing" />
        </RadioList>
        {#if permission === 'default' && prefs.notify !== 'none'}
          <Button label="Allow browser notifications" size="sm" onclick={askPermission} />
        {:else if permission === 'denied'}
          <Text as="p" display="block" type="supporting">Browser notifications are blocked for this site in your browser settings.</Text>
        {:else if permission === 'unsupported'}
          <Text as="p" display="block" type="supporting">This browser doesn't support notifications.</Text>
        {/if}
      </div>
    </ScreenSection>

    <ScreenSection title="Diagnostics" id="set-diag" class="group">
      {#snippet end()}
        <Button label={diag ? 'Check again' : 'Run checks'} size="sm" isLoading={diagLoading} onclick={loadDiagnostics}>
          {#snippet icon()}<Icon icon={RefreshCw} size="sm" />{/snippet}
        </Button>
      {/snippet}
      <div class="body">
        {#if diagError}<div class="alert"><Notice tone="danger" role="alert">{diagError}</Notice></div>{/if}
        {#if diag}
          <ul class="checks">
            {#each diag.checks ?? [] as c (c.name)}
              <li>
                <StateIcon shape={c.ok ? 'check-filled' : 'triangle'} tone={c.ok ? 'success' : 'danger'} />
                <span><strong>{c.name}</strong> — {c.ok ? 'OK' : 'Problem'}{c.detail ? `: ${c.detail}` : ''}</span>
              </li>
            {/each}
          </ul>
          <div class="facts">
            <MetadataList label={app.viewport <= 560 ? { position: 'top' } : { position: 'start', width: 130 }}>
              <MetadataListItem label="Hub version">{diag.version}</MetadataListItem>
              <MetadataListItem label="Data directory"><Code>{diag.dataDir}</Code></MetadataListItem>
              <MetadataListItem label="Database">{bytes(diag.dbSizeBytes)} · artifacts {bytes(diag.artifactBytes)} · {bytes(diag.diskFreeBytes)} free</MetadataListItem>
              <MetadataListItem label="Work">
                {diag.activeRuns} running · {diag.queueDepth} queued · {diag.failedRuns24h} failed in 24h · {diag.pendingApprovals} permission requests waiting
              </MetadataListItem>
              <MetadataListItem label="Connections">
                {diag.sseClients} open {diag.sseClients === 1 ? 'window' : 'windows'} · {diag.nodes?.length ?? 0} machines
              </MetadataListItem>
              <MetadataListItem label="Last backup">{diag.lastBackupAt ? atTime(diag.lastBackupAt) : 'No backup recorded'}</MetadataListItem>
            </MetadataList>
          </div>
        {:else if !diagError}
          <Text as="p" display="block" type="supporting">Checks the hub's storage, queue, machines and connections.</Text>
        {/if}
        <div class="bundle">
          <Text as="p" display="block" type="supporting">
            A diagnostic bundle helps someone troubleshoot your hub. It holds counts, health, versions and recent failure reasons — no messages, prompts,
            code, account names or credentials. You see all of it first; nothing is sent anywhere.
          </Text>
          {#if bundleError}<div class="alert"><Notice tone="danger" role="alert">{bundleError}</Notice></div>{/if}
          {#if !bundle}
            <Button label="Prepare a diagnostic bundle" size="sm" onclick={prepareBundle} />
          {:else}
            <div class="preview">
              <TextArea label="Diagnostic bundle contents" isLabelHidden isReadOnly hasSpellCheck={false} rows={14} width="100%" value={bundleText} />
            </div>
            <div class="row-actions">
              <Button label="Save this file" variant="primary" size="sm" onclick={saveBundle}>
                {#snippet icon()}<Icon icon={Download} size="sm" />{/snippet}
              </Button>
              <Button label="Discard" variant="ghost" size="sm" onclick={() => (bundle = null)} />
            </div>
          {/if}
        </div>
      </div>
    </ScreenSection>

    <ScreenSection title="Your data" id="set-export" class="group">
      <div class="body">
        <Text as="p" display="block" type="supporting">Download rooms, messages, work records, decisions and files as documented JSON in a zip. Nothing leaves your hub otherwise.</Text>
        <Button label="Export everything" size="sm" href={exportUrl} download>
          {#snippet icon()}<Icon icon={Download} size="sm" />{/snippet}
        </Button>
      </div>
    </ScreenSection>

    <ScreenSection title="Session" id="set-session" class="group">
      <div class="body">
        <Text as="p" display="block" type="supporting">Signing out ends this browser's session. Engineers' work keeps running on your machines.</Text>
        <Button label="Sign out" size="sm" onclick={() => app.signOut()}>
          {#snippet icon()}<Icon icon={LogOut} size="sm" />{/snippet}
        </Button>
        <p class="version"><Text type="supporting">yip {app.data.version}</Text></p>
      </div>
    </ScreenSection>
  </div>
</Screen>

<style>
  .groups :global(.group) {
    margin-top: 0;
    padding: var(--spacing-5) 0 var(--spacing-6);
    border-top: 1px solid var(--color-border);
  }
  .body,
  .bundle {
    display: grid;
    gap: var(--spacing-3);
    justify-items: start;
  }
  .body > :global(*),
  .bundle > :global(*) {
    max-width: 100%;
  }
  .setting {
    display: grid;
    gap: var(--spacing-1-5);
  }
  .bundle {
    gap: var(--spacing-2);
    margin-top: var(--spacing-3);
  }
  .alert,
  .preview,
  .facts {
    width: 100%;
  }
  .preview :global(textarea) {
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    resize: vertical;
  }
  .row-actions {
    display: flex;
    gap: var(--spacing-2);
  }
  .checks {
    display: grid;
    gap: var(--spacing-1-5);
  }
  .checks li {
    display: flex;
    gap: var(--spacing-2);
    align-items: flex-start;
  }
  /* Centres the mark on the first line of its text. */
  .checks li > :global(svg) {
    margin-top: 3px;
  }
  .facts :global(dd) {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .version {
    margin-top: var(--spacing-3);
  }
</style>
