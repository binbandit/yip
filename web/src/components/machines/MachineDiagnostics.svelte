<script lang="ts">
  // Diagnostics: everything for troubleshooting, kept out of the way —
  // runner and provider versions, what each adapter supports and its full
  // notes, execution profiles, toolchains, the fingerprint to compare with
  // the machine, and its recent activity.
  import type { Node, ProviderCapabilities } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { runShape, runStateLabel, runTone } from '../../lib/util/labels';
  import { bytes, fullTime, relative } from '../../lib/util/time';
  import { osLabel, type ProviderStatus } from '../../lib/util/machines';
  import StateIcon from '../StateIcon.svelte';
  import Icon from '../Icon.svelte';

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
  <h3 id="md-runner">Runner</h3>
  <dl class="kv">
    <div><dt>Version</dt><dd>{n.runnerVersion || 'Not reported'}</dd></div>
    <div><dt>Platform</dt><dd>{[osLabel(n.os), n.arch].filter(Boolean).join(' · ') || 'Not reported'}{n.hostname ? ` · ${n.hostname}` : ''}</dd></div>
    {#if n.capacity.cpus}<div><dt>Hardware</dt><dd>{n.capacity.cpus} CPUs · {bytes(n.capacity.memMb * 1024 * 1024)} memory</dd></div>{/if}
    <div><dt>Supervision</dt><dd>{n.serviceState || 'Not reported'}</dd></div>
    <div><dt>Paired</dt><dd>{fullTime(n.createdAt)}</dd></div>
    <div>
      <dt>Fingerprint</dt>
      <dd class="fp">
        <code>{n.fingerprint}</code>
        <button class="btn btn-sm" aria-label="Copy fingerprint" onclick={copyFingerprint}><Icon name="copy" size={14} />{copied ? 'Copied' : 'Copy'}</button>
      </dd>
    </div>
  </dl>
  <p class="meta">Compare the fingerprint with <code>yip doctor</code> on the machine.</p>
</section>

<section class="block" aria-labelledby="md-providers">
  <h3 id="md-providers">Providers</h3>
  {#if n.providers.length === 0}
    <p class="meta">None reported.</p>
  {/if}
  {#each n.providers as p (p.provider)}
    {@const st = providers.find((x) => x.provider === p.provider)}
    <div class="prov">
      <p class="prov-name"><strong>{st?.name ?? p.provider}</strong> <span class="meta">{st?.compat}</span></p>
      <dl class="kv">
        {#if p.path}<div><dt>Path</dt><dd class="mono">{p.path}</dd></div>{/if}
        {#if p.profileId}<div><dt>Account pool</dt><dd class="mono">{p.profileId}</dd></div>{/if}
        {#if p.authDetail}<div><dt>Sign-in detail</dt><dd>{p.authDetail}</dd></div>{/if}
        {#if p.authState !== 'not_installed'}
          <div><dt>Supports</dt><dd>{CAPS.filter(([k]) => p.capabilities?.[k]).map(([, l]) => l).join(', ') || 'Nothing reported'}</dd></div>
          {#if CAPS.some(([k]) => !p.capabilities?.[k])}
            <div><dt>Doesn’t support</dt><dd>{CAPS.filter(([k]) => !p.capabilities?.[k]).map(([, l]) => l).join(', ')}</dd></div>
          {/if}
          {#if p.models?.length}<div><dt>Models</dt><dd>{p.models.map((m) => m.label || m.id).join(', ')}</dd></div>{/if}
        {/if}
      </dl>
      {#if p.limitations?.length}
        <p class="sub">Adapter notes</p>
        <ul class="notes">{#each p.limitations as l (l)}<li>{l}</li>{/each}</ul>
      {/if}
    </div>
  {/each}
</section>

<section class="block" aria-labelledby="md-profiles">
  <h3 id="md-profiles">Execution profiles</h3>
  {#if n.profiles.length === 0}<p class="meta">None reported.</p>{/if}
  <ul class="profiles">
    {#each n.profiles as pr (pr.name)}
      <li>
        <StateIcon shape={pr.available ? 'check-filled' : 'slash'} tone={pr.available ? 'success' : 'neutral'} size={14} />
        <div>
          <p><strong>{PROFILE_LABEL[pr.name] ?? pr.name}</strong> · {pr.available ? 'Available' : 'Unavailable'}</p>
          <p class="meta">{pr.available ? pr.summary : pr.reason || 'No reason given.'}</p>
        </div>
      </li>
    {/each}
  </ul>
</section>

<section class="block" aria-labelledby="md-tools">
  <h3 id="md-tools">Toolchains</h3>
  {#if toolchains.length === 0}
    <p class="meta">None reported.</p>
  {:else}
    <dl class="kv tools">
      {#each toolchains as [name, version] (name)}
        <div><dt>{name}</dt><dd class="mono">{version}</dd></div>
      {/each}
    </dl>
  {/if}
</section>

<section class="block" aria-labelledby="md-activity">
  <h3 id="md-activity">Activity</h3>
  <dl class="kv">
    <div><dt>Last heard</dt><dd>{n.lastSeenAt ? `${fullTime(n.lastSeenAt)} (${relative(n.lastSeenAt, app.now)})` : 'Never'}</dd></div>
    {#if n.lastActivity}<div><dt>Last reported</dt><dd>{n.lastActivity}</dd></div>{/if}
  </dl>
  {#if recent.length}
    <ul class="runs">
      {#each recent as r (r.id)}
        {@const j = app.data.jobs[r.jobId]}
        <li>
          <StateIcon shape={runShape(r.state)} tone={runTone(r.state)} size={13} />
          <span class="run-text">{runStateLabel(r.state)}{j ? ` · ${j.kind === 'reply' ? 'a reply' : j.title}` : ''}</span>
          <span class="meta">{relative(r.startedAt ?? r.createdAt, app.now)}</span>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="meta">No attempts on this machine since this page loaded.</p>
  {/if}
</section>

<style>
  .block {
    margin-bottom: 22px;
  }
  h3 {
    font-size: var(--text-body);
    font-weight: 600;
    margin-bottom: 8px;
  }
  .kv {
    margin: 0;
    display: grid;
    gap: 6px;
  }
  .kv > div {
    display: grid;
    grid-template-columns: 112px minmax(0, 1fr);
    gap: 12px;
    font-size: var(--text-body);
  }
  dt {
    color: var(--ink-secondary);
    overflow-wrap: anywhere;
  }
  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .mono {
    font-size: 12.5px;
    line-height: 20px;
  }
  .fp {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
  }
  .fp code {
    font-size: 13px;
    user-select: all;
  }
  .kv + .meta {
    margin-top: 6px;
  }
  .prov {
    padding: 10px 0;
    border-top: 1px solid var(--line);
  }
  .prov-name {
    margin-bottom: 6px;
    overflow-wrap: anywhere;
  }
  .sub {
    margin: 8px 0 4px;
    font-size: var(--text-meta);
    font-weight: 600;
    color: var(--ink-secondary);
  }
  .notes {
    margin: 0;
    padding-left: 18px;
    display: grid;
    gap: 3px;
    font-size: var(--text-body);
    color: var(--ink-secondary);
    overflow-wrap: anywhere;
  }
  .profiles,
  .runs {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 8px;
  }
  .profiles li {
    display: flex;
    gap: 8px;
    align-items: flex-start;
  }
  .profiles li :global(svg) {
    margin-top: 3px;
  }
  .profiles .meta {
    overflow-wrap: anywhere;
  }
  .runs {
    margin-top: 8px;
    gap: 4px;
  }
  .runs li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    gap: 8px;
    align-items: center;
    font-size: var(--text-body);
  }
  .run-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  @container machinepanel (max-width: 440px) {
    .kv > div {
      grid-template-columns: minmax(0, 1fr);
      gap: 0;
    }
  }
</style>
