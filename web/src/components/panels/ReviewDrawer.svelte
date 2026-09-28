<script lang="ts">
  import { untrack } from 'svelte';
  import { app } from '../../lib/state/app.svelte';
  import { details } from '../../lib/state/details.svelte';
  import { workResultKey } from '../../lib/util/reviews';
  import { conversationHref } from '../../lib/util/conversation';
  import { errorMessage } from '../../lib/api/client';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import ReviewDetail from '../ReviewDetail.svelte';
  import PRFacts from '../PRFacts.svelte';
  import Icon from '../Icon.svelte';

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
      <p class="notice danger" role="alert">{error}</p>
    {:else if !review}
      <p class="meta">Loading the review…</p>
    {:else}
      <div class="links">
        <button class="btn btn-sm" onclick={() => app.openPanel({ kind: 'job', id: review.jobId }, 'evidence')}><Icon name="file" size={15} />View evidence</button>
        <a class="btn btn-sm btn-quiet" href={conversationHref(review.source)}>
          Source conversation
        </a>
      </div>
      <ReviewDetail {review} currentHead={job ? workResultKey(job, Object.values(app.data.artifacts)) : undefined} />
      {#if pr}
        <section class="pr-block">
          <h3>Pull request</h3>
          <PRFacts {pr} reviews={[review]} />
        </section>
      {/if}
    {/if}
  </div>
</RightPanel>

<style>
  .pad {
    padding: 14px 18px 24px;
    display: grid;
    gap: 14px;
  }
  .links {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
  h3 {
    font-size: 14px;
    margin-bottom: 8px;
  }
</style>
