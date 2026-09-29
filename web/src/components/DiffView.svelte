<script lang="ts">
  // A unified diff with per-file headers and +/− lines. Additions and
  // deletions are marked by symbol and colour, never colour alone. This is a
  // domain renderer, so it keeps its own markup and reads Astryx tokens.
  import { Icon, Text } from '@astryx-svelte/core';
  import { filePath, type DiffFile } from '../lib/util/diff';

  interface Props {
    files: DiffFile[];
    /** A location to bring into view and mark (a review finding's file:line). */
    focus?: { file: string; line?: number; at: number } | null;
  }
  let { files, focus = null }: Props = $props();
  let collapsed = $state<Record<string, boolean>>({});
  let root: HTMLDivElement | undefined = $state();

  const matches = (path: string, file: string) => path === file || path.endsWith('/' + file) || file.endsWith('/' + path);
  const focusPath = $derived(focus ? files.map(filePath).find((p) => matches(p, focus!.file)) : undefined);

  $effect(() => {
    if (!focus || !focusPath || !root) return;
    void focus.at;
    collapsed[focusPath] = false;
    requestAnimationFrame(() => {
      const el =
        (focus?.line && root?.querySelector<HTMLElement>(`[data-path="${CSS.escape(focusPath)}"] [data-new="${focus.line}"]`)) ||
        root?.querySelector<HTMLElement>(`[data-path="${CSS.escape(focusPath)}"]`);
      el?.scrollIntoView({ block: 'center' });
    });
  });
</script>

{#if files.length === 0}
  <Text as="p" type="supporting">The diff is empty.</Text>
{:else}
  {#if focus && !focusPath}<Text as="p" type="supporting">{focus.file} isn't part of this diff.</Text>{/if}
  <div class="diff" bind:this={root}>
    {#each files as f (filePath(f))}
      {@const path = filePath(f)}
      <section class="file" aria-label="Changes in {path}" data-path={path}>
        <header>
          <button class="toggle" aria-expanded={!collapsed[path]} onclick={() => (collapsed[path] = !collapsed[path])}>
            <Icon icon={collapsed[path] ? 'chevronRight' : 'chevronDown'} size="sm" color="secondary" />
            <span class="path">{path}</span>
          </button>
          <span class="status">{f.status === 'modified' ? '' : f.status}</span>
          <span class="stats"><span class="add">+{f.additions}</span> <span class="del">−{f.deletions}</span></span>
        </header>
        {#if !collapsed[path]}
          {#if f.status === 'binary'}
            <p class="binary">Binary file — not shown.</p>
          {:else}
            <div class="code" role="table" aria-label="Diff of {path}">
              {#each f.hunks as h, hi (hi)}
                <div class="hunk" role="row"><span role="cell">{h.header}</span></div>
                {#each h.lines as l, li (li)}
                  <div
                    class="line {l.kind}"
                    class:focus={focusPath === path && !!focus?.line && l.newNo === focus.line}
                    role="row"
                    data-new={l.newNo ?? undefined}
                  >
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
    gap: var(--spacing-2);
  }
  .file {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    background: var(--color-background-card);
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    padding: var(--spacing-1) var(--spacing-3) var(--spacing-1) var(--spacing-1);
    background: var(--color-background-muted);
    border-bottom: 1px solid var(--color-border);
  }
  .toggle {
    display: flex;
    align-items: center;
    gap: var(--spacing-1-5);
    flex: 1;
    min-width: 0;
    min-height: var(--size-element-sm);
    padding: 0 var(--spacing-1-5);
    border-radius: var(--radius-element);
    color: var(--color-text-primary);
    text-align: left;
  }
  .toggle:hover {
    background: var(--color-overlay-hover);
  }
  .toggle:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: -2px;
  }
  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
  }
  .status {
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  .stats {
    font-size: var(--font-size-sm);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .add {
    color: var(--color-success);
    font-weight: var(--font-weight-semibold);
  }
  .del {
    color: var(--color-error);
    font-weight: var(--font-weight-semibold);
  }
  .binary {
    padding: var(--spacing-2) var(--spacing-3);
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  .code {
    overflow-x: auto;
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
    line-height: 1.6;
  }
  .hunk {
    padding: var(--spacing-0-5) var(--spacing-3);
    background: var(--color-background-muted);
    color: var(--color-text-secondary);
    white-space: pre;
  }
  .line {
    display: grid;
    grid-template-columns: 40px 40px 18px auto;
    min-width: max-content;
  }
  .line.focus {
    outline: 2px solid var(--color-accent);
    outline-offset: -2px;
  }
  .no {
    padding: 0 var(--spacing-1-5);
    text-align: right;
    color: var(--color-text-secondary);
    user-select: none;
    font-variant-numeric: tabular-nums;
    border-right: 1px solid var(--color-border);
  }
  .sign {
    text-align: center;
    user-select: none;
  }
  .text {
    white-space: pre;
    padding-right: var(--spacing-4);
  }
  .line.add {
    background: var(--color-success-muted);
  }
  .line.add .sign {
    color: var(--color-success);
    font-weight: var(--font-weight-bold);
  }
  .line.del {
    background: var(--color-error-muted);
  }
  .line.del .sign {
    color: var(--color-error);
    font-weight: var(--font-weight-bold);
  }
  .line.meta {
    color: var(--color-text-secondary);
    font-style: italic;
  }
</style>
