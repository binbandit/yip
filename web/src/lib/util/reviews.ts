import type { Artifact, Job, Review } from '../api/types.gen';

/** A verdict is applicable only to the current immutable work. */
export function currentReviewRound(job: Job, review: Review, artifacts: Artifact[]) {
  const round = review.rounds.find((r) => r.number === review.currentRound);
  if (!round || round.supersededBy) return undefined;
  if (job.kind === 'code') return round.target.head === job.revision?.head ? round : undefined;
  const document = artifacts
    .filter((a) => a.jobId === job.id && (a.kind === 'document' || a.kind === 'file'))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
  return document && round.target.hash === document.hash ? round : undefined;
}
