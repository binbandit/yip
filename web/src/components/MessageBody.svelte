<script lang="ts">
  // Markdown-lite body. renderMarkdown escapes all input; only its own tags
  // (p, br, strong, em, code, pre, ul/ol/li, blockquote, a, span.mention) appear.
  import { renderMarkdown } from '../lib/util/markdown';
  import { app } from '../lib/state/app.svelte';
  import type { Message } from '../lib/api/types.gen';

  interface Props {
    message: Pick<Message, 'body' | 'mentions'>;
  }
  let { message }: Props = $props();
  const html = $derived(renderMarkdown(message.body, { mentions: app.handleMentionsFor(message as Message) }));
</script>

<div class="prose">{@html html}</div>
