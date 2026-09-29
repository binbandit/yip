// A small history-API router. The hub serves index.html for unknown paths,
// so every screen has a real, shareable URL. The right-hand panel (thread or
// detail drawer) lives in the query string so Back closes it and links can
// open a specific job, review, or message.
import { workspaceBase, workspaceLocalPath, workspaceUrl } from './workspace';

export type Route =
  | { name: 'setup' }
  | { name: 'signin' }
  | { name: 'home' }
  | { name: 'start' }
  | { name: 'room'; roomId: string }
  | { name: 'engineers' }
  | { name: 'engineer'; id: string }
  | { name: 'projects' }
  | { name: 'project'; id: string }
  | { name: 'machines' }
  | { name: 'connections'; provider?: string }
  | { name: 'settings' }
  | { name: 'notfound'; path: string };

const PANEL_KINDS = ['thread', 'job', 'review', 'pr', 'engineer', 'decision', 'room', 'machine'] as const;

export type PanelKind = (typeof PANEL_KINDS)[number];

export interface Panel {
  kind: PanelKind;
  id: string;
}

export interface Location {
  route: Route;
  panel: Panel | null;
  /** Tab inside the panel (e.g. job drawer "evidence"). */
  tab: string | null;
  /** Message to scroll to and highlight. */
  msg: string | null;
  /** Where to return after sign-in. */
  next: string | null;
}

function seg(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

export function parseRoute(pathname: string): Route {
  const parts = workspaceLocalPath(pathname).replace(/\/+$/, '').split('/').filter(Boolean).map(seg);
  const [a, b, ...rest] = parts;
  if (rest.length) return { name: 'notfound', path: pathname };
  switch (a) {
    case undefined:
      return { name: 'home' };
    case 'start':
      return b ? { name: 'notfound', path: pathname } : { name: 'start' };
    case 'setup':
      return { name: 'setup' };
    case 'signin':
      return { name: 'signin' };
    case 'rooms':
      return b ? { name: 'room', roomId: b } : { name: 'home' };
    case 'engineers':
      return b ? { name: 'engineer', id: b } : { name: 'engineers' };
    case 'projects':
      return b ? { name: 'project', id: b } : { name: 'projects' };
    case 'machines':
      return b ? { name: 'notfound', path: pathname } : { name: 'machines' };
    case 'connections':
      return b ? { name: 'connections', provider: b } : { name: 'connections' };
    case 'settings':
      return b ? { name: 'notfound', path: pathname } : { name: 'settings' };
    default:
      return { name: 'notfound', path: pathname };
  }
}

export function parsePanel(value: string | null): Panel | null {
  if (!value) return null;
  const i = value.indexOf(':');
  if (i <= 0) return null;
  const kind = value.slice(0, i) as PanelKind;
  const id = value.slice(i + 1);
  if (!PANEL_KINDS.includes(kind) || !id) return null;
  return { kind, id };
}

export function parseLocation(pathname: string, search: string): Location {
  const q = new URLSearchParams(search);
  return {
    route: parseRoute(pathname),
    panel: parsePanel(q.get('panel')),
    tab: q.get('tab'),
    msg: q.get('msg'),
    next: q.get('next'),
  };
}

export function routePath(r: Route): string {
  return workspaceUrl(localRoutePath(r));
}

function localRoutePath(r: Route): string {
  const e = encodeURIComponent;
  switch (r.name) {
    case 'setup':
      return '/setup';
    case 'signin':
      return '/signin';
    case 'home':
      return '/';
    case 'start':
      return '/start';
    case 'room':
      return `/rooms/${e(r.roomId)}`;
    case 'engineers':
      return '/engineers';
    case 'engineer':
      return `/engineers/${e(r.id)}`;
    case 'projects':
      return '/projects';
    case 'project':
      return `/projects/${e(r.id)}`;
    case 'machines':
      return '/machines';
    case 'connections':
      return r.provider ? `/connections/${e(r.provider)}` : '/connections';
    case 'settings':
      return '/settings';
    case 'notfound':
      return r.path;
  }
}

export interface HrefExtras {
  panel?: Panel | null;
  tab?: string | null;
  msg?: string | null;
  next?: string | null;
}

export function href(r: Route, extras: HrefExtras = {}): string {
  const q = new URLSearchParams();
  if (extras.panel) q.set('panel', `${extras.panel.kind}:${extras.panel.id}`);
  if (extras.tab) q.set('tab', extras.tab);
  if (extras.msg) q.set('msg', extras.msg);
  if (extras.next) q.set('next', extras.next);
  const qs = q.toString();
  return routePath(r) + (qs ? `?${qs}` : '');
}

/** The same location with a different panel (keeps the route). */
export function withPanel(loc: Location, panel: Panel | null, tab: string | null = null): string {
  return href(loc.route, { panel, tab, msg: panel ? loc.msg : null });
}

/** Only same-origin, in-app paths are acceptable "next" targets after sign-in. */
export function safeNext(next: string | null): string | null {
  if (!next || !next.startsWith('/') || next.startsWith('//') || /[\\\u0000-\u0020]/.test(next)) return null;
  const base = workspaceBase(next);
  if (base && base !== workspaceBase()) return null;
  const origin = typeof window === 'undefined' ? 'http://yip.invalid' : window.location.origin;
  const url = new URL(workspaceUrl(next), origin);
  if (url.origin !== origin || workspaceBase(url.pathname) !== workspaceBase()) return null;
  const r = parseRoute(url.pathname);
  if (r.name === 'signin' || r.name === 'setup' || r.name === 'notfound') return null;
  return url.pathname + url.search + url.hash;
}
