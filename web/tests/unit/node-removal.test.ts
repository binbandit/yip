import { describe, expect, it } from 'vitest';
import { applyBootstrap, applyEvent, emptyState, mergeNode, removeNode, replaceNodes } from '../../src/lib/state/data';
import type { Bootstrap, Event, Node } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';

const node = fixture<Node[]>('nodes.json')[0];

describe('removed machine state', () => {
  it('removes the row on an event from another tab and rejects delayed list/action responses', () => {
    const s = emptyState();
    mergeNode(s, node);
    s.nodeReportSeq[node.id] = 1;
    applyEvent(s, { type: 'node.updated', sequence: 2, payload: { ...node, removedAt: '2026-10-05T00:00:00Z' } } as Event);
    expect(s.nodes[node.id]).toBeUndefined();
    expect(s.nodeReportSeq[node.id]).toBeUndefined();
    replaceNodes(s, [node]);
    mergeNode(s, node);
    expect(s.nodes[node.id]).toBeUndefined();
    mergeNode(s, { ...node, id: 'paired-again' });
    expect(s.nodes['paired-again']).toBeDefined();
  });

  it('drops stale list entries when refreshing and keeps revocation distinct from removal', () => {
    const s = emptyState();
    const revoked = { ...node, status: 'revoked', revokedAt: '2026-10-05T00:00:00Z' };
    replaceNodes(s, [revoked]);
    expect(s.nodes[node.id]).toEqual(revoked);
    replaceNodes(s, []);
    expect(s.nodes[node.id]).toBeUndefined();
    mergeNode(s, revoked);
    removeNode(s, node.id);
    removeNode(s, node.id);
    mergeNode(s, revoked);
    expect(s.nodes[node.id]).toBeUndefined();
  });

  it('preserves pairing and updates received while a list request was in flight', () => {
    const s = emptyState();
    replaceNodes(s, [node, { ...node, id: 'removed-while-away' }]);
    const requestedAt = s.lastSeq;
    applyEvent(s, { type: 'node.updated', sequence: 1, payload: { ...node, id: 'paired-during-request' } } as Event);
    applyEvent(s, { type: 'node.updated', sequence: 2, payload: { ...node, status: 'revoked', revokedAt: '2026-10-05T00:00:00Z' } } as Event);
    replaceNodes(s, [node], requestedAt);
    expect(s.nodes['paired-during-request']).toBeDefined();
    expect(s.nodes[node.id].status).toBe('revoked');
    expect(s.nodes['removed-while-away']).toBeUndefined();
    expect(s.lastSeq).toBe(2);
  });

  it('preserves historical machine names after removal and fresh bootstrap', () => {
    const s = emptyState();
    mergeNode(s, node);
    removeNode(s, node.id);
    expect(s.nodeNames[node.id]).toBe(node.name);
    const fresh = emptyState();
    applyBootstrap(fresh, { ...fixture<Bootstrap>('bootstrap.json'), nodes: [], nodeNames: { [node.id]: node.name } });
    expect(fresh.nodes[node.id]).toBeUndefined();
    expect(fresh.nodeNames[node.id]).toBe(node.name);
  });
});
