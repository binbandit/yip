<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Button, Link, MetadataList, MetadataListItem, Text } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Bootstrap, ProviderProfile } from '../lib/api/types.gen';
  import { workspaceUrl } from '../lib/workspace';
  import Screen from '../components/Screen.svelte';
  import ScreenSection from '../components/ScreenSection.svelte';
  import Notice from '../components/Notice.svelte';
  import BillingPermission from '../components/settings/BillingPermission.svelte';
  import AccountConcurrency from '../components/settings/AccountConcurrency.svelte';

  let saved = $state<Bootstrap | null>(null);
  let profiles = $state<ProviderProfile[]>([]);
  let loading = $state(true);
  let error = $state('');
  let edits = $state<Record<string, { dirty: boolean; busy: boolean }>>({});
  const dirty = $derived(Object.values(edits).some((edit) => edit.dirty));
  const busy = $derived(Object.values(edits).some((edit) => edit.busy));
  const engineers = $derived([...(saved?.engineers ?? [])].sort((a, b) => Number(a.archived) - Number(b.archived) || a.name.localeCompare(b.name)));

  async function load() {
    loading = true;
    error = '';
    try {
      const [bootstrap, accounts] = await Promise.all([api.bootstrap(), api.providerProfiles()]);
      saved = bootstrap;
      profiles = accounts;
      edits = {};
    } catch (err) {
      error = errorMessage(err);
    } finally {
      loading = false;
    }
    await tick();
    document.getElementById(window.location.hash.slice(1))?.scrollIntoView();
  }

  onMount(() => {
    void load();
    let leaving = false;
    const guard = () => {
      if (busy) {
        app.toast('Wait for settings to finish saving.', 'error');
        return false;
      }
      const proceed = !dirty || window.confirm('Discard unsaved workspace settings?');
      if (proceed) leaving = true;
      return proceed;
    };
    app.beforeNavigate = guard;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!leaving && (dirty || busy)) { event.preventDefault(); event.returnValue = ''; }
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => {
      if (app.beforeNavigate === guard) app.beforeNavigate = null;
      window.removeEventListener('beforeunload', beforeUnload);
    };
  });
</script>

<Screen title="Workspace settings" subtitle={saved?.org.name ?? app.data.org?.name} width={760}>
  {#snippet actions()}<Button label="Personal settings" href={workspaceUrl('/settings')} />{/snippet}
  {#if loading}<Text as="p" role="status">Loading workspace settings…</Text>
  {:else if error}
    <Notice tone="danger" role="alert">{error}</Notice>
    <Button label="Try again" onclick={load} />
  {:else if saved}
    <div class="groups">
      <ScreenSection title="Workspace" id="workspace-info" class="group">
        <MetadataList>
          <MetadataListItem label="Name">{saved.org.name}</MetadataListItem>
          <MetadataListItem label="Your account">{saved.user.name} · @{saved.user.handle}{saved.canManageWorkspace ? ' · Owner' : ''}</MetadataListItem>
        </MetadataList>
        <Text as="p" type="supporting">These settings apply to {saved.org.name}. Other workspaces keep their own engineers, accounts and permissions.</Text>
        {#if !saved.canManageWorkspace}<Notice>Only the workspace owner can change these settings.</Notice>{/if}
      </ScreenSection>
      <ScreenSection title="API billing permissions" id="api-billing" class="group">
        <div class="intro">
          <p>Allow API billing separately for each engineer. New engineers start with this permission off.</p>
          <p>API usage may incur charges from the provider, separate from your subscription. This permission has no budget or spending cap. Saving an opt-in can let queued work start on a paid account.</p>
          <Text as="p" type="supporting">When off, accounts known to use API billing are blocked, including harnesses with mixed API and subscription accounts. Unknown billing is not a guarantee of subscription usage. Existing runs keep their starting permissions.</Text>
          <Link hasUnderline href={workspaceUrl('/connections')}>Review connections and reported billing</Link>
        </div>
        {#each engineers as engineer (engineer.id)}
          <BillingPermission {engineer} canEdit={saved.canManageWorkspace} onstate={(dirty, busy) => { edits[`engineer:${engineer.id}`] = { dirty, busy }; }} />
        {:else}
          <p class="empty">No engineers yet. <Link hasUnderline href={workspaceUrl('/engineers')}>Create an engineer</Link> to set their provider and billing permission.</p>
        {/each}
      </ScreenSection>
      <ScreenSection title="Account concurrency" id="account-concurrency" class="group">
        <div class="intro">
          <p>The number of runs each account can carry at once across this workspace’s machines. Runs share that account’s allowance; higher limits may consume it faster.</p>
          <Text as="p" type="supporting">This is a concurrency limit, not a spending cap or permission to use API billing. Machine slots and provider availability can reduce the number of active runs.</Text>
        </div>
        {#each profiles as profile (profile.id)}
          <AccountConcurrency {profile} canEdit={saved.canManageWorkspace} onstate={(dirty, busy) => { edits[`profile:${profile.id}`] = { dirty, busy }; }} />
        {:else}
          <p class="empty">No accounts reported yet. <Link hasUnderline href={workspaceUrl('/connections')}>Connect a provider</Link> on a paired machine to see its account here.</p>
        {/each}
      </ScreenSection>
      <ScreenSection title="More settings" id="workspace-more" class="group">
        <div class="links">
          <Link hasUnderline href={workspaceUrl('/projects')}>Project access and review policies</Link>
          <Link hasUnderline href={workspaceUrl('/machines')}>Machines and their connections</Link>
          <Link hasUnderline href={workspaceUrl('/settings')}>Your profile, appearance, notifications and diagnostics</Link>
        </div>
      </ScreenSection>
    </div>
  {/if}
</Screen>

<style>
  .groups :global(.group) { margin-top: 0; padding: var(--spacing-5) 0 var(--spacing-6); border-top: 1px solid var(--color-border); }
  .intro, .links { display: grid; gap: var(--spacing-3); margin-bottom: var(--spacing-4); }
  .empty { margin-top: var(--spacing-4); }
</style>
