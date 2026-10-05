<script lang="ts">
  import { app } from '../lib/state/app.svelte';
  import { conversationJobs } from '../lib/util/workActivity';
  import WorkActivityRow from './WorkActivityRow.svelte';

  let { roomId, threadId }: { roomId: string; threadId?: string } = $props();
  let open = $state(false);
  let now = $state(Date.now());
  const jobs = $derived(conversationJobs(app.data, roomId, threadId, Math.max(now, app.now)));
  $effect(() => {
    if (!open) return;
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(timer);
  });
</script>

{#if jobs.length}
  <section class="conversation-activity" aria-label="Engineer work">
    <button class="toggle" aria-expanded={open} onclick={() => { now = Date.now(); open = !open; }}>
      <span aria-hidden="true">{open ? '▾' : '▸'}</span> Engineer work <span class="count">· {jobs.length} {jobs.length === 1 ? 'task' : 'tasks'}</span>
    </button>
    {#if open}
      <div class="rows">
        {#key app.data}
          {#each jobs as job (`${job.id}:${job.currentRunId ?? ''}`)}
            <WorkActivityRow jobId={job.id} {roomId} {threadId} {now} />
          {/each}
        {/key}
      </div>
    {/if}
  </section>
{/if}

<style>
  .conversation-activity { flex: none; border-bottom: 1px solid var(--color-border); background: var(--color-background-surface); }
  .toggle { display: flex; gap: var(--spacing-2); align-items: baseline; padding: var(--spacing-2) var(--spacing-3); width: 100%; text-align: left; color: var(--color-text-primary); font-size: var(--text-supporting-size); font-weight: var(--font-weight-semibold); }
  .toggle:hover { background: var(--color-overlay-hover); }
  .toggle:focus-visible { outline: var(--focus-outline-width) var(--focus-outline-style) var(--focus-outline-color); outline-offset: -2px; }
  .count { color: var(--color-text-secondary); font-weight: var(--font-weight-normal); }
  .rows { max-height: min(40vh, 24rem); overflow: auto; }
</style>
