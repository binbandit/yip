// Setup, sign-in, sign-out, session expiry and the boot error screen.
import { expect, test } from './fixtures';
import { composer, draftSaved, openRoom, signIn } from './helpers';

test.describe('sign in', () => {
  test('names the workspace and asks for both fields', async ({ page, hub }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/\/signin/);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(`Sign in to ${hub.handle.replace(/^./, (c) => c.toUpperCase())}'s workspace`);
    await expect(page.getByLabel('Handle')).toBeFocused();
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toHaveText('Enter your handle and password.');
    await page.getByLabel('Handle').fill(hub.handle);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toHaveText('Enter your handle and password.');
    await expect(page).toHaveURL(/\/signin/);
  });

  test('shows the hub’s message for a wrong password', async ({ page, hub }) => {
    await page.goto('/signin');
    await page.getByLabel('Handle').fill(hub.handle);
    await page.getByLabel('Password').fill('not-the-password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toHaveText("That handle and password don't match.");
    await expect(page).toHaveURL(/\/signin/);
    // The form stays usable: the right password gets in.
    await page.getByLabel('Password').fill(hub.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  });

  test('accepts a handle typed with a leading @', async ({ page, hub }) => {
    await page.goto('/signin');
    await page.getByLabel('Handle').fill(`@${hub.handle}`);
    await page.getByLabel('Password').fill(hub.password);
    await page.getByLabel('Password').press('Enter');
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    await expect(page).toHaveURL(/\/overview$/);
  });

  test('returns to the page that asked for sign-in', async ({ page, hub }) => {
    await page.goto('/engineers');
    await expect(page).toHaveURL(/\/signin\?next=%2Fengineers/);
    await page.getByLabel('Handle').fill(hub.handle);
    await page.getByLabel('Password').fill(hub.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).toHaveURL(/\/engineers$/);
    await expect(page.getByRole('heading', { name: 'Engineers', level: 1 })).toBeVisible();
  });

  test('ignores a next address outside the app', async ({ page, hub }) => {
    await page.goto('/signin?next=%2F%2Fevil.example%2F');
    await page.getByLabel('Handle').fill(hub.handle);
    await page.getByLabel('Password').fill(hub.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).toHaveURL(new RegExp(`^${hub.url}/overview$`));
  });

  test('a signed-in visit to /signin goes to the Overview', async ({ app: page }) => {
    await page.goto('/signin');
    await expect(page).toHaveURL(/\/overview$/);
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  });
});

test.describe('sign out', () => {
  test('from the profile menu, and the session is gone afterwards', async ({ app: page }) => {
    await page.getByRole('button', { name: 'Your profile' }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page).toHaveURL(/\/signin$/);
    await expect(page.getByLabel('Handle')).toBeVisible();
    await page.goto('/overview');
    await expect(page).toHaveURL(/\/signin\?next=%2Foverview/);
  });

  test('from Settings', async ({ app: page }) => {
    await page.goto('/settings');
    await page.getByRole('button', { name: 'Sign out' }).click();
    await expect(page).toHaveURL(/\/signin$/);
  });
});

test('an ended session sends you to sign in and keeps your draft', async ({ app: page, hub }) => {
  await openRoom(page, 'Security');
  const roomUrl = new URL(page.url()).pathname;
  await composer(page).fill('A draft that should survive sign-in');
  await draftSaved(page, 'A draft that should survive sign-in');
  await page.context().clearCookies();
  await page.goto(roomUrl);
  await expect(page).toHaveURL(/\/signin\?next=/);
  await page.getByLabel('Handle').fill(hub.handle);
  await page.getByLabel('Password').fill(hub.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#room-title')).toHaveText(/Security/);
  await expect(composer(page)).toHaveValue('A draft that should survive sign-in');
});

test('a 401 during use shows the session-ended notice', async ({ app: page }) => {
  await openRoom(page, 'Security');
  await page.route('**/v1/rooms/*/messages', (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ code: 'unauthorized', message: 'Sign in to continue.' }) })
      : route.continue(),
  );
  await composer(page).fill('This send meets an ended session');
  await composer(page).press('Enter');
  await expect(page).toHaveURL(/\/signin\?next=%2Frooms%2F/);
  await expect(page.getByText('Your session ended. Sign in again — your drafts are saved on this device.')).toBeVisible();
});

test.describe('boot', () => {
  test('shows a loading state, then an error with Try again when the hub is unreachable', async ({ page }) => {
    let fail = true;
    await page.route('**/v1/setup', async (route) => {
      if (fail) return route.abort('connectionrefused');
      return route.continue();
    });
    await page.goto('/');
    await expect(page.getByRole('heading', { name: "Can't reach your workspace.", level: 1 })).toBeVisible();
    await expect(page.getByText(/Drafts you were writing are saved on this device\./)).toBeVisible();
    fail = false;
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page).toHaveURL(/\/signin/);
  });

  test('shows the hub’s message when bootstrap fails', async ({ app: page }) => {
    await page.route('**/v1/bootstrap', (route) =>
      route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ code: 'internal', message: 'The database is locked.' }) }),
    );
    await page.reload();
    await expect(page.getByRole('heading', { name: "Can't reach your workspace.", level: 1 })).toBeVisible();
    await expect(page.getByText(/The database is locked\./)).toBeVisible();
    await page.unroute('**/v1/bootstrap');
    await page.getByRole('button', { name: 'Try again' }).click();
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
  });

  test('says it is opening the workspace while it loads', async ({ page }) => {
    let release!: () => void;
    const held = new Promise<void>((r) => (release = r));
    await page.route('**/v1/setup', async (route) => {
      await held;
      await route.continue();
    });
    await page.goto('/');
    await expect(page.getByRole('main', { name: 'Loading your workspace' })).toBeVisible();
    await expect(page.getByText('Opening your workspace…')).toBeVisible();
    release();
    await expect(page).toHaveURL(/\/signin/);
  });
});

test.describe('setup', () => {
  test.use({ hubKind: 'fresh' });

  test('any address leads to setup, and every field is checked', async ({ page }) => {
    await page.goto('/engineers');
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole('heading', { name: 'Set up your workspace', level: 1 })).toBeVisible();
    await page.getByRole('button', { name: 'Create workspace' }).click();
    await expect(page.getByRole('main').getByRole('alert')).toHaveText('Check the highlighted fields.');
    for (const msg of [
      'Enter the one-time setup code shown where the hub is running.',
      'Name your workspace.',
      'Enter your name.',
      'Use 2–32 lowercase letters, numbers, dots, dashes or underscores.',
      'Use at least 10 characters.',
    ]) {
      await expect(page.getByRole('main').getByText(msg)).toBeVisible();
    }
    await page.getByLabel('Password', { exact: true }).fill('long-enough-1');
    await page.getByLabel('Confirm password').fill('something-else');
    await page.getByRole('button', { name: 'Create workspace' }).click();
    await expect(page.getByRole('main').getByText("The passwords don't match.")).toBeVisible();
  });

  test('suggests a handle from the name until you edit it', async ({ page }) => {
    await page.goto('/setup');
    await page.getByLabel('Your name').fill('Ada Lovelace');
    await expect(page.getByLabel('Handle')).toHaveValue('ada-lovelace');
    await expect(page.getByText('Engineers mention you as @ada-lovelace.')).toBeVisible();
    await page.getByLabel('Handle').fill('ada');
    await page.getByLabel('Your name').fill('Ada King');
    await expect(page.getByLabel('Handle')).toHaveValue('ada');
  });

  test('rejects a wrong setup code with the hub’s message', async ({ page }) => {
    await page.goto('/setup');
    await page.getByLabel('One-time setup code').fill('not-a-real-code');
    await page.getByLabel('Workspace name').fill('Test workspace');
    await page.getByLabel('Your name').fill('Ada');
    await page.getByLabel('Password', { exact: true }).fill('a-long-password');
    await page.getByLabel('Confirm password').fill('a-long-password');
    await page.getByRole('button', { name: 'Create workspace' }).click();
    await expect(page.getByRole('main').getByRole('alert')).toContainText('That setup code is invalid or expired.');
  });

  test('creates the workspace and opens it; sign-in works afterwards', async ({ page, hub }) => {
    await page.goto('/setup');
    await page.getByLabel('One-time setup code').fill(hub.setupCode);
    await page.getByLabel('Workspace name').fill('Lovelace Labs');
    await page.getByLabel('Your name').fill('Ada');
    await page.getByLabel('Password', { exact: true }).fill('a-long-password');
    await page.getByLabel('Confirm password').fill('a-long-password');
    await page.getByRole('button', { name: 'Create workspace' }).click();
    await expect(page.getByRole('heading', { name: 'Overview', level: 1 })).toBeVisible();
    await expect(page).toHaveURL(/\/overview$/);
    await expect(page.getByRole('region', { name: 'Getting started' })).toBeVisible();
    // Setup is done: the address now leads to sign-in, not setup.
    await page.getByRole('button', { name: 'Your profile' }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page).toHaveURL(/\/signin$/);
    await page.goto('/setup');
    await expect(page).toHaveURL(/\/signin/);
    await expect(page.getByRole('heading', { name: 'Sign in to Lovelace Labs' })).toBeVisible();
    await signIn(page, { ...hub, handle: 'ada', password: 'a-long-password' });
  });
});
