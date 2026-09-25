<script lang="ts">
  // Reactions are social only; they never approve or trigger anything.
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
        <button
          class="reaction"
          class:mine={r.mine}
          aria-pressed={r.mine}
          aria-label="{r.emoji} {r.count}{r.mine ? ', including you' : ''}. {r.mine ? 'Remove your reaction' : 'React'}"
          onclick={() => toggle(r.emoji, r.mine)}
        >
          <span aria-hidden="true">{r.emoji}</span>
          <span class="n" aria-hidden="true">{r.count}</span>
        </button>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .reactions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
  }
  .reaction {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 26px;
    padding: 0 9px;
    border-radius: var(--r-pill);
    border: 1px solid var(--line);
    background: var(--surface);
    font-size: 13px;
    cursor: pointer;
  }
  .reaction:hover {
    border-color: var(--control-edge);
  }
  .reaction.mine {
    border-color: var(--accent);
    background: var(--accent-subtle);
  }
  .n {
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    color: var(--ink-secondary);
  }
  .mine .n {
    color: var(--accent);
  }
  @media (pointer: coarse) {
    .reaction {
      height: 36px;
    }
  }
</style>
