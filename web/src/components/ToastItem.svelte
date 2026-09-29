<script lang="ts">
  // One store toast in Astryx's viewport: shown on mount, dismissed on unmount.
  // Closing it in the viewport removes it from the store.
  import { onMount, type Component } from 'svelte';
  import { Button, Icon, useToast } from '@astryx-svelte/core';
  import { Check, CircleAlert } from '@lucide/svelte';
  import { app, type Toast } from '../lib/state/app.svelte';

  let { toast }: { toast: Toast } = $props();
  const show = useToast();

  // Errors and confirmations lead with their tone's shape; info is plain text.
  const LEAD: Record<Exclude<Toast['tone'], 'info'>, { icon: Component; color: 'inherit' | 'success' }> = {
    error: { icon: CircleAlert, color: 'inherit' },
    success: { icon: Check, color: 'success' },
  };

  onMount(() =>
    show({
      body,
      type: toast.tone === 'error' ? 'error' : 'info',
      // The store times toasts out; the viewport only removes one on request.
      isAutoHide: false,
      endContent: toast.action ? action : undefined,
      onHide: () => app.dismissToast(toast.id),
    }),
  );
</script>

{#snippet body()}
  <span class="text">
    {#if toast.tone !== 'info'}
      {@const lead = LEAD[toast.tone]}
      <span class="shape"><Icon icon={lead.icon} size="sm" color={lead.color} /></span>
    {/if}
    <span>{toast.text}</span>
  </span>
{/snippet}

{#snippet action()}
  <Button
    label={toast.action?.label ?? ''}
    size="sm"
    onclick={() => {
      toast.action?.run();
      app.dismissToast(toast.id);
    }}
  />
{/snippet}

<style>
  .text {
    display: flex;
    gap: var(--spacing-2);
  }
  .shape {
    display: inline-flex;
    flex: none;
    margin-top: calc((1lh - 16px) / 2);
  }
</style>
