import { execFile } from 'node:child_process';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { expect, test, YIP_BIN } from './fixtures';

test('remove a revoked machine with confirmation, errors and live updates', async ({ app, api, context, hub }) => {
  // Pair a separate idle identity: the scripted harness exits when its own
  // runner is revoked. This state lives only in the disposable test directory.
  const name = 'Retired browser fixture';
  const enrollment = await api.req('POST', '/v1/nodes/enrollments', { name });
  await promisify(execFile)(YIP_BIN, ['runner', 'pair', '--state', join(hub.dataDir, 'retired-runner'),
    '--hub', enrollment.hubUrl, '--fingerprint', enrollment.hubFingerprint, '--token', enrollment.token, '--name', name]);
  const node = (await api.nodes()).find((n) => n.name === name)!;
  await app.goto('/machines');
  await app.getByRole('button', { name: `Details for ${node.name}`, exact: true }).click();
  await expect(app.getByRole('button', { name: 'Remove machine…', exact: true })).toHaveCount(0);
  await app.getByRole('button', { name: 'Revoke access…', exact: true }).click();
  await app.getByRole('alertdialog').getByRole('button', { name: 'Revoke access', exact: true }).click();
  await expect(app.getByRole('button', { name: 'Remove machine…', exact: true })).toBeVisible();
  expect((await api.nodes()).find((n) => n.id === node.id).status).toBe('revoked');

  const other = await context.newPage();
  await other.goto(`${hub.url}/machines?panel=machine%3A${node.id}`);
  await expect(other.getByRole('button', { name: 'Remove machine…', exact: true })).toBeVisible();

  await app.getByRole('button', { name: 'Remove machine…', exact: true }).click();
  const confirmation = app.getByRole('alertdialog');
  await expect(confirmation).toContainText('Its runs, work history and reported workspaces stay on the hub');
  await expect(confirmation).toContainText('its local files stay on the machine');
  await expect(confirmation).toContainText('Its access stays revoked');
  await expect(confirmation.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
  await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(app.getByRole('button', { name: 'Remove machine…', exact: true })).toBeFocused();
  expect((await api.nodes()).some((n) => n.id === node.id)).toBe(true);

  const endpoint = `${hub.url}/v1/nodes/${node.id}`;
  await app.route(endpoint, (route) => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'unavailable', message: 'Test connection interrupted. Try again.', recoverable: true }),
  }));
  await app.getByRole('button', { name: 'Remove machine…', exact: true }).click();
  await confirmation.getByRole('button', { name: 'Remove machine', exact: true }).click();
  await expect(confirmation.getByRole('alert')).toContainText('Test connection interrupted. Try again.');
  expect((await api.nodes()).some((n) => n.id === node.id)).toBe(true);

  await app.unroute(endpoint);
  await confirmation.getByRole('button', { name: 'Remove machine', exact: true }).click();
  await expect(confirmation).toHaveCount(0);
  await expect(app.locator('aside.panel')).toHaveCount(0);
  await expect(app.getByRole('heading', { name: 'Machines', exact: true })).toBeFocused();
  await expect(app.getByRole('button', { name: `Details for ${node.name}`, exact: true })).toHaveCount(0);
  await expect(other.getByText('This machine isn’t available.', { exact: true })).toBeVisible();
  await expect(other.getByRole('button', { name: `Details for ${node.name}`, exact: true })).toHaveCount(0);
  expect((await api.nodes()).some((n) => n.id === node.id)).toBe(false);
  await api.req('DELETE', `/v1/nodes/${node.id}`);
  await app.reload();
  await expect(app.getByRole('heading', { name: 'Machines', exact: true })).toBeVisible();
  await expect(app.getByRole('button', { name: `Details for ${node.name}`, exact: true })).toHaveCount(0);
  await other.close();
});
