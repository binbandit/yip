<script lang="ts">
  // Reactions are social only; they never approve or trigger anything.
  import { ToggleButton } from '@astryx-svelte/core';
  import { api } from '../lib/api/endpoints';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import type { Message } from '../lib/api/types.gen';

  interface Props {
    message: Message;
  }
  let { message }: Props = $props();

  async function toggle(emoji: string, mine: boolean) {
    try {
      const m = await api.react(message.id, emoji, mine);
      const cur = app.data.messages[m.id];
      if (!cur || m.revision >= cur.revision) app.data.messages[m.id] = { ...m, mentions: m.mentions ?? [], refs: m.refs ?? [], reactions: m.reactions ?? [], projectIds: m.projectIds ?? [] };
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    }
  }
</script>

{#if message.reactions.length}
  <ul class="reactions" aria-label="Reactions">
    {#each message.reactions as r (r.emoji)}
      <li>
        <!-- The pressed state flips at once; the hub's answer settles it. -->
        <ToggleButton
          label="{r.emoji} {r.count}{r.mine ? ', including you' : ''}. {r.mine ? 'Remove your reaction' : 'React'}"
          size="sm"
          isPressed={r.mine}
          pressedChangeAction={() => toggle(r.emoji, r.mine)}
        >
          <span class="emoji" aria-hidden="true">{r.emoji}</span>
          <span class="n" aria-hidden="true">{r.count}</span>
        </ToggleButton>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .reactions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1-5);
    list-style: none;
    margin: var(--spacing-1-5) 0 0;
    padding: 0;
  }
  /* A full pill with a hairline edge; yours carries the accent. These replace
     Astryx's own fills, so hover and press are tinted here too. Touch screens
     get the app-wide 44px height. */
  .reactions :global(.astryx-toggle-button) {
    height: 26px;
    padding-inline: var(--spacing-2);
    border-radius: var(--radius-full);
    box-shadow: inset 0 0 0 1px var(--color-border);
    background-color: var(--color-background-surface);
  }
  .reactions :global(.astryx-toggle-button:hover) {
    box-shadow: inset 0 0 0 1px var(--color-border-emphasized);
    background-image: linear-gradient(var(--color-overlay-hover), var(--color-overlay-hover));
  }
  .reactions :global(.astryx-toggle-button:active) {
    background-image: linear-gradient(var(--color-overlay-pressed), var(--color-overlay-pressed));
  }
  .reactions :global(.astryx-toggle-button[aria-pressed='true']) {
    box-shadow: inset 0 0 0 1px var(--color-accent);
    background-color: var(--color-accent-muted);
  }
  .emoji {
    font-size: var(--font-size-base);
  }
  .n {
    margin-left: var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-semibold);
    font-variant-numeric: tabular-nums;
    color: var(--color-text-secondary);
  }
  .reactions :global([aria-pressed='true'] .n) {
    color: var(--color-text-primary);
  }
</style>
