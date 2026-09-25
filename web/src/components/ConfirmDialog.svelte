<script lang="ts">
  import Dialog from './Dialog.svelte';
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

<Dialog {title} {onclose} width={460} initialFocus="[data-cancel]">
  <p>{body}</p>
  {#if error}<p class="form-error" role="alert" style="margin-top:12px">{error}</p>{/if}
  {#snippet footer()}
    <button class="btn" data-cancel onclick={onclose}>Cancel</button>
    <button class="btn {danger ? 'btn-danger-solid' : 'btn-primary'}" disabled={busy} onclick={confirm}>{busy ? 'Working…' : confirmLabel}</button>
  {/snippet}
</Dialog>
