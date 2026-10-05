import { afterEach, expect, it, vi } from 'vitest';
import { EventStream } from '../../src/lib/state/events';
import { FakeEventSource } from './fakehub';

afterEach(() => vi.unstubAllGlobals());

it('ignores activity and state callbacks from retired connections', () => {
  vi.stubGlobal('EventSource', FakeEventSource);
  const handlers = { cursor: () => 42, onEvent: vi.fn(), onTransient: vi.fn(), onReset: vi.fn(), onState: vi.fn(), onFatal: vi.fn() };
  const stream = new EventStream(handlers);
  stream.start();
  const old = FakeEventSource.latest();
  old.emit('ready', {});
  stream.reconnect();
  const active = FakeEventSource.latest();
  expect(active.url).toContain('cursor=42');
  handlers.onState.mockClear();
  old.emit('ready', {});
  old.onopen?.(new Event('open'));
  old.emit('transient', { type: 'run.stream', runId: 'old' });
  old.emit('run.updated', { type: 'run.updated', sequence: 43 });
  old.emit('reset', { cursor: 44 });
  expect(handlers.onState).not.toHaveBeenCalled();
  expect(handlers.onTransient).not.toHaveBeenCalled();
  expect(handlers.onEvent).not.toHaveBeenCalled();
  expect(handlers.onReset).not.toHaveBeenCalled();
  active.emit('ready', {});
  active.emit('transient', { type: 'run.stream', runId: 'new' });
  expect(handlers.onState).toHaveBeenCalledWith('live');
  expect(handlers.onTransient).toHaveBeenCalledOnce();
  stream.stop();
  active.emit('transient', { type: 'run.stream', runId: 'late' });
  active.emit('ready', {});
  expect(handlers.onTransient).toHaveBeenCalledOnce();
  expect(handlers.onState).toHaveBeenCalledTimes(1);
});
