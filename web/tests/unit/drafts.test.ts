import { beforeEach, describe, expect, it } from 'vitest';
import {
  clearDraft,
  clearUnsent,
  draftKey,
  loadDraft,
  loadUnsent,
  roomsWithDrafts,
  saveDraft,
  saveUnsent,
  type KeyValueStorage,
} from '../../src/lib/state/drafts';

class MemoryStorage implements KeyValueStorage {
  map = new Map<string, string>();
  get length() {
    return this.map.size;
  }
  key(i: number) {
    return [...this.map.keys()][i] ?? null;
  }
  getItem(k: string) {
    return this.map.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.map.set(k, v);
  }
  removeItem(k: string) {
    this.map.delete(k);
  }
}

describe('drafts', () => {
  let st: MemoryStorage;
  beforeEach(() => {
    st = new MemoryStorage();
  });

  it('keys drafts per room and per thread', () => {
    expect(draftKey('r1')).toBe('yip.draft.r1');
    expect(draftKey('r1', 't1')).toBe('yip.draft.r1.t1');
    expect(draftKey('r1', null)).toBe('yip.draft.r1');
  });

  it('saves and restores body, mentions, projects and steering scope', () => {
    saveDraft(draftKey('r1'), {
      body: '@mira keep the API shape',
      mentions: [{ kind: 'engineer', id: 'e1', handle: 'mira' }],
      projectIds: ['p1'],
      jobId: 'j1',
    }, st);
    const d = loadDraft(draftKey('r1'), st);
    expect(d?.body).toBe('@mira keep the API shape');
    expect(d?.mentions).toEqual([{ kind: 'engineer', id: 'e1', handle: 'mira' }]);
    expect(d?.projectIds).toEqual(['p1']);
    expect(d?.jobId).toBe('j1');
  });

  it('removes empty drafts instead of storing them', () => {
    saveDraft(draftKey('r1'), { body: 'x', mentions: [], projectIds: [] }, st);
    saveDraft(draftKey('r1'), { body: '   ', mentions: [], projectIds: [] }, st);
    expect(loadDraft(draftKey('r1'), st)).toBeNull();
    expect(st.length).toBe(0);
  });

  it('lists rooms with top-level drafts only', () => {
    saveDraft(draftKey('r1'), { body: 'a', mentions: [], projectIds: [] }, st);
    saveDraft(draftKey('r2', 't'), { body: 'b', mentions: [], projectIds: [] }, st);
    expect([...roomsWithDrafts(st)]).toEqual(['r1']);
    clearDraft(draftKey('r1'), st);
    expect([...roomsWithDrafts(st)]).toEqual([]);
  });

  it('ignores corrupted entries', () => {
    st.setItem(draftKey('r1'), '{nope');
    expect(loadDraft(draftKey('r1'), st)).toBeNull();
  });

  it('keeps unsent messages with their clientKey until cleared', () => {
    saveUnsent({ clientKey: 'ck1', roomId: 'r1', body: 'hi', mentions: [], projectIds: [], createdAt: '2026-01-01T00:00:00Z' }, st);
    expect(loadUnsent(st).map((u) => u.clientKey)).toEqual(['ck1']);
    clearUnsent('ck1', st);
    expect(loadUnsent(st)).toEqual([]);
  });

  it('works against the real localStorage in the browser environment', () => {
    saveDraft(draftKey('real'), { body: 'kept', mentions: [], projectIds: [] });
    expect(loadDraft(draftKey('real'))?.body).toBe('kept');
    clearDraft(draftKey('real'));
    expect(loadDraft(draftKey('real'))).toBeNull();
  });
});
