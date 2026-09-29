// A workspace lives in its URL, not in a shared "active workspace" cookie.
// Two tabs can therefore stay in different workspaces without affecting one another.
export type { Workspace as WorkspaceSummary } from './api/types.gen';

export function workspaceBase(pathname = typeof window === 'undefined' ? '/' : window.location.pathname): string {
  return pathname.match(/^\/w\/[a-zA-Z0-9_-]+(?=\/|$)/)?.[0] ?? '';
}

/** Prefix application/API links; leave already scoped and external links alone. */
export function workspaceUrl(path: string, base = workspaceBase()): string {
  if (!path.startsWith('/') || path.startsWith('//') || workspaceBase(path)) return path;
  return base + path;
}

export function workspaceLocalPath(path: string): string {
  const base = workspaceBase(path);
  return path.slice(base.length) || '/';
}

/** Root keys stay unchanged so existing drafts survive the upgrade. */
export function workspaceStoragePrefix(kind: string): string {
  const base = workspaceBase();
  return base ? `yip.workspace.${base.slice(3)}.${kind}.` : `yip.${kind}.`;
}

export function rememberWorkspaceLocation(base: string, location: string): void {
  try {
    localStorage.setItem(`yip.workspace-location.${base || 'root'}`, location);
  } catch {
    // Storage is optional; switching still works in private browsing.
  }
}

export function recalledWorkspaceLocation(base: string): string | null {
  try {
    return localStorage.getItem(`yip.workspace-location.${base || 'root'}`);
  } catch {
    return null;
  }
}
