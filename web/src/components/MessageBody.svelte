<script lang="ts">
  // Markdown-lite body. renderMarkdown escapes all input; only its own tags
  // (p, br, strong, em, code, pre, ul/ol/li, blockquote, a, span.mention) appear.
  // yip keeps its own renderer rather than Astryx's Markdown: chat text is full
  // of paths like __tests__ and plain arithmetic, which a general Markdown
  // parser turns into emphasis, and line breaks inside a message must survive.
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

<style>
  /* The markup comes from renderMarkdown, so these reach it globally from the root. */
  .prose {
    overflow-wrap: anywhere;
  }
  .prose :global(:is(p, ul, ol, pre, blockquote) + :is(p, ul, ol, pre, blockquote)) {
    margin-top: var(--spacing-2);
  }
  .prose :global(ul),
  .prose :global(ol) {
    margin: var(--spacing-1) 0 0;
    padding-inline-start: 22px;
  }
  .prose :global(ul) {
    list-style: disc;
  }
  .prose :global(ol) {
    list-style: decimal;
  }
  .prose :global(li + li) {
    margin-top: 3px;
  }
  .prose :global(a) {
    color: inherit;
    text-decoration: underline;
    text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
    text-underline-offset: 2px;
  }
  .prose :global(a:hover) {
    text-decoration-color: currentColor;
  }
  .prose :global(code) {
    padding: 1px 5px;
    border-radius: var(--radius-inner);
    background: color-mix(in srgb, var(--color-text-primary) 7%, transparent);
    font-family: var(--font-family-code);
    font-size: var(--font-size-sm);
  }
  .prose :global(pre) {
    margin: var(--spacing-2) 0 0;
    padding: var(--spacing-2) var(--spacing-3);
    border-radius: var(--radius-container);
    background: var(--color-background-muted);
    overflow: auto;
    max-height: 400px;
    font-size: var(--font-size-sm);
    line-height: 1.5;
  }
  .prose :global(pre code) {
    padding: 0;
    background: none;
  }
  .prose :global(blockquote) {
    margin: var(--spacing-1-5) 0 0;
    padding-inline-start: var(--spacing-3);
    border-inline-start: 2px solid var(--color-border-emphasized);
    color: var(--color-text-secondary);
  }
  .prose :global(.mention) {
    padding: 0 var(--spacing-1);
    border-radius: var(--radius-inner);
    background: color-mix(in srgb, var(--color-text-primary) 7%, transparent);
    color: var(--color-text-primary);
    font-weight: var(--font-weight-semibold);
  }
  /* You were asked something: the same amber wash as a question for you. */
  .prose :global(.mention[data-kind='user']) {
    background: var(--color-warning-muted);
    color: var(--color-warning);
  }
</style>
