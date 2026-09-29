<script lang="ts">
  // Changes a profile picture: the avatar as everyone sees it, a button that
  // opens the file picker, and one that goes back to the initial. A choice is
  // saved at once, like the other settings on these screens.
  import { tick } from 'svelte';
  import { Button, Icon, Text } from '@astryx-svelte/core';
  import { ImageUp } from '@lucide/svelte';
  import Avatar from './Avatar.svelte';
  import Notice from './Notice.svelte';
  import { app } from '../lib/state/app.svelte';
  import { errorMessage } from '../lib/api/client';
  import { PICTURE_HINT, PICTURE_TYPES, pictureProblem } from '../lib/util/avatars';

  interface Props {
    actor: { kind: 'user' | 'engineer'; id: string };
    hasPicture: boolean;
    /** Saves a new picture, or removes it with null; throws when the hub refuses. */
    onchange: (picture: File | null) => Promise<void>;
  }
  let { actor, hasPicture, onchange }: Props = $props();

  let input = $state<HTMLInputElement>();
  let buttons = $state<HTMLDivElement>();
  let busy = $state<'' | 'upload' | 'remove'>('');
  let error = $state('');

  async function apply(picture: File | null) {
    error = picture ? pictureProblem(picture) : '';
    if (error) return;
    busy = picture ? 'upload' : 'remove';
    try {
      await onchange(picture);
      app.announce(picture ? 'Picture saved.' : 'Picture removed.');
      if (!picture) {
        // The Remove button is gone; keep focus in the picker, not the page.
        await tick();
        buttons?.querySelector('button')?.focus();
      }
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = '';
    }
  }

  function picked(ev: Event & { currentTarget: HTMLInputElement }) {
    const file = ev.currentTarget.files?.[0];
    ev.currentTarget.value = ''; // so choosing the same file again still counts
    if (file) void apply(file);
  }
</script>

<div class="picker">
  <Avatar {actor} size={64} />
  <div class="controls">
    <div class="row" bind:this={buttons}>
      <Button
        label={hasPicture ? 'Change picture' : 'Upload a picture'}
        size="sm"
        isLoading={busy === 'upload'}
        isDisabled={!!busy}
        onclick={() => input?.click()}
      >
        {#snippet icon()}<Icon icon={ImageUp} size="sm" />{/snippet}
      </Button>
      {#if hasPicture}
        <Button label="Remove picture" size="sm" variant="ghost" isLoading={busy === 'remove'} isDisabled={!!busy} onclick={() => apply(null)} />
      {/if}
    </div>
    <Text as="p" display="block" type="supporting">{PICTURE_HINT}</Text>
    {#if error}<Notice tone="danger" role="alert">{error}</Notice>{/if}
  </div>
  <input bind:this={input} type="file" accept={PICTURE_TYPES.join(',')} hidden onchange={picked} />
</div>

<style>
  .picker {
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-4);
  }
  .controls {
    display: grid;
    gap: var(--spacing-2);
    justify-items: start;
    min-width: 0;
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-2);
  }
</style>
