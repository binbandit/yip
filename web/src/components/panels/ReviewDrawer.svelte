<script lang="ts">
  import { untrack } from 'svelte';
  import { Button, Heading, Icon, Text } from '@astryx-svelte/core';
  import Notice from '../Notice.svelte';
  import { File } from '@lucide/svelte';
  import { app } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { workResultKey } from '../../lib/util/reviews';
  import { conversationHref } from '../../lib/util/conversation';
  import { errorMessage } from '../../lib/api/client';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import ReviewDetail from '../ReviewDetail.svelte';
  import PRFacts from '../PRFacts.svelte';

  interface Props {
    reviewId: string;
    mode: PanelMode;
  }
  let { reviewId, mode }: Props = $props();
  let error = $state('');

  $effect(() => {
    void app.data.touched.reviews[reviewId];
    untrack(() => details.ensureReview(reviewId, true).catch((e) => (error = errorMessage(e))));
  });

  const review = $derived(app.data.reviews[reviewId]);
  const job = $derived(review ? app.data.jobs[review.jobId] : undefined);
  $effect(() => {
    const id = review?.jobId;
    const touch = id ? app.data.touched.jobs[id] ?? 0 : 0;
    if (id) untrack(() => details.ensureJob(id, touch));
  });
  $effect(() => {
    const pr = review?.pullRequestId;
    if (pr) untrack(() => void details.ensurePR(pr).catch(() => {}));
  });
  const pr = $derived(review?.pullRequestId ? app.data.prs[review.pullRequestId] : undefined);
</script>

<RightPanel title={job ? `Review · ${job.title}` : 'Review'} {mode} wide onclose={() => app.closePanel()}>
  {#snippet subtitle()}{review ? `${app.engineerName(review.reviewerId)} reviewing ${app.engineerName(review.authorId)}` : ''}{/snippet}
  <div class="pad">
    {#if error && !review}
      <Notice tone="danger" role="alert">{error}</Notice>
    {:else if !review}
      <Text as="p" type="supporting">Loading the review…</Text>
    {:else}
      <div class="links">
        <Button size="sm" label="View evidence" onclick={() => app.openPanel({ kind: 'job', id: review.jobId }, 'evidence')}>
          {#snippet icon()}<Icon icon={File} size="sm" />{/snippet}
        </Button>
        <Button size="sm" variant="ghost" label="Source conversation" href={conversationHref(review.source)} />
      </div>
      <ReviewDetail {review} currentHead={job ? workResultKey(job, Object.values(app.data.artifacts)) : undefined} />
      {#if pr}
        <section class="pr-block">
          <Heading level={3}>Pull request</Heading>
          <PRFacts {pr} reviews={[review]} />
        </section>
      {/if}
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: var(--spacing-3) var(--spacing-4) var(--spacing-6);
    display: grid;
    gap: var(--spacing-4);
  }
  .links {
    display: flex;
    gap: var(--spacing-2);
    flex-wrap: wrap;
  }
  .pr-block {
    display: grid;
    gap: var(--spacing-2);
  }
  /* Section titles in a drawer sit below its 17px title. */
  .pr-block :global(h3.astryx-heading) {
    font-size: var(--text-heading-4-size);
    line-height: var(--text-heading-4-leading);
  }
</style>
