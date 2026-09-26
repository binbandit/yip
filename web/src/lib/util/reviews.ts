import type { Artifact, Job, Review } from '../api/types.gen';

export function workResultKey(job: Job, artifacts: Artifact[]) {
  if (job.kind === 'code') return job.revision?.head;
  return artifacts
    .filter((a) => a.jobId === job.id && (a.kind === 'document' || a.kind === 'file'))
    .sort((a, b) => a.createdAt.localeCompare(b.createdAt)).at(-1)?.hash;
}

/** A verdict is applicable only to the current immutable work. */
export function currentReviewRound(job: Job, review: Review, artifacts: Artifact[]) {
  const round = review.rounds.find((r) => r.number === review.currentRound);
  if (!round || round.supersededBy) return undefined;
  const key = workResultKey(job, artifacts);
  return key && (round.target.head || round.target.hash) === key ? round : undefined;
}
