<script lang="ts">
  // A unified diff with per-file headers and +/− lines. Additions and
  // deletions are marked by symbol and colour, never colour alone.
  import { filePath, type DiffFile } from '../lib/util/diff';
  import Icon from './Icon.svelte';

  interface Props {
    files: DiffFile[];
  }
  let { files }: Props = $props();
  let collapsed = $state<Record<string, boolean>>({});
</script>

{#if files.length === 0}
  <p class="meta">The diff is empty.</p>
{:else}
  <div class="diff">
    {#each files as f (filePath(f))}
      {@const path = filePath(f)}
      <section class="file" aria-label="Changes in {path}">
        <header>
          <button class="toggle" aria-expanded={!collapsed[path]} onclick={() => (collapsed[path] = !collapsed[path])}>
            <Icon name={collapsed[path] ? 'chevronRight' : 'chevronDown'} size={15} />
            <span class="path mono">{path}</span>
          </button>
          <span class="status">{f.status === 'modified' ? '' : f.status}</span>
          <span class="stats"><span class="add">+{f.additions}</span> <span class="del">−{f.deletions}</span></span>
        </header>
        {#if !collapsed[path]}
          {#if f.status === 'binary'}
            <p class="meta pad">Binary file — not shown.</p>
          {:else}
            <div class="code" role="table" aria-label="Diff of {path}">
              {#each f.hunks as h, hi (hi)}
                <div class="hunk" role="row"><span role="cell" class="mono">{h.header}</span></div>
                {#each h.lines as l, li (li)}
                  <div class="line {l.kind}" role="row">
                    <span class="no" role="cell" aria-label={l.oldNo ? `old line ${l.oldNo}` : undefined}>{l.oldNo ?? ''}</span>
                    <span class="no" role="cell" aria-label={l.newNo ? `new line ${l.newNo}` : undefined}>{l.newNo ?? ''}</span>
                    <span class="sign" role="cell" aria-label={l.kind === 'add' ? 'added' : l.kind === 'del' ? 'removed' : undefined}
                      >{l.kind === 'add' ? '+' : l.kind === 'del' ? '−' : ' '}</span
                    >
                    <span class="text" role="cell">{l.text}</span>
                  </div>
                {/each}
              {/each}
            </div>
          {/if}
        {/if}
      </section>
    {/each}
  </div>
{/if}

<style>
  .diff {
    display: grid;
    gap: 10px;
  }
  .file {
    border: 1px solid var(--line);
    border-radius: var(--r-artifact);
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 10px 4px 4px;
    background: var(--surface-subtle);
    border-bottom: 1px solid var(--line);
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    min-height: 30px;
    padding: 0 6px;
    border: 0;
    background: none;
    color: var(--ink);
    cursor: pointer;
    text-align: left;
  }
  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
  }
  .status {
    font-size: 12px;
    color: var(--ink-secondary);
  }
  .stats {
    font-size: 12.5px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .add {
    color: var(--success);
    font-weight: 600;
  }
  .del {
    color: var(--danger);
    font-weight: 600;
  }
  .pad {
    padding: 10px;
  }
  .code {
    overflow-x: auto;
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 1.55;
  }
  .hunk {
    padding: 2px 10px;
    background: var(--accent-subtle);
    color: var(--ink-secondary);
    white-space: pre;
  }
  .line {
    display: grid;
    grid-template-columns: 40px 40px 18px auto;
    min-width: max-content;
  }
  .no {
    padding: 0 6px;
    text-align: right;
    color: var(--ink-secondary);
    user-select: none;
    font-variant-numeric: tabular-nums;
    border-right: 1px solid var(--line-soft);
  }
  .sign {
    text-align: center;
    user-select: none;
  }
  .text {
    white-space: pre;
    padding-right: 16px;
  }
  .line.add {
    background: var(--success-subtle);
  }
  .line.add .sign {
    color: var(--success);
    font-weight: 700;
  }
  .line.del {
    background: var(--danger-subtle);
  }
  .line.del .sign {
    color: var(--danger);
    font-weight: 700;
  }
  .line.meta {
    color: var(--ink-secondary);
    font-style: italic;
  }
</style>
