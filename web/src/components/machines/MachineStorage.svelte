<script lang="ts">
  // Storage: the workspaces on this machine, what kind each is, how big it
  // is (honestly: "under 1 MB", "at least 2.4 GB", "size not measured"),
  // whether deleting it loses anything, and the delete action in one
  // trailing column. Nothing is deleted without a confirmation, and open or
  // in-use work is protected (the hub refuses those too).
  import { Button, Heading, Icon, Text } from '@astryx-svelte/core';
  import { Lock, TriangleAlert } from '@lucide/svelte';
  import type { Node, NodeWorkspace } from '../../lib/api/types.gen';
  import { app } from '../../lib/state/app.svelte';
  import { bytes, fullTime, relative } from '../../lib/util/time';
  import { LOW_DISK_MB, storageTotal, workspaceKind, workspaceSize, workspaceStatus, workspaceTitle } from '../../lib/util/machines';
  import Notice from '../Notice.svelte';

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
  <Heading level={3} id="machine-storage-heading" tabindex={-1}>Workspaces</Heading>
  <p>
    {ws.length} {ws.length === 1 ? 'workspace' : 'workspaces'} · {storageTotal(ws)}
    {#if n.capacity.diskFreeMb > 0}<span class:low={lowDisk}> · {bytes(n.capacity.diskFreeMb * 1024 * 1024)} free{lowDisk ? ', low' : ''}</span>{/if}
  </p>
  <p class="explain">Workspaces stay until you delete them. Deleting one removes its files from this machine only; the work’s record, revisions and reviews stay on the hub.</p>
  {#if lowDisk}
    <Notice tone="warning" title="Low on disk space" description="It takes no new work until more than 2 GB is free." />
  {/if}
  {#if !online && n.status !== 'revoked' && ws.length}
    <Notice
      title="It deletes workspaces itself, so it has to be connected first."
      description="This list is from its last report{n.lastSeenAt ? ` (${relative(n.lastSeenAt, app.now)})` : ''}."
    />
  {/if}
</div>

{#if ws.length === 0}
  <Text as="p" type="supporting">No workspaces on this machine.</Text>
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
            {#if status.protected}<Icon icon={Lock} size="xsm" />{:else if status.tone === 'attention'}<Icon icon={TriangleAlert} size="xsm" />{/if}{status.text}
          </p>
          <p class="where" title={[w.name, w.branch].filter(Boolean).join(' · ')}>{w.name}{w.branch ? ` · ${w.branch}` : ''}</p>
        </div>
        <div class="ws-action">
          {#if status.protected}
            <span class="protected" title={w.blocked}><Icon icon={Lock} size="xsm" />Protected</span>
          {:else if online}
            <Button label="Delete {kind.label.toLowerCase()} {w.name}" size="sm" onclick={() => onrequest('workspace', w)}>Delete…</Button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .summary {
    display: grid;
    gap: var(--spacing-1-5);
    margin-bottom: var(--spacing-4);
  }
  .summary :global(h3) {
    font-size: var(--font-size-base);
    font-weight: var(--font-weight-semibold);
  }
  .summary :global(h3:focus-visible) {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
    border-radius: var(--radius-inner);
  }
  .explain {
    color: var(--color-text-secondary);
  }
  .low {
    font-weight: var(--font-weight-semibold);
  }
  .wss {
    list-style: none;
    margin: 0;
    padding: 0;
    border-top: 1px solid var(--color-border);
  }
  /* The action sits in one trailing column shared by every row. */
  .ws {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 96px;
    gap: var(--spacing-3);
    align-items: start;
    padding: var(--spacing-3) 0;
    border-bottom: 1px solid var(--color-border);
  }
  .ws-main {
    display: grid;
    gap: var(--spacing-0-5);
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
    color: var(--color-text-primary);
    font: inherit;
    font-weight: var(--font-weight-medium);
    line-height: 20px;
    text-align: left;
    overflow-wrap: anywhere;
  }
  button.ws-title {
    cursor: pointer;
    border-radius: var(--radius-inner);
  }
  button.ws-title:hover {
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  button.ws-title:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
  .meta {
    font-size: var(--font-size-sm);
    line-height: 1.45;
    color: var(--color-text-secondary);
  }
  .kind {
    color: var(--color-text-primary);
    font-weight: var(--font-weight-medium);
  }
  .status {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1);
    font-size: var(--font-size-sm);
    line-height: 1.45;
  }
  .status.tone-neutral {
    color: var(--color-text-secondary);
  }
  .status.tone-attention {
    color: var(--color-warning);
  }
  .status.tone-success {
    color: var(--color-success);
  }
  .where {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  .ws-action {
    display: flex;
    justify-content: flex-end;
  }
  .protected {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1);
    min-height: 28px;
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
</style>
