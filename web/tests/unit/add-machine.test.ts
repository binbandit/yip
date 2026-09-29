import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import AddMachineDialog from '../../src/components/AddMachineDialog.svelte';
import { api } from '../../src/lib/api/endpoints';
import type { Enrollment } from '../../src/lib/api/types.gen';

vi.mock('../../src/lib/api/endpoints', () => ({
  api: { createEnrollment: vi.fn() },
}));

let component: ReturnType<typeof mount>;
async function settle() {
  flushSync();
  await tick();
  await new Promise((resolve) => setTimeout(resolve, 30));
  flushSync();
}
afterEach(async () => {
  await unmount(component);
  vi.clearAllMocks();
});

describe('pairing runner instructions', () => {
  it.each([
    { kind: 'child workspace', runCommand: 'yip runner --state ~/.yip/runners/workspace-org', separate: true },
    { kind: 'root workspace', runCommand: 'yip runner', separate: false },
    { kind: 'older hub', runCommand: undefined, separate: false },
  ])('uses the matching run command for a $kind', async ({ runCommand, separate }) => {
    const enrollment: Enrollment = {
      id: 'enrollment-test', name: 'Office machine', expiresAt: new Date(Date.now() + 900_000).toISOString(),
      hubUrl: 'https://localhost:7443', hubFingerprint: 'test-fingerprint',
      command: `yip runner pair${separate ? ' --state ~/.yip/runners/workspace-org' : ''} --hub https://localhost:7443`,
      runCommand,
    };
    vi.mocked(api.createEnrollment).mockResolvedValue(enrollment);
    component = mount(AddMachineDialog, { target: document.body, props: { onclose: vi.fn() } });
    await settle();
    const input = document.querySelector<HTMLInputElement>('input')!;
    input.value = enrollment.name;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    await settle();
    expect(api.createEnrollment).toHaveBeenCalledWith({ name: enrollment.name });
    const codes = [...document.querySelectorAll('code')].map((code) => code.textContent);
    expect(codes).toContain(enrollment.command);
    expect(codes).toContain(runCommand || 'yip runner');
    if (separate) {
      expect(document.body.textContent).toContain('separate runner process');
      expect(document.body.textContent).not.toContain('yip service install runner');
    } else {
      expect(document.body.textContent).toContain('yip service install runner');
    }
  });
});
