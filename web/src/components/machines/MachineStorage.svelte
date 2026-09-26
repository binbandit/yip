<script lang="ts">
  // Storage: the workspaces on this machine, what kind each is, how big it
  // is (honestly: "under 1 MB", "at least 2.4 GB", "size not measured"),
  // whether deleting it loses anything, and the delete action in one
  // trailing column. Nothing is deleted without a confirmation, and open or
  // in-use work is protected (the hub refuses those too).
  import type { Node, NodeWorkspace } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { bytes, fullTime, relative } from '../../lib/util/time';
  import { LOW_DISK_MB, storageTotal, workspaceKind, workspaceSize, workspaceStatus, workspaceTitle } from '../../lib/util/machines';
  import Icon from '../Icon.svelte';

  interface Props {
    node: Node;
    onrequest: (kind: 'workspace', ws: NodeWorkspace) => void;
  }
  let { node: n, onrequest }: Props = $props();

  const ws = $derived(n.workspaces ?? []);
  const online = $derived(n.status === 'online');
  const lowDisk = $derived(n.capacity.diskPressure || (n.capacity.diskFreeMb > 0 && n.capacity.diskFreeMb < LOW_DISK_MB));
</script>

<div class="summary">
  <h3 id="machine-storage-heading" tabindex="-1">Workspaces</h3>
  <p>
    {ws.length} {ws.length === 1 ? 'workspace' : 'workspaces'} · {storageTotal(ws)}
    {#if n.capacity.diskFreeMb > 0}<span class:low={lowDisk}> · {bytes(n.capacity.diskFreeMb * 1024 * 1024)} free{lowDisk ? ', low' : ''}</span>{/if}
  </p>
  <p class="explain">Workspaces stay until you delete them. Deleting one removes its files from this machine only; the work’s record, revisions and reviews stay on the hub.</p>
  {#if lowDisk}
    <p class="notice attention">Low on disk space: it takes no new work until more than 2 GB is free.</p>
  {/if}
  {#if !online && n.status !== 'revoked' && ws.length}
    <p class="notice">It deletes workspaces itself, so it has to be connected first. This list is from its last report{n.lastSeenAt ? ` (${relative(n.lastSeenAt, app.now)})` : ''}.</p>
  {/if}
</div>

{#if ws.length === 0}
  <p class="meta">No workspaces on this machine.</p>
{:else}
  <ul class="wss" aria-labelledby="machine-storage-heading">
    {#each ws as w (w.name)}
      {@const kind = workspaceKind(w)}
      {@const title = workspaceTitle(w)}
      {@const status = workspaceStatus(w)}
      <li class="ws">
        <div class="ws-main">
          {#if w.jobId}
            <button class="ws-title" {title} onclick={() => app.openPanel({ kind: 'job', id: w.jobId! })}>{title}</button>
          {:else}
            <p class="ws-title" {title}>{title}</p>
          {/if}
          <p class="meta">
            <span class="kind">{kind.label}</span> · {workspaceSize(w)} · <span title={fullTime(w.modifiedAt)}>changed {relative(w.modifiedAt, app.now)}</span>
          </p>
          <p class="status tone-{status.tone}">
            {#if status.protected}<Icon name="lock" size={13} />{:else if status.tone === 'attention'}<Icon name="alert" size={13} />{/if}{status.text}
          </p>
          <p class="where mono" title={[w.name, w.branch].filter(Boolean).join(' · ')}>{w.name}{w.branch ? ` · ${w.branch}` : ''}</p>
        </div>
        <div class="ws-action">
          {#if status.protected}
            <span class="protected" title={w.blocked}><Icon name="lock" size={14} />Protected</span>
          {:else if online}
            <button class="btn btn-sm" aria-label="Delete {kind.label.toLowerCase()} {w.name}" onclick={() => onrequest('workspace', w)}>Delete…</button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .summary {
    display: grid;
    gap: 6px;
    margin-bottom: 14px;
  }
  h3 {
    font-size: var(--text-body);
    font-weight: 600;
  }
  h3:focus-visible {
    outline-offset: 2px;
  }
  .explain {
    color: var(--ink-secondary);
  }
  .low {
    font-weight: 600;
  }
  .wss {
    list-style: none;
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--line);
  }
  .ws {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 96px;
    gap: 12px;
    align-items: start;
    padding: 12px 0;
    border-bottom: 1px solid var(--line);
  }
  .ws-main {
    display: grid;
    gap: 2px;
    min-width: 0;
    justify-items: start;
    text-align: left;
  }
  .ws-title {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    max-width: 100%;
    padding: 0;
    border: 0;
    background: none;
    color: var(--ink);
    font: inherit;
    font-size: var(--text-body);
    font-weight: 500;
    line-height: 20px;
    text-align: left;
    overflow-wrap: anywhere;
  }
  button.ws-title {
    cursor: pointer;
  }
  button.ws-title:hover {
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  .kind {
    color: var(--ink);
    font-weight: 500;
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    font-size: var(--text-meta);
    line-height: 1.45;
  }
  .status.tone-neutral {
    color: var(--ink-secondary);
  }
  .where {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    color: var(--ink-secondary);
  }
  .ws-action {
    display: flex;
    justify-content: flex-end;
  }
  .protected {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    min-height: 28px;
    font-size: var(--text-meta);
    color: var(--ink-secondary);
  }
</style>
