<script lang="ts">
  // Diagnostics: everything for troubleshooting, kept out of the way —
  // runner and provider versions, what each adapter supports and its full
  // notes, execution profiles, toolchains, the fingerprint to compare with
  // the machine, and its recent activity.
  import { Button, Code, Heading, Icon, MetadataList, MetadataListItem, Text } from '@astryx-svelte/core';
  import { Copy } from '@lucide/svelte';
  import type { Node, ProviderCapabilities } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { runShape, runStateLabel, runTone } from '../../lib/util/labels';
  import { bytes, fullTime, relative } from '../../lib/util/time';
  import { osLabel, type ProviderStatus } from '../../lib/util/machines';
  import StateIcon from '../StateIcon.svelte';

  interface Props {
    node: Node;
    providers: ProviderStatus[];
  }
  let { node: n, providers }: Props = $props();

  const CAPS: [keyof ProviderCapabilities, string][] = [
    ['structuredEvents', 'structured events'],
    ['toolApprovals', 'tool approvals'],
    ['userQuestions', 'questions to you'],
    ['sessionResume', 'resuming a session'],
    ['activeSteering', 'steering while running'],
    ['usageTelemetry', 'usage reporting'],
    ['sandbox', 'its own sandbox'],
    ['modelEnumeration', 'listing models'],
    ['readOnly', 'read-only runs'],
    ['mcpTools', 'yip’s tools'],
  ];
  const PROFILE_LABEL: Record<string, string> = { native: 'Native', readonly: 'Read-only', container: 'Container' };

  let copied = $state(false);
  async function copyFingerprint() {
    try {
      await navigator.clipboard.writeText(n.fingerprint);
      copied = true;
      app.announce('Fingerprint copied.');
      setTimeout(() => (copied = false), 2000);
    } catch {
      app.toast("Couldn't copy — select the fingerprint and copy it manually.", 'error');
    }
  }

  const toolchains = $derived(Object.entries(n.toolchains ?? {}).sort(([a], [b]) => a.localeCompare(b)));
  const recent = $derived(
    Object.values(app.data.runs)
      .filter((r) => r.nodeId === n.id)
      .sort((a, b) => (b.startedAt ?? b.createdAt).localeCompare(a.startedAt ?? a.createdAt))
      .slice(0, 8),
  );
</script>

<section class="block" aria-labelledby="md-runner">
  <Heading level={3} id="md-runner">Runner</Heading>
  <div class="kv">
    <MetadataList>
      <MetadataListItem label="Version">{n.runnerVersion || 'Not reported'}</MetadataListItem>
      <MetadataListItem label="Platform">{[osLabel(n.os), n.arch].filter(Boolean).join(' · ') || 'Not reported'}{n.hostname ? ` · ${n.hostname}` : ''}</MetadataListItem>
      {#if n.capacity.cpus}<MetadataListItem label="Hardware">{n.capacity.cpus} CPUs · {bytes(n.capacity.memMb * 1024 * 1024)} memory</MetadataListItem>{/if}
      <MetadataListItem label="Supervision">{n.serviceState || 'Not reported'}</MetadataListItem>
      <MetadataListItem label="Paired">{fullTime(n.createdAt)}</MetadataListItem>
      <MetadataListItem label="Fingerprint">
        <span class="fp">
          <Code>{n.fingerprint}</Code>
          <Button label="Copy fingerprint" size="sm" onclick={copyFingerprint}>
            {#snippet icon()}<Icon icon={Copy} size="sm" />{/snippet}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </span>
      </MetadataListItem>
    </MetadataList>
  </div>
  <Text as="p" type="supporting" class="after-kv">Compare the fingerprint with <Code size="inherit">yip doctor</Code> on the machine.</Text>
</section>

<section class="block" aria-labelledby="md-providers">
  <Heading level={3} id="md-providers">Providers</Heading>
  {#if n.providers.length === 0}
    <Text as="p" type="supporting">None reported.</Text>
  {/if}
  {#each n.providers as p (p.provider)}
    {@const st = providers.find((x) => x.provider === p.provider)}
    <div class="prov">
      <p class="prov-name"><strong>{st?.name ?? p.provider}</strong> <Text type="supporting">{st?.compat}</Text></p>
      <div class="kv">
        <MetadataList>
          {#if p.path}<MetadataListItem label="Path"><Code>{p.path}</Code></MetadataListItem>{/if}
          {#if p.profileId}<MetadataListItem label="Account pool"><Code>{p.profileId}</Code></MetadataListItem>{/if}
          {#if p.authDetail}<MetadataListItem label="Sign-in detail">{p.authDetail}</MetadataListItem>{/if}
          {#if p.authState !== 'not_installed'}
            <MetadataListItem label="Supports">{CAPS.filter(([k]) => p.capabilities?.[k]).map(([, l]) => l).join(', ') || 'Nothing reported'}</MetadataListItem>
            {#if CAPS.some(([k]) => !p.capabilities?.[k])}
              <MetadataListItem label="Doesn’t support">{CAPS.filter(([k]) => !p.capabilities?.[k]).map(([, l]) => l).join(', ')}</MetadataListItem>
            {/if}
            {#if p.models?.length}<MetadataListItem label="Models">{p.models.map((m) => m.label || m.id).join(', ')}</MetadataListItem>{/if}
          {/if}
        </MetadataList>
      </div>
      {#if p.limitations?.length}
        <p class="sub">Adapter notes</p>
        <ul class="notes">{#each p.limitations as l (l)}<li>{l}</li>{/each}</ul>
      {/if}
    </div>
  {/each}
</section>

<section class="block" aria-labelledby="md-profiles">
  <Heading level={3} id="md-profiles">Execution profiles</Heading>
  {#if n.profiles.length === 0}<Text as="p" type="supporting">None reported.</Text>{/if}
  <ul class="profiles">
    {#each n.profiles as pr (pr.name)}
      <li>
        <StateIcon shape={pr.available ? 'check-filled' : 'slash'} tone={pr.available ? 'success' : 'neutral'} size={14} />
        <div>
          <p><strong>{PROFILE_LABEL[pr.name] ?? pr.name}</strong> · {pr.available ? 'Available' : 'Unavailable'}</p>
          <Text as="p" type="supporting">{pr.available ? pr.summary : pr.reason || 'No reason given.'}</Text>
        </div>
      </li>
    {/each}
  </ul>
</section>

<section class="block" aria-labelledby="md-tools">
  <Heading level={3} id="md-tools">Toolchains</Heading>
  {#if toolchains.length === 0}
    <Text as="p" type="supporting">None reported.</Text>
  {:else}
    <div class="kv">
      <MetadataList class="tools">
        {#each toolchains as [name, version] (name)}
          <MetadataListItem label={name}><Code>{version}</Code></MetadataListItem>
        {/each}
      </MetadataList>
    </div>
  {/if}
</section>

<section class="block" aria-labelledby="md-activity">
  <Heading level={3} id="md-activity">Activity</Heading>
  <div class="kv">
    <MetadataList>
      <MetadataListItem label="Last heard">{n.lastSeenAt ? `${fullTime(n.lastSeenAt)} (${relative(n.lastSeenAt, app.now)})` : 'Never'}</MetadataListItem>
      {#if n.lastActivity}<MetadataListItem label="Last reported">{n.lastActivity}</MetadataListItem>{/if}
    </MetadataList>
  </div>
  {#if recent.length}
    <ul class="runs">
      {#each recent as r (r.id)}
        {@const j = app.data.jobs[r.jobId]}
        <li>
          <StateIcon shape={runShape(r.state)} tone={runTone(r.state)} size={13} />
          <span class="run-text">{runStateLabel(r.state)}{j ? ` · ${j.kind === 'reply' ? 'a reply' : j.title}` : ''}</span>
          <Text type="supporting">{relative(r.startedAt ?? r.createdAt, app.now)}</Text>
        </li>
      {/each}
    </ul>
  {:else}
    <Text as="p" type="supporting">No attempts on this machine since this page loaded.</Text>
  {/if}
</section>

<style>
  .block {
    margin-bottom: var(--spacing-6);
  }
  .block :global(h3) {
    font-size: var(--font-size-base);
    font-weight: var(--font-weight-semibold);
    margin-bottom: var(--spacing-2);
  }
  .kv :global(dl) {
    grid-template-columns: 112px minmax(0, 1fr);
    row-gap: var(--spacing-1-5);
  }
  .kv :global(dt) {
    font-weight: var(--font-weight-normal);
    overflow-wrap: anywhere;
  }
  .kv :global(dd) {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .fp {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-2);
    min-width: 0;
  }
  .fp :global(code) {
    min-width: 0;
    overflow-wrap: anywhere;
    user-select: all;
  }
  .block :global(.after-kv) {
    margin-top: var(--spacing-1-5);
  }
  .prov {
    padding: var(--spacing-3) 0;
    border-top: 1px solid var(--color-border);
  }
  .prov-name {
    margin-bottom: var(--spacing-1-5);
    overflow-wrap: anywhere;
  }
  .prov-name strong {
    font-weight: var(--font-weight-semibold);
  }
  .sub {
    margin: var(--spacing-2) 0 var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    color: var(--color-text-secondary);
  }
  .notes {
    margin: 0;
    padding-left: var(--spacing-4);
    list-style: disc;
    display: grid;
    gap: var(--spacing-1);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }
  .profiles,
  .runs {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: var(--spacing-2);
  }
  .profiles li {
    display: flex;
    gap: var(--spacing-2);
    align-items: flex-start;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .profiles li > :global(svg) {
    margin-top: 3px;
  }
  .runs {
    margin-top: var(--spacing-2);
    gap: var(--spacing-1);
  }
  .runs li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: var(--spacing-2);
    align-items: center;
  }
  .run-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  @container machinepanel (max-width: 440px) {
    .kv :global(dl) {
      grid-template-columns: minmax(0, 1fr);
      row-gap: 0;
    }
    .kv :global(dd:not(:last-child)) {
      margin-bottom: var(--spacing-1-5);
    }
  }
</style>
