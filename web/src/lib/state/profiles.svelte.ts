// Provider accounts (profiles) and their concurrency and allowance pauses,
// shared by the Machines list and a machine's details. Refreshed when a
// machine reports a change (a sign-in, an allowance pause).
import { api } from '../api/endpoints';
import type { ProviderProfile } from '../api/types.gen';

class ProviderProfiles {
  list = $state<ProviderProfile[]>([]);
  private inflight: Promise<void> | null = null;
  private lastKey = '';

  /** Re-reads the profiles when `key` (a digest of the machines' reports) changes. */
  refresh(key: string): Promise<void> {
    if (key === this.lastKey && this.inflight) return this.inflight;
    this.lastKey = key;
    this.inflight = api.providerProfiles().then(
      (p) => {
        this.list = p ?? [];
      },
      () => {},
    );
    return this.inflight;
  }

  get(id: string | undefined): ProviderProfile | undefined {
    return id ? this.list.find((p) => p.id === id) : undefined;
  }

  replace(p: ProviderProfile): void {
    this.list = this.list.map((x) => (x.id === p.id ? { ...x, ...p } : x));
  }
}

export const providerProfiles = new ProviderProfiles();

/** A digest of what machines report that can change a profile's state. */
export function nodesDigest(nodes: { id: string; status: string; providers: { updatedAt: string; authState: string }[]; lastActivity?: string }[]): string {
  return nodes.map((n) => `${n.id}:${n.status}:${n.providers.map((p) => p.updatedAt + p.authState).join(',')}:${n.lastActivity ?? ''}`).join('|');
}
