<script lang="ts">
  import { onMount } from 'svelte';
  import { VisuallyHidden } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { replyActivity, replyActivityText } from '../lib/util/replyActivity';

  let { roomId, threadId }: { roomId: string; threadId?: string } = $props();
  let now = $state(Date.now());
  onMount(() => {
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(timer);
  });
  const groups = $derived(app.online && app.connection === 'live' ? replyActivity(app.data, roomId, threadId, now) : []);
  const text = $derived(replyActivityText(groups, (id) => app.engineerName(id)));
  // Announce the participants once, rather than every writing/pause transition.
  const participants = $derived([...new Set(groups.flatMap((g) => g.engineerIds))].sort());
  const announcement = $derived(participants.length ? `Reply activity from ${participants.map((id) => app.engineerName(id)).join(', ')}.` : '');
</script>

<VisuallyHidden role="status" aria-live="polite" aria-atomic="true">{announcement}</VisuallyHidden>
{#if text}
  <span class="activity" aria-label="Engineer activity">
    {text}{#if groups.some((g) => g.phase === 'writing')}<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>{/if}
  </span>
{/if}

<style>
  .activity { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dots { display: inline-flex; gap: 2px; margin-left: 3px; vertical-align: middle; }
  .dots i { width: 3px; height: 3px; border-radius: var(--radius-full); background: currentColor; animation: blink 1.2s infinite ease-in-out; }
  .dots i:nth-child(2) { animation-delay: 0.15s; }
  .dots i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes blink { 0%, 80%, 100% { opacity: 0.25; } 40% { opacity: 1; } }
  @media (prefers-reduced-motion: reduce) { .dots i { animation: none; } }
</style>
