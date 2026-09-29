<script lang="ts">
  // The store (app.toast) owns which toasts exist and for how long; each one is
  // shown through Astryx's toast viewport while it is in the store.
  import { app } from '../lib/state/app.svelte';
  import ToastItem from './ToastItem.svelte';
</script>

{#each app.toasts as toast (toast.id)}
  <ToastItem {toast} />
{/each}
