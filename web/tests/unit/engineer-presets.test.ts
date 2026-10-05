import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import CreateEngineerDialog from '../../src/components/CreateEngineerDialog.svelte';
import { api } from '../../src/lib/api/endpoints';
import { app } from '../../src/lib/state/app.svelte';
import { emptyState } from '../../src/lib/state/data';
import type { Bootstrap } from '../../src/lib/api/types.gen';
import { fixture } from './fakehub';
import { choose } from './controls';

let component: ReturnType<typeof mount>;
const close = vi.fn();
const engineer = fixture<Bootstrap>('bootstrap.json').engineers[0];

function control<T extends HTMLElement = HTMLInputElement>(label: string): T {
  const el = [...document.querySelectorAll<HTMLLabelElement>('label')].find((el) => el.textContent?.trim() === label);
  if (!el) throw new Error(`No label: ${label}`);
  return document.getElementById(el.htmlFor) as T;
}
function edit(label: string, value: string) {
  const input = control<HTMLInputElement | HTMLTextAreaElement>(label);
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}
function button(label: string) {
  const result = [...document.querySelectorAll<HTMLButtonElement>('button')].find((el) => el.textContent?.trim() === label);
  if (!result) throw new Error(`No button: ${label}`);
  return result;
}
function submit() {
  document.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  flushSync();
}

beforeEach(async () => {
  app.data = emptyState();
  app.data.providers = [
    { provider: 'test-one', label: 'Test one', readyNodes: ['node'], billing: 'unknown' },
    { provider: 'test-two', label: 'Test two', readyNodes: [], billing: 'unknown' },
  ];
  vi.spyOn(api, 'createEngineer').mockResolvedValue(engineer);
  vi.spyOn(app, 'go').mockImplementation(() => {});
  component = mount(CreateEngineerDialog, { target: document.body, props: { onclose: close } });
  flushSync();
  await tick();
});

afterEach(async () => {
  await unmount(component);
  vi.restoreAllMocks();
  close.mockClear();
});

describe('engineer presets', () => {
  it('starts with a generalist while leaving identity to the user', () => {
    expect(control('Name').value).toBe('');
    expect(control('Handle').value).toBe('');
    expect(control('Role').value).toBe('Generalist engineer');
    expect(control('Capabilities').value).toBe('full-stack, product, implementation');
    expect(control<HTMLTextAreaElement>('Standing instructions').value).toContain('smallest complete flow');
    expect(control<HTMLElement>('Starting point').getAttribute('role')).toBe('combobox');
    submit();
    expect(document.querySelector('[role=alert]')?.textContent).toContain('Give the engineer a name and a role.');
    expect(api.createEngineer).not.toHaveBeenCalled();
  });

  it('keeps identity, provider and edited fields while updating untouched defaults', () => {
    edit('Name', 'Ada Example');
    expect(control('Handle').value).toBe('ada-example');
    edit('Handle', 'ada-custom');
    choose(control('Provider preference'), 'Test two');
    edit('Standing instructions', 'Keep the checkout clean and check the changed flow.');
    edit("What they're for", 'Works on our dashboard.');
    choose(control('Starting point'), 'Frontend');
    expect(control('Role').value).toBe('Frontend engineer');
    expect(control('Capabilities').value).toBe('ui, accessibility, typescript');
    expect(control("What they're for").value).toBe('Works on our dashboard.');
    expect(control<HTMLTextAreaElement>('Standing instructions').value).toBe('Keep the checkout clean and check the changed flow.');
    choose(control('Starting point'), 'Backend');
    expect(control('Role').value).toBe('Backend engineer');
    expect(control('Capabilities').value).toBe('backend, api, databases');
    expect(control('Name').value).toBe('Ada Example');
    expect(control('Handle').value).toBe('ada-custom');
    expect(control('Provider preference').textContent).toContain('Test two');
    button('Reset to preset').click();
    flushSync();
    expect(control("What they're for").value).toBe('Builds APIs, data models and reliable service behaviour.');
    expect(control<HTMLTextAreaElement>('Standing instructions').value).toContain('transactions');
    expect(control('Name').value).toBe('Ada Example');
    expect(control('Handle').value).toBe('ada-custom');
    expect(control('Provider preference').textContent).toContain('Test two');
  });

  it('creates only on submit and sends the edited fields through the existing API', async () => {
    choose(control('Starting point'), 'Reviewer');
    edit('Name', '  Rowan  ');
    edit('Role', '  Release reviewer  ');
    edit("What they're for", '  Reviews release changes.  ');
    edit('Capabilities', 'review, releases, , regression ');
    edit('Standing instructions', '  Review the final revision and record any gaps.  ');
    expect(api.createEngineer).not.toHaveBeenCalled();
    submit();
    await tick();
    expect(api.createEngineer).toHaveBeenCalledExactlyOnceWith({
      name: 'Rowan', handle: 'rowan', role: 'Release reviewer',
      description: 'Reviews release changes.', capabilityTags: ['review', 'releases', 'regression'],
      instructions: 'Review the final revision and record any gaps.', provider: { provider: 'test-one' },
    });
    expect(close).toHaveBeenCalledOnce();
    expect(app.go).toHaveBeenCalledWith({ name: 'engineer', id: engineer.id });
  });

  it('offers an empty custom start, preserves edits on return and clears them only explicitly', () => {
    edit('Name', 'Robin');
    choose(control('Starting point'), 'Custom');
    for (const label of ['Role', "What they're for", 'Capabilities', 'Standing instructions']) {
      expect(control<HTMLInputElement | HTMLTextAreaElement>(label).value).toBe('');
    }
    submit();
    expect(api.createEngineer).not.toHaveBeenCalled();
    edit('Role', 'Documentation engineer');
    edit('Standing instructions', 'Cite the source.');
    choose(control('Starting point'), 'Security');
    expect(control('Role').value).toBe('Documentation engineer');
    expect(control('Capabilities').value).toBe('security, review, auth');
    choose(control('Starting point'), 'Custom');
    expect(control('Role').value).toBe('Documentation engineer');
    expect(control<HTMLTextAreaElement>('Standing instructions').value).toBe('Cite the source.');
    button('Clear custom fields').click();
    flushSync();
    expect(control('Role').value).toBe('');
    expect(control<HTMLTextAreaElement>('Standing instructions').value).toBe('');
    expect(control('Name').value).toBe('Robin');
    button('Cancel').click();
    expect(close).toHaveBeenCalledOnce();
    expect(api.createEngineer).not.toHaveBeenCalled();
  });

  it.each([
    { label: 'Chief Engineer', role: 'Chief engineer', tags: 'architecture, technical-direction, standards', purpose: 'across authorized projects', tools: ['knowledge_search', 'decision_propose', 'work_create'] },
    { label: 'Principal Engineer', role: 'Principal engineer', tags: 'system-design, implementation, review, mentoring', purpose: 'complex implementation and design', tools: ['work_run_check', 'work_request_review'] },
    { label: 'Engineering Manager', role: 'Engineering manager', tags: 'planning, delegation, coordination, delivery', purpose: 'owners, dependencies and progress', tools: ['work_create', 'work_status', 'work_request_help', 'work_update', 'work_wait', 'work_request_review'] },
  ])('$label provides distinct responsibilities without changing authority or identity', async ({ label, role, tags, purpose, tools }) => {
    choose(control('Starting point'), label);
    expect(control('Role').value).toBe(role);
    expect(control('Capabilities').value).toBe(tags);
    expect(control("What they're for").value).toContain(purpose);
    const instructions = control<HTMLTextAreaElement>('Standing instructions').value;
    for (const tool of tools) expect(instructions).toContain(tool);
    expect(instructions).toContain('approval requirements');
    expect(instructions).toContain('independent peer review');
    expect(instructions).toContain('extra authority');
    expect(control('Name').value).toBe('');
    expect(api.createEngineer).not.toHaveBeenCalled();
    edit('Name', 'Sage');
    edit('Role', `${role} for Atlas`);
    edit('Standing instructions', `${instructions} Focus on Atlas.`);
    submit();
    await tick();
    expect(api.createEngineer).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Sage', handle: 'sage', role: `${role} for Atlas`,
      instructions: `${instructions} Focus on Atlas.`,
      capabilityTags: tags.split(', '), provider: { provider: 'test-one' },
    }));
  });
});
