<script lang="ts">
  import type { Panel } from '../lib/router';
  import type { PanelMode } from './RightPanel.svelte';
  import ThreadPanel from './panels/ThreadPanel.svelte';
  import JobDrawer from './panels/JobDrawer.svelte';
  import ReviewDrawer from './panels/ReviewDrawer.svelte';
  import PRDrawer from './panels/PRDrawer.svelte';
  import EngineerDrawer from './panels/EngineerDrawer.svelte';
  import DecisionDrawer from './panels/DecisionDrawer.svelte';
  import RoomSettings from './panels/RoomSettings.svelte';

  interface Props {
    panel: Panel;
    mode: PanelMode;
  }
  let { panel, mode }: Props = $props();
</script>

{#if panel.kind === 'thread'}
  <ThreadPanel rootId={panel.id} {mode} />
{:else if panel.kind === 'job'}
  <JobDrawer jobId={panel.id} {mode} />
{:else if panel.kind === 'review'}
  <ReviewDrawer reviewId={panel.id} {mode} />
{:else if panel.kind === 'pr'}
  <PRDrawer prId={panel.id} {mode} />
{:else if panel.kind === 'engineer'}
  <EngineerDrawer engineerId={panel.id} {mode} />
{:else if panel.kind === 'decision'}
  <DecisionDrawer decisionId={panel.id} {mode} />
{:else if panel.kind === 'room'}
  <RoomSettings roomId={panel.id} {mode} />
{/if}
