<script lang="ts">
  // A confirmed action, shaped like Astryx's AlertDialog: Cancel takes focus
  // and the action shows its progress. AlertDialog has no room for an error,
  // and a toast would sit behind the modal, so a failure is said here, inside
  // the dialog, which stays open.
  import { Button, Dialog, Heading, HStack, Layout, LayoutContent, LayoutFooter, Text, VStack } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { errorMessage } from '../lib/api/client';

  interface Props {
    title: string;
    body: string;
    confirmLabel: string;
    danger?: boolean;
    onconfirm: () => Promise<void> | void;
    onclose: () => void;
  }
  let { title, body, confirmLabel, danger = false, onconfirm, onclose }: Props = $props();
  let busy = $state(false);
  let error = $state('');
  const id = $props.id();

  // The parent unmounts the dialog when it closes, so return focus to the
  // control that opened it here (as Dialog does).
  const invoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  $effect(() => () => {
    const active = document.activeElement;
    if (invoker?.isConnected && (!active || active === document.body)) invoker.focus({ preventScroll: true });
  });

  async function confirm() {
    busy = true;
    error = '';
    try {
      await onconfirm();
      onclose();
    } catch (err) {
      error = errorMessage(err);
      busy = false;
    }
  }
</script>

{#snippet content()}
  <LayoutContent>
    <VStack gap={2}>
      <Heading level={2} id="{id}-title">{title}</Heading>
      <Text type="body" color="secondary" id="{id}-description">{body}</Text>
      {#if error}<div class="error"><Notice tone="danger" role="alert">{error}</Notice></div>{/if}
    </VStack>
  </LayoutContent>
{/snippet}

{#snippet footer()}
  <LayoutFooter>
    <HStack gap={2} hAlign="end">
      <Button variant="ghost" label="Cancel" onclick={onclose} data-autofocus />
      <Button variant={danger ? 'destructive' : 'primary'} label={confirmLabel} onclick={confirm} isLoading={busy} />
    </HStack>
  </LayoutFooter>
{/snippet}

<Dialog
  isOpen
  onOpenChange={(open) => !open && onclose()}
  width={460}
  purpose="form"
  role="alertdialog"
  aria-labelledby="{id}-title"
  aria-describedby="{id}-description"
>
  <Layout {content} {footer} />
</Dialog>

<style>
  .error {
    margin-top: var(--spacing-2);
  }
</style>
