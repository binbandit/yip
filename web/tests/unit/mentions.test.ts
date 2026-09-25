import { describe, expect, it } from 'vitest';
import {
  filterCandidates,
  findMentionQuery,
  insertMention,
  parseSerializedMentions,
  pruneSelected,
  resolveMentions,
  serializeMentions,
  type MentionCandidate,
} from '../../src/lib/util/mentions';

const mira = { kind: 'engineer' as const, id: 'e1', handle: 'mira' };
const oren = { kind: 'engineer' as const, id: 'e2', handle: 'oren' };

describe('mention query detection', () => {
  it('finds the query at the caret', () => {
    expect(findMentionQuery('hey @mi', 7)).toEqual({ start: 4, query: 'mi' });
    expect(findMentionQuery('@', 1)).toEqual({ start: 0, query: '' });
    expect(findMentionQuery('(@or', 4)).toEqual({ start: 1, query: 'or' });
  });

  it('ignores email addresses and code', () => {
    expect(findMentionQuery('me@mira', 7)).toBeNull();
    expect(findMentionQuery('run `@mi', 8)).toBeNull();
    expect(findMentionQuery('```\n@mi', 7)).toBeNull();
  });

  it('inserts the handle and a trailing space', () => {
    expect(insertMention('ask @mi now', 4, 7, 'mira')).toEqual({ text: 'ask @mira now', caret: 10 });
    expect(insertMention('@o', 0, 2, 'oren')).toEqual({ text: '@oren ', caret: 6 });
  });
});

describe('structured mentions', () => {
  it('sends only picked mentions still present in the body, in order', () => {
    expect(resolveMentions('@oren and @mira, please', [mira, oren])).toEqual([
      { kind: 'engineer', id: 'e2' },
      { kind: 'engineer', id: 'e1' },
    ]);
  });

  it('never creates a mention from typed or pasted text', () => {
    expect(resolveMentions('@mira please look', [])).toEqual([]);
    expect(resolveMentions('Pasted: "@oren said hi"', [mira])).toEqual([]);
  });

  it('drops mentions whose text was deleted or only appears inside code', () => {
    expect(resolveMentions('please look', [mira])).toEqual([]);
    expect(resolveMentions('see `@mira`', [mira])).toEqual([]);
    expect(resolveMentions('```\n@mira\n```', [mira])).toEqual([]);
    expect(resolveMentions('@miranda', [mira])).toEqual([]);
  });

  it('deduplicates repeated mentions', () => {
    expect(resolveMentions('@mira @mira', [mira, mira])).toEqual([{ kind: 'engineer', id: 'e1' }]);
    expect(pruneSelected('@mira', [mira, mira, oren])).toEqual([mira]);
  });

  it('round-trips serialized drafts and rejects junk', () => {
    expect(parseSerializedMentions(serializeMentions([mira, oren]))).toEqual([mira, oren]);
    expect(parseSerializedMentions('not json')).toEqual([]);
    expect(parseSerializedMentions('[{"kind":"robot","id":"x","handle":"x"}]')).toEqual([]);
  });

  it('ranks candidates by handle/name prefix, then role', () => {
    const all: MentionCandidate[] = [
      { kind: 'engineer', id: 'e1', handle: 'mira', name: 'Mira', role: 'Platform engineer' },
      { kind: 'engineer', id: 'e2', handle: 'oren', name: 'Oren', role: 'Security engineer' },
      { kind: 'engineer', id: 'e3', handle: 'pip', name: 'Pip', role: 'Reverse engineer', unavailable: 'Archived' },
    ];
    expect(filterCandidates(all, 'o').map((c) => c.handle)).toEqual(['oren']);
    expect(filterCandidates(all, 'security').map((c) => c.handle)).toEqual(['oren']);
    expect(filterCandidates(all, '').length).toBe(3);
  });
});
