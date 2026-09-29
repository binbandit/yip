import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import WorkspaceSwitcher from '../../src/components/WorkspaceSwitcher.svelte';

const { app } = vi.hoisted(() => ({
  app: {
    data: { org: { name: 'Work' } },
    currentWorkspacePath: '',
    workspaces: [
      { id: 'work', name: 'Work', path: '' },
      { id: 'personal', name: 'Personal', path: '/w/personal' },
    ],
    workspacesLoading: false,
    workspacesError: '',
    refreshWorkspaces: vi.fn<() => Promise<void>>(),
    addWorkspace: vi.fn<(name: string) => Promise<void>>(),
    switchWorkspace: vi.fn(),
  },
}));
vi.mock('../../src/lib/state/app.svelte', () => ({ app }));

let component: ReturnType<typeof mount>;
async function settle() {
  flushSync();
  await tick();
  await new Promise((resolve) => setTimeout(resolve, 30));
  flushSync();
}
function trigger() {
  return document.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!;
}
function item(label: string) {
  return [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((element) => element.textContent?.includes(label))!;
}
async function openCreate() {
  trigger().click();
  await settle();
  item('Create workspace').click();
  await settle();
}
function input(value: string) {
  const field = document.querySelector<HTMLInputElement>('input')!;
  field.value = value;
  field.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}
async function submit() {
  document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  await settle();
}
beforeEach(() => {
  app.data.org.name = 'Work';
  app.currentWorkspacePath = '';
  app.workspaces = [
    { id: 'work', name: 'Work', path: '' },
    { id: 'personal', name: 'Personal', path: '/w/personal' },
  ];
  app.workspacesLoading = false;
  app.workspacesError = '';
  app.refreshWorkspaces.mockReset().mockResolvedValue();
  app.addWorkspace.mockReset().mockResolvedValue();
  app.switchWorkspace.mockReset();
});
afterEach(async () => {
  if (component) await unmount(component);
  document.body.innerHTML = '';
});

describe('workspace switcher', () => {
  it('keeps the active name visible and checks the active menu item, switching only to another workspace', async () => {
    component = mount(WorkspaceSwitcher, { target: document.body });
    await settle();
    expect(trigger().textContent).toContain('Work');
    trigger().click();
    await settle();
    expect(app.refreshWorkspaces).toHaveBeenCalledOnce();
    expect(item('Work').textContent).toContain('Current workspace');
    expect(item('Work').querySelector('svg')).not.toBeNull();
    item('Work').click();
    expect(app.switchWorkspace).not.toHaveBeenCalled();
    trigger().click();
    await settle();
    item('Personal').click();
    expect(app.switchWorkspace).toHaveBeenCalledWith(app.workspaces[1]);
  });

  it('preserves the current workspace and offers retry and creation when discovery fails', async () => {
    app.workspaces = [];
    app.workspacesError = 'Could not load workspaces. Check your connection.';
    component = mount(WorkspaceSwitcher, { target: document.body });
    await settle();
    trigger().click();
    await settle();
    expect(item('Work').textContent).toContain('Current workspace');
    expect(item('Retry loading workspaces').textContent).toContain(app.workspacesError);
    item('Retry loading workspaces').click();
    expect(app.refreshWorkspaces).toHaveBeenCalledTimes(2);
    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(item('Create workspace')).toBeDefined();
  });

  it('shows loading without hiding the current name', async () => {
    app.workspaces = [];
    app.workspacesLoading = true;
    component = mount(WorkspaceSwitcher, { target: document.body });
    await settle();
    trigger().click();
    await settle();
    expect(trigger().textContent).toContain('Work');
    expect(item('Loading workspaces').getAttribute('aria-disabled')).toBe('true');
  });

  it('uses the current list entry when the bootstrap name is unavailable', async () => {
    app.data.org.name = '';
    app.currentWorkspacePath = '/w/personal';
    component = mount(WorkspaceSwitcher, { target: document.body });
    await settle();
    expect(trigger().textContent).toContain('Personal');
    trigger().click();
    await settle();
    expect(item('Personal').textContent).toContain('Current workspace');
    item('Work').click();
    expect(app.switchWorkspace).toHaveBeenCalledWith(app.workspaces[0]);
  });

  it('validates a required name and the 80-character limit before creating', async () => {
    component = mount(WorkspaceSwitcher, { target: document.body });
    await openCreate();
    expect(document.querySelector('input')?.maxLength).toBe(80);
    expect(document.querySelector('input')?.placeholder).toBe('e.g. Design studio');
    expect(document.querySelector('label')?.textContent).toContain('Workspace name');
    expect(document.querySelector('dialog')?.textContent).toContain('Pair a machine');
    input('   ');
    await submit();
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('Enter a workspace name');
    input('x'.repeat(81));
    await submit();
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('80 characters');
    expect(app.addWorkspace).not.toHaveBeenCalled();
  });

  it('keeps failed names for retry, prevents duplicate submits, and closes after success', async () => {
    app.addWorkspace.mockRejectedValueOnce(new Error('A workspace with this name already exists.'));
    component = mount(WorkspaceSwitcher, { target: document.body });
    await openCreate();
    input('  Personal  ');
    await submit();
    expect(app.addWorkspace).toHaveBeenCalledWith('Personal');
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('already exists');
    expect(document.querySelector('input')?.value).toBe('  Personal  ');
    let finish!: () => void;
    app.addWorkspace.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    input('Personal projects');
    await submit();
    expect(document.querySelector('form')?.getAttribute('aria-busy')).toBe('true');
    expect(document.querySelector('input')?.readOnly).toBe(true);
    await submit();
    expect(app.addWorkspace).toHaveBeenCalledTimes(2);
    finish();
    await settle();
    expect(document.querySelector('dialog[open]')).toBeNull();
  });

  it('opens from the compact trigger with the keyboard and cancels without creating', async () => {
    component = mount(WorkspaceSwitcher, { target: document.body, props: { compact: true } });
    await settle();
    trigger().focus();
    trigger().dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    await settle();
    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    item('Create workspace').click();
    await settle();
    expect(document.querySelectorAll('dialog[open]')).toHaveLength(1);
    const cancel = [...document.querySelectorAll<HTMLButtonElement>('dialog button')].find((button) => button.textContent?.includes('Cancel'))!;
    cancel.click();
    await settle();
    expect(document.querySelector('dialog[open]')).toBeNull();
    expect(app.addWorkspace).not.toHaveBeenCalled();
  });

  it('allows Escape to dismiss creation and returns focus to the switcher', async () => {
    component = mount(WorkspaceSwitcher, { target: document.body });
    await openCreate();
    document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    await settle();
    expect(document.querySelector('dialog[open]')).toBeNull();
    expect(document.activeElement).toBe(trigger());
    expect(app.addWorkspace).not.toHaveBeenCalled();
  });
});
