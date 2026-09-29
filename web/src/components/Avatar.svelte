<script lang="ts">
  // Engineers are rounded squares tinted by their hue, the human owner is a
  // circle, and yip itself (system and machine messages) wears its mark. Shape
  // is the only distinction: no presence dots implying someone is online.
  // A profile picture, when one is set, fills the same shape; GIFs animate
  // unless reduced motion is preferred, when their first frame stands still.
  // Avatars sit beside the name they belong to, so they're hidden from
  // assistive tech (Astryx names any avatar with a `name`, hence the wrapper).
  import { Avatar, type AvatarSize } from '@astryx-svelte/core';
  import { prefersReducedMotion } from 'svelte/motion';
  import { app } from '../lib/state/app.svelte';
  import { avatarUrl } from '../lib/api/endpoints';
  import { stillFrame } from '../lib/util/avatars';
  import type { Actor } from '../lib/api/types.gen';

  interface Props {
    actor: Actor | { kind: string; id: string };
    size?: AvatarSize;
  }
  let { actor, size = 36 }: Props = $props();

  const yip = $derived(actor.kind === 'system' || actor.kind === 'node');
  const eng = $derived(actor.kind === 'engineer' ? app.data.engineers[actor.id] : undefined);
  const name = $derived(yip ? 'yip' : (eng?.name ?? app.actorName(actor as Actor) ?? '?'));
  const kind = $derived(yip ? 'yip-mark-avatar' : actor.kind === 'engineer' ? 'yip-engineer-avatar' : actor.kind === 'user' ? 'yip-owner-avatar' : '');

  const pictureId = $derived(eng ? eng.avatarId : actor.kind === 'user' && actor.id === app.me?.id ? app.me.avatarId : undefined);
  const picture = $derived(pictureId ? avatarUrl(pictureId) : undefined);
  // The still for `picture`, once made (the initial shows meanwhile).
  let still = $state<{ of: string; src: string | undefined }>();
  $effect(() => {
    const url = picture;
    if (!url || !prefersReducedMotion.current) return;
    let current = true;
    void stillFrame(url).then((src) => {
      if (current) still = { of: url, src };
    });
    return () => {
      current = false;
    };
  });
  const src = $derived(!picture || !prefersReducedMotion.current ? picture : still?.of === picture ? still.src : undefined);
</script>

<span class="yip-avatar" aria-hidden="true">
  <Avatar {name} {src} {size} shape={actor.kind === 'user' ? 'circle' : 'rounded'} tooltip={false} class={kind} style="--yip-hue: {eng?.hue ?? 190}" />
</span>

<style>
  .yip-avatar {
    display: inline-flex;
    flex: none;
  }
  /* Engineers: the initial on a tint of their hue (contrast in scripts/contrast.mjs). */
  :global(.yip-engineer-avatar .astryx-avatar-fallback) {
    background: light-dark(hsl(var(--yip-hue) 34% 91%), hsl(var(--yip-hue) 26% 26%));
    color: light-dark(hsl(var(--yip-hue) 45% 27%), hsl(var(--yip-hue) 45% 88%));
  }
  /* The owner: ink on the accent, like every other primary mark. */
  :global(.yip-owner-avatar .astryx-avatar-fallback) {
    background: var(--color-accent);
    color: var(--color-on-accent);
  }
  /* yip: its two-stroke mark in place of an initial. */
  :global(.yip-mark-avatar .astryx-avatar-fallback) {
    position: relative;
    background: var(--color-background-muted);
    color: transparent;
    box-shadow: inset 0 0 0 1px var(--color-border);
  }
  :global(.yip-mark-avatar .astryx-avatar-fallback)::after {
    content: '';
    position: absolute;
    inset: 22%;
    background: var(--color-text-secondary);
    mask: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='black' stroke-width='2.2' stroke-linecap='round'%3E%3Cpath d='M7 5.5 12 11'/%3E%3Cpath d='M17 5.5 7 18.5'/%3E%3C/svg%3E") center / contain no-repeat;
  }
</style>
