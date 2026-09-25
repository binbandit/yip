<script lang="ts">
  // Engineers are squircles tinted by their hue; the human owner is a circle.
  // Shape is the only distinction — no presence dots implying someone is online.
  import { app } from '../lib/state/app.svelte';
  import type { Actor } from '../lib/api/types.gen';

  interface Props {
    actor: Actor | { kind: string; id: string };
    size?: number;
  }
  let { actor, size = 36 }: Props = $props();

  const eng = $derived(actor.kind === 'engineer' ? app.data.engineers[actor.id] : undefined);
  const name = $derived(app.actorName(actor as Actor));
  const initial = $derived((eng?.name ?? name ?? '?').trim().charAt(0).toUpperCase() || '?');
  const hue = $derived(eng?.hue ?? 190);
</script>

<span
  class="avatar"
  class:engineer={actor.kind === 'engineer'}
  class:human={actor.kind === 'user'}
  class:system={actor.kind === 'system' || actor.kind === 'node'}
  style:--size="{size}px"
  style:--h={hue}
  aria-hidden="true"
>
  {#if actor.kind === 'system' || actor.kind === 'node'}
    <svg viewBox="0 0 24 24" width={Math.round(size * 0.55)} height={Math.round(size * 0.55)} fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round">
      <path d="M7 5.5 12 11" /><path d="M17 5.5 7 18.5" />
    </svg>
  {:else}
    {initial}
  {/if}
</span>

<style>
  .avatar {
    display: inline-grid;
    place-items: center;
    width: var(--size);
    height: var(--size);
    flex: none;
    font-size: calc(var(--size) * 0.44);
    font-weight: 650;
    line-height: 1;
    user-select: none;
  }
  .engineer {
    border-radius: 32%;
    background: hsl(var(--h) var(--hue-s) var(--hue-l-bg));
    color: hsl(var(--h) 45% var(--hue-l-ink));
  }
  .human {
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-ink);
  }
  .system {
    border-radius: 30%;
    background: var(--surface-subtle);
    color: var(--ink-secondary);
    border: 1px solid var(--line);
  }
</style>
