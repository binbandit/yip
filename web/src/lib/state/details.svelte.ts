// On-demand detail fetches (job detail, reviews, approvals, pull requests),
// shared across result cards and drawers. Job detail refetches (debounced)
// whenever an event touches that job, so open views stay live.
import { api } from '../api/endpoints';
import { ApiError, errorMessage } from '../api/client';
import type { JobDetail } from '../api/types.gen';
import { app } from './app.svelte';

export interface Entry<T> {
  data?: T;
  error?: string;
  missing?: boolean;
  loading: boolean;
  touch: number;
}

class Details {
  jobs = $state<Record<string, Entry<JobDetail>>>({});
  private timers = new Map<string, ReturnType<typeof setTimeout>>();
  private pendingFetch = new Set<string>();

  /** Ensure a job's detail is loaded and current with the given touch counter. */
  ensureJob(id: string, touch: number): void {
    const e = this.jobs[id];
    if (e && (e.touch === touch || e.missing)) return;
    if (!e) {
      this.jobs[id] = { loading: true, touch };
      void this.fetchJob(id, touch);
      return;
    }
    e.touch = touch;
    const prev = this.timers.get(id);
    if (prev) clearTimeout(prev);
    this.timers.set(
      id,
      setTimeout(() => {
        this.timers.delete(id);
        void this.fetchJob(id, touch);
      }, 350),
    );
  }

  async refreshJob(id: string): Promise<void> {
    await this.fetchJob(id, this.jobs[id]?.touch ?? 0);
  }

  private async fetchJob(id: string, touch: number): Promise<void> {
    if (this.pendingFetch.has(id)) return;
    this.pendingFetch.add(id);
    try {
      const d = await api.job(id);
      normalizeDetail(d);
      this.jobs[id] = { data: d, loading: false, touch };
      // Keep the shared stores current with what the detail tells us.
      const cur = app.data.jobs[d.job.id];
      if (!cur || d.job.version >= cur.version) app.data.jobs[d.job.id] = d.job;
      for (const r of d.runs) app.data.runs[r.id] = r;
      for (const r of d.reviews) {
        const c = app.data.reviews[r.id];
        if (!c || r.updatedAt >= c.updatedAt) app.data.reviews[r.id] = r;
      }
      for (const a of d.approvals) {
        const c = app.data.approvals[a.id];
        if (!c || a.version >= c.version) app.data.approvals[a.id] = a;
      }
      for (const q of d.questions) app.data.questions[q.id] = q;
      for (const p of d.pullRequests) app.data.prs[p.id] = p;
      for (const i of d.inputs) {
        const c = app.data.inputs[i.id];
        if (!(c && c.delivery !== 'pending' && i.delivery === 'pending')) app.data.inputs[i.id] = i;
      }
    } catch (err) {
      const missing = err instanceof ApiError && (err.status === 404 || err.status === 403);
      const prev = this.jobs[id];
      this.jobs[id] = { data: prev?.data, error: errorMessage(err), missing, loading: false, touch };
    } finally {
      this.pendingFetch.delete(id);
    }
  }

  private loadingOther = new Set<string>();

  async ensureReview(id: string, force = false): Promise<void> {
    if ((!force && app.data.reviews[id]) || this.loadingOther.has('r:' + id)) return;
    this.loadingOther.add('r:' + id);
    try {
      const r = await api.review(id);
      r.rounds = r.rounds ?? [];
      app.data.reviews[r.id] = r;
    } finally {
      this.loadingOther.delete('r:' + id);
    }
  }

  async ensureApproval(id: string): Promise<void> {
    if (app.data.approvals[id] || this.loadingOther.has('a:' + id)) return;
    this.loadingOther.add('a:' + id);
    try {
      const a = await api.approval(id);
      app.data.approvals[a.id] = a;
    } catch {
      /* rendered as unavailable */
    } finally {
      this.loadingOther.delete('a:' + id);
    }
  }

  async ensurePR(id: string, refresh = false): Promise<void> {
    if ((!refresh && app.data.prs[id]) || this.loadingOther.has('p:' + id)) return;
    this.loadingOther.add('p:' + id);
    try {
      const p = await api.pullRequest(id, refresh);
      app.data.prs[p.id] = p;
    } finally {
      this.loadingOther.delete('p:' + id);
    }
  }
}

function normalizeDetail(d: JobDetail): void {
  d.runs = d.runs ?? [];
  d.checks = d.checks ?? [];
  d.artifacts = d.artifacts ?? [];
  d.reviews = (d.reviews ?? []).map((r) => ({ ...r, rounds: r.rounds ?? [] }));
  d.questions = d.questions ?? [];
  d.approvals = d.approvals ?? [];
  d.pullRequests = d.pullRequests ?? [];
  d.children = d.children ?? [];
  d.decisions = d.decisions ?? [];
  d.activity = d.activity ?? [];
  d.inputs = d.inputs ?? [];
  d.missing = d.missing ?? [];
}

export const details = new Details();
