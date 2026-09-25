<script lang="ts">
  // A modal side sheet for navigation on small screens.
  import { onMount, type Snippet } from 'svelte';
  import { pushLayer, trapFocus, focusFirst } from '../lib/ui/layers';
  import Icon from './Icon.svelte';

  interface Props {
    label: string;
    onclose: () => void;
    children: Snippet;
  }
  let { label, onclose, children }: Props = $props();
  let el: HTMLElement | undefined = $state();

  onMount(() => {
    const release = pushLayer(onclose);
    const untrap = el ? trapFocus(el) : () => {};
    queueMicrotask(() => el && focusFirst(el, '[aria-current="page"]'));
    return () => {
      untrap();
      release();
    };
  });
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="veil" onclick={onclose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label={label} bind:this={el}>
  <div class="head">
    <button class="icon-btn" aria-label="Close navigation" onclick={onclose}><Icon name="x" /></button>
  </div>
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="body" onclick={(e) => (e.target as HTMLElement).closest('a') && onclose()}>
    {@render children()}
  </div>
</div>

<style>
  .veil {
    position: fixed;
    inset: 0;
    z-index: 600;
    background: var(--veil);
    animation: fade var(--t-slow) var(--ease);
  }
  .sheet {
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 601;
    width: min(300px, 86vw);
    display: flex;
    flex-direction: column;
    background: var(--frame);
    background-color: var(--canvas);
    box-shadow: var(--shadow-pop);
    padding-top: env(safe-area-inset-top);
    padding-bottom: env(safe-area-inset-bottom);
    animation: slide var(--t-slow) var(--ease);
  }
  .head {
    display: flex;
    justify-content: flex-end;
    padding: 6px;
  }
  .body {
    flex: 1;
    min-height: 0;
  }
  @keyframes slide {
    from {
      transform: translateX(-16px);
      opacity: 0.5;
    }
  }
  @keyframes fade {
    from {
      opacity: 0;
    }
  }
</style>
