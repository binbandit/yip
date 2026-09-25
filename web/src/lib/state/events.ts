// One EventSource for the whole app. Committed events carry an id, so the
// browser resends Last-Event-ID when it reconnects by itself; after we close
// the connection deliberately (a "slow" notice, a network change) we reconnect
// with ?cursor= from the last applied sequence.
import type { Event } from '../api/types.gen';
import type { TransientStream } from './data';

export const COMMITTED_TYPES = [
  'message.created',
  'message.updated',
  'room.created',
  'room.updated',
  'room.member_added',
  'room.member_removed',
  'read.updated',
  'engineer.created',
  'engineer.updated',
  'project.created',
  'project.updated',
  'job.created',
  'job.updated',
  'run.created',
  'run.updated',
  'input.updated',
  'check.recorded',
  'revision.published',
  'artifact.published',
  'review.updated',
  'question.created',
  'question.updated',
  'approval.created',
  'approval.updated',
  'permission.auto',
  'decision.created',
  'decision.updated',
  'node.updated',
  'pr.updated',
  'run.stale_report',
  'org.created',
];

export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'offline';

export interface StreamHandlers {
  cursor(): number;
  onEvent(ev: Event): void;
  onTransient(t: TransientStream): void;
  onReset(cursor: number, reason: string): void;
  onState(state: ConnectionState): void;
  /** The stream closed for good (e.g. 401); verify the session. */
  onFatal(): void;
}

export class EventStream {
  private es: EventSource | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private attempts = 0;
  private stopped = true;

  constructor(private readonly h: StreamHandlers) {}

  start(): void {
    this.stopped = false;
    this.open();
  }

  stop(): void {
    this.stopped = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.es?.close();
    this.es = null;
  }

  /** Close and reopen from our own cursor (used after "slow" or coming back online). */
  reconnect(): void {
    if (this.stopped) return;
    this.es?.close();
    this.es = null;
    this.open();
  }

  private open(): void {
    if (typeof EventSource === 'undefined') return;
    this.h.onState(this.attempts ? 'reconnecting' : 'connecting');
    const es = new EventSource(`/v1/events?cursor=${this.h.cursor()}`, { withCredentials: true });
    this.es = es;
    const parse = (e: MessageEvent) => {
      try {
        return JSON.parse(e.data);
      } catch {
        return null;
      }
    };
    es.addEventListener('ready', () => {
      this.attempts = 0;
      this.h.onState('live');
    });
    es.addEventListener('reset', (e) => {
      const d = parse(e as MessageEvent) ?? {};
      this.h.onReset(Number(d.cursor) || 0, String(d.reason ?? ''));
    });
    es.addEventListener('slow', () => {
      this.h.onState('reconnecting');
      setTimeout(() => this.reconnect(), 250);
    });
    es.addEventListener('transient', (e) => {
      const d = parse(e as MessageEvent);
      if (d) this.h.onTransient(d as TransientStream);
    });
    for (const type of COMMITTED_TYPES) {
      es.addEventListener(type, (e) => {
        const d = parse(e as MessageEvent);
        if (d) this.h.onEvent(d as Event);
      });
    }
    es.onopen = () => {
      this.h.onState('live');
    };
    es.onerror = () => {
      if (this.stopped || this.es !== es) return;
      if (es.readyState === EventSource.CLOSED) {
        // The browser gave up (HTTP error such as 401, or a proxy failure).
        this.es = null;
        this.attempts++;
        this.h.onState(navigator.onLine === false ? 'offline' : 'reconnecting');
        const delay = Math.min(30_000, 1000 * 2 ** Math.min(this.attempts, 5));
        this.retryTimer = setTimeout(() => {
          this.retryTimer = null;
          this.h.onFatal();
        }, delay);
      } else {
        this.h.onState(navigator.onLine === false ? 'offline' : 'reconnecting');
      }
    };
  }
}
