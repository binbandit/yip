<script lang="ts">
  // yip's dialog shape on Astryx's Dialog: mounting it opens it, and `onclose`
  // asks the parent to unmount it. A title (which takes focus on open and names
  // the dialog), an optional description (which describes it), the body, and
  // end-aligned actions.
  // Mark an element `data-autofocus` (TextInput's `hasAutoFocus`) to focus it
  // instead of the title.
  import type { Snippet } from 'svelte';
  import { Dialog, DialogHeader, HStack, Layout, LayoutContent, LayoutFooter, Text } from '@astryx-svelte/core';

  interface Props {
    title: string;
    description?: string;
    onclose: () => void;
    children: Snippet;
    footer?: Snippet;
    width?: number;
    /** `form` keeps a backdrop click from discarding input; `required` also ignores Escape. */
    purpose?: 'info' | 'form' | 'required';
  }
  let { title, description, onclose, children, footer: actions, width = 520, purpose = 'info' }: Props = $props();
  const id = $props.id();

  // Astryx returns focus to the invoker when a dialog closes itself; a parent
  // that unmounts it directly (after a save, say) gets the same courtesy here.
  const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  $effect(() => () => {
    const active = document.activeElement;
    if (invoker?.isConnected && (!active || active === document.body)) invoker.focus({ preventScroll: true });
  });

  const onOpenChange = (open: boolean) => {
    if (!open) onclose();
  };
</script>

{#snippet head()}
  <DialogHeader {title} {onOpenChange} />
{/snippet}

{#snippet body()}
  <LayoutContent>
    <!-- In the body rather than DialogHeader's subtitle, which can't carry the id. -->
    {#if description}<div class="description"><Text type="body" color="secondary" id="{id}-description">{description}</Text></div>{/if}
    {@render children()}
  </LayoutContent>
{/snippet}

{#snippet foot()}
  <LayoutFooter hasDivider>
    <HStack gap={2} hAlign="end" wrap="wrap">
      {@render actions?.()}
    </HStack>
  </LayoutFooter>
{/snippet}

<Dialog isOpen {onOpenChange} {width} {purpose} maxHeight="min(88dvh, 900px)" aria-describedby={description ? `${id}-description` : undefined}>
  <Layout header={head} content={body} footer={actions ? foot : undefined} />
</Dialog>

<style>
  .description {
    margin-bottom: var(--spacing-4);
  }
</style>
