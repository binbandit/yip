// Human time formatting. Relative times are used for "last confirmed" facts;
// exact times are always available in a title/datetime attribute.

const locale = typeof navigator !== 'undefined' ? navigator.language : 'en';

const clockFmt = new Intl.DateTimeFormat(locale, { hour: 'numeric', minute: '2-digit' });
const dayFmt = new Intl.DateTimeFormat(locale, { weekday: 'long', day: 'numeric', month: 'long' });
const dayYearFmt = new Intl.DateTimeFormat(locale, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
const shortDateFmt = new Intl.DateTimeFormat(locale, { day: 'numeric', month: 'short' });
const fullFmt = new Intl.DateTimeFormat(locale, { dateStyle: 'full', timeStyle: 'short' });

export function toDate(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d;
}

export function clock(iso: string | null | undefined): string {
  const d = toDate(iso);
  return d ? clockFmt.format(d) : '';
}

export function fullTime(iso: string | null | undefined): string {
  const d = toDate(iso);
  return d ? fullFmt.format(d) : '';
}

export function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function dayLabel(iso: string, now = new Date()): string {
  const d = toDate(iso);
  if (!d) return '';
  if (sameDay(d, now)) return 'Today';
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (sameDay(d, y)) return 'Yesterday';
  return d.getFullYear() === now.getFullYear() ? dayFmt.format(d) : dayYearFmt.format(d);
}

export function relative(iso: string | null | undefined, now = Date.now()): string {
  const d = toDate(iso);
  if (!d) return '';
  const s = Math.round((now - d.getTime()) / 1000);
  if (s < 0) {
    const f = -s;
    if (f < 60) return 'in under a minute';
    if (f < 3600) return `in ${Math.round(f / 60)}m`;
    if (f < 86400) return `in ${Math.round(f / 3600)}h`;
    return `in ${Math.round(f / 86400)}d`;
  }
  if (s < 45) return 'just now';
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  if (s < 2 * 86400) return 'yesterday';
  if (s < 7 * 86400) return `${Math.round(s / 86400)}d ago`;
  return shortDateFmt.format(d);
}

/** "at 10:42" today, otherwise "on 23 Sep at 10:42". */
export function atTime(iso: string | null | undefined, now = new Date()): string {
  const d = toDate(iso);
  if (!d) return '';
  return sameDay(d, now) ? `at ${clockFmt.format(d)}` : `on ${shortDateFmt.format(d)} at ${clockFmt.format(d)}`;
}

export function duration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
  const m = Math.floor(s / 60);
  const rs = Math.round(s % 60);
  if (m < 60) return rs ? `${m}m ${rs}s` : `${m}m`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function bytes(n: number): string {
  if (!Number.isFinite(n)) return '';
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

export function shortSha(sha: string | null | undefined): string {
  return sha ? sha.slice(0, 7) : '';
}
