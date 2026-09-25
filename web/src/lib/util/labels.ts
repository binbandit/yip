// The plain words the interface uses for states. State is always shown as a
// word plus a shape, never colour alone.
import type { Job, JobState, ReviewState, RunState } from '../api/types.gen';

export type Shape = 'circle' | 'bar' | 'pause' | 'check' | 'check-filled' | 'triangle' | 'slash' | 'question';

export function jobStateLabel(j: Pick<Job, 'state' | 'requiresHumanReview'>): string {
  switch (j.state) {
    case 'queued':
      return 'Queued';
    case 'running':
      return 'Running';
    case 'waiting':
      return 'Waiting';
    case 'review_ready':
      return j.requiresHumanReview ? 'Ready for your review' : 'Ready';
    case 'completed':
      return 'Completed';
    case 'failed':
      return 'Failed';
    case 'cancelled':
      return 'Stopped';
  }
  return j.state;
}

export function jobShape(state: JobState | string): Shape {
  switch (state) {
    case 'queued':
      return 'circle';
    case 'running':
      return 'bar';
    case 'waiting':
      return 'pause';
    case 'review_ready':
      return 'check';
    case 'completed':
      return 'check-filled';
    case 'failed':
      return 'triangle';
    case 'cancelled':
      return 'slash';
  }
  return 'question';
}

export type Tone = 'neutral' | 'accent' | 'attention' | 'success' | 'danger';

export function jobTone(state: JobState | string): Tone {
  switch (state) {
    case 'running':
      return 'accent';
    case 'waiting':
      return 'attention';
    case 'review_ready':
    case 'completed':
      return 'success';
    case 'failed':
      return 'danger';
  }
  return 'neutral';
}

export function waitingReasonLabel(reason: string | undefined): string {
  switch (reason) {
    case 'missing_information':
      return 'Waiting for an answer';
    case 'approval':
      return 'Waiting for permission';
    case 'dependency':
      return 'Waiting on a colleague';
    case 'provider_allowance':
      return 'Allowance reached';
    case 'machine_availability':
      return 'Waiting for a machine';
    case 'recovery':
      return 'Recovering';
    case 'provider_sign_in':
      return 'Provider needs sign-in';
    case 'engineer_capacity':
      return 'Engineer busy';
    case 'stalled':
      return 'Stalled';
    default:
      return 'Waiting';
  }
}

export function runStateLabel(state: RunState | string): string {
  switch (state) {
    case 'created':
      return 'Queued';
    case 'offered':
      return 'Offered to a machine';
    case 'preparing':
      return 'Preparing';
    case 'running':
      return 'Running';
    case 'awaiting_input':
      return 'Waiting for input';
    case 'stopping':
      return 'Stopping';
    case 'succeeded':
      return 'Finished';
    case 'failed':
      return 'Failed';
    case 'cancelled':
      return 'Stopped';
    case 'unknown':
      return 'Outcome not confirmed';
  }
  return state;
}

export function runShape(state: RunState | string): Shape {
  switch (state) {
    case 'created':
    case 'offered':
      return 'circle';
    case 'preparing':
    case 'running':
    case 'stopping':
      return 'bar';
    case 'awaiting_input':
      return 'pause';
    case 'succeeded':
      return 'check-filled';
    case 'failed':
      return 'triangle';
    case 'cancelled':
      return 'slash';
  }
  return 'question';
}

export function runTone(state: RunState | string): Tone {
  switch (state) {
    case 'running':
    case 'preparing':
      return 'accent';
    case 'awaiting_input':
    case 'unknown':
      return 'attention';
    case 'succeeded':
      return 'success';
    case 'failed':
      return 'danger';
  }
  return 'neutral';
}

export function reviewStateLabel(state: ReviewState | string): string {
  switch (state) {
    case 'requested':
      return 'Review requested';
    case 'queued':
      return 'Review queued';
    case 'reviewing':
      return 'Reviewing';
    case 'approved':
      return 'Approved';
    case 'changes_requested':
      return 'Changes requested';
    case 'comments_only':
      return 'Comments only';
    case 'unable_to_review':
      return 'Unable to review';
    case 'cancelled':
      return 'Review cancelled';
  }
  return state;
}

export function reviewShape(state: ReviewState | string): Shape {
  switch (state) {
    case 'approved':
      return 'check-filled';
    case 'changes_requested':
      return 'triangle';
    case 'reviewing':
      return 'bar';
    case 'requested':
    case 'queued':
      return 'circle';
    case 'unable_to_review':
      return 'question';
    case 'cancelled':
      return 'slash';
  }
  return 'pause';
}

export function reviewTone(state: ReviewState | string): Tone {
  switch (state) {
    case 'approved':
      return 'success';
    case 'changes_requested':
      return 'danger';
    case 'reviewing':
      return 'accent';
    case 'unable_to_review':
      return 'attention';
  }
  return 'neutral';
}

/** Past-tense verdict for a round: "requested changes on", "approved". */
export function verdictPhrase(state: ReviewState | string): string {
  switch (state) {
    case 'approved':
      return 'approved';
    case 'changes_requested':
      return 'requested changes on';
    case 'comments_only':
      return 'commented on';
    case 'unable_to_review':
      return 'could not review';
    case 'reviewing':
      return 'is reviewing';
    case 'requested':
    case 'queued':
      return 'was asked to review';
    case 'cancelled':
      return 'stopped reviewing';
  }
  return state;
}

export function severityLabel(s: string): string {
  switch (s) {
    case 'blocking':
      return 'Blocking';
    case 'suggestion':
      return 'Suggestion';
    case 'note':
      return 'Note';
  }
  return s;
}

export function findingStatusLabel(s: string): string {
  switch (s) {
    case 'open':
      return 'Open';
    case 'addressed':
      return 'Addressed';
    case 'resolved':
      return 'Resolved';
    case 'disputed':
      return 'Disputed';
    case 'withdrawn':
      return 'Withdrawn';
  }
  return s;
}

export function approvalStatusLabel(s: string): string {
  switch (s) {
    case 'pending':
      return 'Waiting for your decision';
    case 'approved':
      return 'Allowed';
    case 'rejected':
      return 'Rejected';
    case 'expired':
      return 'Expired — it can no longer run';
    case 'consumed':
      return 'Allowed and used';
    case 'cancelled':
      return 'Withdrawn';
  }
  return s;
}

export function approvalVerb(kind: string): string {
  switch (kind) {
    case 'push':
      return 'Allow this push';
    case 'merge':
      return 'Allow this merge';
    case 'publish':
      return 'Allow publishing';
    case 'network':
      return 'Allow this network access';
    case 'exec':
      return 'Allow this command';
    case 'edit':
      return 'Allow this edit';
    default:
      return 'Allow this action';
  }
}

export function nodeStatusLabel(status: string, draining = false): string {
  const base = (() => {
    switch (status) {
      case 'online':
        return 'Connected';
      case 'suspect':
        return 'Not responding';
      case 'offline':
        return 'Offline';
      case 'revoked':
        return 'Revoked';
    }
    return status;
  })();
  return draining && status !== 'revoked' ? `${base} · draining` : base;
}

export function nodeShape(status: string): Shape {
  switch (status) {
    case 'online':
      return 'check-filled';
    case 'suspect':
      return 'pause';
    case 'offline':
      return 'circle';
    case 'revoked':
      return 'slash';
  }
  return 'question';
}

export function nodeTone(status: string): Tone {
  switch (status) {
    case 'online':
      return 'success';
    case 'suspect':
      return 'attention';
  }
  return 'neutral';
}

export function authStateLabel(s: string, detail?: string): string {
  switch (s) {
    case 'ready':
      return 'Signed in';
    case 'needs_signin':
      return 'Needs sign-in';
    case 'not_installed':
      return 'Not installed';
    case 'error':
      return detail ? `Error: ${detail}` : 'Error';
    case 'unknown':
      return 'Sign-in state unknown';
  }
  return s;
}

export function billingLabel(b: string | undefined): string {
  switch (b) {
    case 'subscription':
      return 'subscription';
    case 'api':
      return 'API billed';
    default:
      return 'billing unknown';
  }
}

const PROVIDER_LABELS: Record<string, string> = {
  codex: 'Codex',
  claude: 'Claude Code',
  cursor: 'Cursor',
  fake: 'Demo provider (fake)',
};

export function providerLabel(p: string | undefined): string {
  if (!p) return 'No preference';
  return PROVIDER_LABELS[p] ?? p;
}

export function deliveryReceipt(delivery: string, name: string): string {
  switch (delivery) {
    case 'immediate':
      return `${name} received your update`;
    case 'queued':
      return `Queued for ${name}'s next step`;
    case 'pending':
      return `Delivering to ${name}…`;
  }
  return `Sent to ${name}`;
}

export function accessLabel(a: string): string {
  switch (a) {
    case 'write':
      return 'Can change';
    case 'read':
      return 'Can read';
    default:
      return 'No access';
  }
}

export const GRANT_ACTIONS: { id: string; label: string }[] = [
  { id: 'push', label: 'Push branches' },
  { id: 'open_pr', label: 'Open pull requests' },
  { id: 'publish_review', label: 'Publish reviews' },
  { id: 'merge', label: 'Merge' },
];

export function catchupKindLabel(kind: string): string {
  switch (kind) {
    case 'completed':
      return 'Completed';
    case 'decision':
      return 'Decision';
    case 'blocker':
      return 'Blocked';
    case 'question':
      return 'Question';
    case 'started':
      return 'Started';
    case 'failed':
      return 'Failed';
    case 'unknown':
      return 'Not confirmed';
  }
  return kind;
}

export function catchupShape(kind: string): Shape {
  switch (kind) {
    case 'completed':
      return 'check-filled';
    case 'blocker':
      return 'pause';
    case 'failed':
      return 'triangle';
    case 'started':
      return 'bar';
    case 'unknown':
      return 'question';
    case 'question':
      return 'question';
  }
  return 'circle';
}

export function catchupTone(kind: string): Tone {
  switch (kind) {
    case 'completed':
      return 'success';
    case 'blocker':
    case 'question':
    case 'unknown':
      return 'attention';
    case 'failed':
      return 'danger';
    case 'started':
      return 'accent';
  }
  return 'neutral';
}

/**
 * A short note about the latest attempt when it adds something the job state
 * doesn't say ("unknown" means the outcome is not confirmed).
 */
export function runStateNote(runState: string | undefined): string | undefined {
  switch (runState) {
    case 'unknown':
      return 'Outcome not confirmed';
    case 'offered':
      return 'Handing to a machine';
    case 'preparing':
      return 'Preparing the workspace';
    case 'awaiting_input':
      return 'Waiting for input';
    case 'stopping':
      return 'Stopping';
  }
  return undefined;
}
