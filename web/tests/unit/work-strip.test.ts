import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import WorkStrip from '../../src/components/WorkStrip.svelte';
import { app } from '../../src/lib/state/app.svelte';
import { details } from '../../src/lib/state/details.svelte';
import { emptyState, applyBootstrap, applyEvent } from '../../src/lib/state/data';
import { fixture } from './fakehub';
import type { Bootstrap, JobDetail, ReviewState } from '../../src/lib/api/types.gen';

let component: ReturnType<typeof mount> | undefined;
afterEach(async () => { if (component) await unmount(component); vi.restoreAllMocks(); });

it.each(['code', 'document'])('shows current and stale %s review verdicts', (kind) => {
  const d = fixture<JobDetail>('job-code.json');
  app.data = emptyState();
  applyBootstrap(app.data, fixture<Bootstrap>('bootstrap.json'));
  const job = { ...d.job, kind, state: 'review_ready' as const };
  app.data.jobs[job.id] = job;
  const doc = { ...d.artifacts[0], jobId: job.id, kind: 'document', hash: 'content-v2' };
  app.data.artifacts[doc.id] = doc;
  vi.spyOn(details, 'ensureJob').mockImplementation(() => {});
  component = mount(WorkStrip, { target: document.body, props: { roomId: job.source.roomId } });
  const review = d.reviews[0];
  const round = review.rounds.at(-1)!;
  const update = (state: ReviewState, key = kind === 'code' ? job.revision!.head : doc.hash) => {
    app.data.reviews[review.id] = { ...review, state, rounds: [{ ...round, state, target: { ...round.target, head: kind === 'code' ? key : undefined, hash: kind === 'document' ? key : undefined } }] };
    flushSync();
    return document.querySelector('.strip')!.textContent!;
  };
  expect(update('queued')).toContain('review requested');
  expect(document.querySelector('.strip')!.textContent).toContain('In review');
  expect(document.querySelector('.strip')!.textContent).not.toContain('reviewed');
  expect(update('reviewing')).toContain('reviewing');
  expect(update('changes_requested')).toContain('requested changes');
  expect(update('approved')).toContain('approved');
  expect(update('approved', 'older-revision')).toContain('earlier revision');
  expect(document.querySelector('.strip .who')!.textContent).not.toContain('approved');
  applyEvent(app.data, { schemaVersion: 1, eventId: 'done', orgId: job.orgId, actor: { kind: 'system', id: 'hub' }, sequence: app.data.lastSeq + 1, type: 'job.updated', occurredAt: new Date().toISOString(), payload: { ...job, state: 'completed', version: job.version + 1 } });
  flushSync();
  expect(document.querySelector('.strip')).toBeNull();
});
