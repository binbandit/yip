import { describe, expect, it } from 'vitest';
import { href, parseLocation, parseRoute, routePath, safeNext, withPanel, type Route } from '../../src/lib/router';

describe('router', () => {
  it('parses every screen', () => {
    expect(parseRoute('/')).toEqual({ name: 'overview' });
    expect(parseRoute('/overview')).toEqual({ name: 'overview' });
    expect(parseRoute('/rooms/abc')).toEqual({ name: 'room', roomId: 'abc' });
    expect(parseRoute('/engineers')).toEqual({ name: 'engineers' });
    expect(parseRoute('/engineers/e1')).toEqual({ name: 'engineer', id: 'e1' });
    expect(parseRoute('/projects/p1/')).toEqual({ name: 'project', id: 'p1' });
    expect(parseRoute('/machines')).toEqual({ name: 'machines' });
    expect(parseRoute('/settings')).toEqual({ name: 'settings' });
    expect(parseRoute('/signin')).toEqual({ name: 'signin' });
    expect(parseRoute('/setup')).toEqual({ name: 'setup' });
    expect(parseRoute('/nope/x/y')).toEqual({ name: 'notfound', path: '/nope/x/y' });
  });

  it('round-trips routes through paths', () => {
    const routes: Route[] = [
      { name: 'overview' },
      { name: 'room', roomId: 'a b' },
      { name: 'engineer', id: 'e1' },
      { name: 'project', id: 'p/1' },
      { name: 'machines' },
    ];
    for (const r of routes) expect(parseRoute(routePath(r))).toEqual(r);
  });

  it('keeps the panel, tab and highlighted message in the query', () => {
    const url = href({ name: 'room', roomId: 'r1' }, { panel: { kind: 'job', id: 'j1' }, tab: 'review', msg: 'm1' });
    expect(url).toBe('/rooms/r1?panel=job%3Aj1&tab=review&msg=m1');
    const loc = parseLocation('/rooms/r1', url.split('?')[1]);
    expect(loc.panel).toEqual({ kind: 'job', id: 'j1' });
    expect(loc.tab).toBe('review');
    expect(loc.msg).toBe('m1');
    expect(withPanel(loc, null)).toBe('/rooms/r1');
  });

  it('opens machine details as a panel on Machines, with its section in the tab', () => {
    const url = href({ name: 'machines' }, { panel: { kind: 'machine', id: 'n1' }, tab: 'storage' });
    expect(url).toBe('/machines?panel=machine%3An1&tab=storage');
    const loc = parseLocation('/machines', url.split('?')[1]);
    expect(loc.panel).toEqual({ kind: 'machine', id: 'n1' });
    expect(loc.tab).toBe('storage');
    expect(withPanel(loc, null)).toBe('/machines');
  });

  it('rejects unknown panels', () => {
    expect(parseLocation('/rooms/r1', 'panel=evil:1').panel).toBeNull();
    expect(parseLocation('/rooms/r1', 'panel=job:').panel).toBeNull();
  });

  it('only accepts in-app next targets', () => {
    expect(safeNext('/rooms/r1?panel=job%3Aj')).toBe('/rooms/r1?panel=job%3Aj');
    expect(safeNext('//evil.example')).toBeNull();
    expect(safeNext('https://evil.example')).toBeNull();
    expect(safeNext('/v1/export')).toBeNull();
    expect(safeNext('/signin')).toBeNull();
  });
});
