// A room: its header, the composer, the message history and what you can do
// with a message. Security is quiet (Mira and Oren, with Atlas); Engineering
// is answered by Mira (Mira and Pip, with Atlas and Beacon).
import type { Page, Route } from '@playwright/test';
import { expect, test, type HubApi } from './fixtures';
import { composer, draftSaved, message, openRoom, panel, panelTitle, send } from './helpers';

const replyFromMira = /deterministic fake provider/;

/** Posts `n` numbered messages as the owner; three lines each, so a few fill the screen. */
async function seed(api: HubApi, room: string, n: number): Promise<void> {
  for (let i = 1; i <= n; i++) {
    await api.post(room, `Seed message ${String(i).padStart(3, '0')}\nsecond line\nthird line`);
  }
}

function history(page: Page) {
  return page.getByRole('region', { name: /^Messages in / });
}

/** Shows a message's action toolbar, as hovering it does. */
async function actions(page: Page, text: string | RegExp) {
  const row = message(page, text);
  await row.hover();
  const bar = row.getByRole('toolbar', { name: 'Message actions' });
  await expect(bar).toBeVisible();
  return bar;
}

/** Handles the room's message POSTs with `handler`; everything else goes to the hub. */
async function onPost(page: Page, handler: (route: Route) => Promise<void>): Promise<void> {
  await page.route(/\/v1\/rooms\/[^/]+\/messages$/, (route) => (route.request().method() === 'POST' ? handler(route) : route.fallback()));
}

test.describe('header', () => {
  test('names the room, its purpose, projects, reply mode and members', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const header = page.locator('header.room-head');
    await expect(header).toContainText('Small details. Strong foundations.');
    await expect(header).toContainText('Quiet — only mentioned engineers reply');
    await header.getByRole('link', { name: 'Atlas' }).click();
    await expect(page).toHaveURL(/\/projects\//);
    await expect(page.getByRole('heading', { name: 'Atlas', level: 1 })).toBeVisible();

    await openRoom(page, 'Engineering');
    await expect(page.locator('header.room-head')).toContainText('Mira answers unaddressed messages');
    await expect(page.locator('header.room-head').getByRole('link', { name: 'Beacon' })).toBeVisible();
  });

  test('the members and settings buttons open the room settings', async ({ app: page }) => {
    await openRoom(page, 'Security');
    // Members who joined together are listed in either order.
    await page.getByRole('button', { name: /^Members: (Mira, Platform engineer; Oren, Security engineer|Oren, Security engineer; Mira, Platform engineer)\. Manage room$/ }).click();
    await expect(panelTitle(page)).toHaveText('Room settings');
    await expect(page).toHaveURL(/panel=room/);
    await panel(page).getByRole('button', { name: 'Close panel' }).click();
    await expect(panel(page)).toBeHidden();
    await page.getByRole('button', { name: 'Room settings' }).click();
    await expect(panelTitle(page)).toHaveText('Room settings');
  });

  test('an empty room says who is here and who replies', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await expect(page.getByText(/^Tell (Mira and Oren|Oren and Mira) what you're working on\.$/)).toBeVisible();
    await expect(page.getByText('This room is quiet: only the engineers you mention reply.')).toBeVisible();
    await openRoom(page, 'Engineering');
    await expect(page.getByText(/^Tell (Mira and Pip|Pip and Mira) what you're working on\.$/)).toBeVisible();
    await expect(page.getByText("Mira answers messages that don't mention anyone.")).toBeVisible();
  });
});

test.describe('composer', () => {
  test('sends on Enter, adds a line on Shift+Enter and is disabled when empty', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const box = composer(page);
    const sendButton = page.getByRole('button', { name: 'Send (Enter)' });
    await expect(box).toHaveAccessibleName('Message Security');
    await expect(box).toHaveAccessibleDescription(/Enter sends; Shift and Enter adds a new line\. Type @ to mention an engineer\./);
    await expect(sendButton).toBeDisabled();
    await box.fill('   ');
    await expect(sendButton).toBeDisabled();

    await box.fill('first line');
    await box.press('Shift+Enter');
    await box.pressSequentially('second line');
    await expect(box).toHaveValue('first line\nsecond line');
    await expect(sendButton).toBeEnabled();
    await box.press('Enter');
    await expect(box).toHaveValue('');
    await expect(message(page, 'first line')).toContainText('second line');
    await expect(sendButton).toBeDisabled();

    await box.fill('sent with the button');
    await sendButton.click();
    await expect(message(page, 'sent with the button')).toBeVisible();
    await expect(box).toBeFocused();
  });

  test('the mention list works by keyboard and mouse, and names who is asked', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const box = composer(page);
    const list = page.getByRole('listbox', { name: 'People you can mention' });
    await box.focus();
    await box.pressSequentially('@');
    await expect(list).toBeVisible();
    await expect(box).toHaveAttribute('aria-expanded', 'true');
    await expect(list.getByRole('option')).toHaveCount(3);

    // Pip isn't in this room: listed, but can't be chosen.
    const pip = list.getByRole('option', { name: /Pip/ });
    await expect(pip).toHaveAttribute('aria-disabled', 'true');
    await expect(pip).toContainText('Not in this room — add them in room settings first');

    // Arrow keys move the active option, wrapping around.
    const active = list.locator('[aria-selected="true"]');
    const first = await active.textContent();
    await box.press('ArrowDown');
    await expect(active).not.toHaveText(first ?? '');
    await box.press('ArrowUp');
    await expect(active).toHaveText(first ?? '');
    await box.press('ArrowUp');
    await expect(list.getByRole('option').last()).toHaveAttribute('aria-selected', 'true');

    // Escape closes the list and keeps the text.
    await box.press('Escape');
    await expect(list).toBeHidden();
    await expect(box).toHaveValue('@');

    // Filtering, then Tab chooses.
    await box.pressSequentially('ore');
    await expect(list.getByRole('option')).toHaveCount(1);
    await box.press('Tab');
    await expect(box).toHaveValue('@oren ');
    await expect(page.getByText('Asking Oren')).toBeVisible();

    // A click chooses too; Enter on an unavailable person does nothing.
    await box.pressSequentially('and @pi');
    await box.press('Enter');
    await expect(box).toHaveValue('@oren and @pi');
    await box.press('Backspace');
    await box.press('Backspace');
    await box.pressSequentially('mi');
    await list.getByRole('option', { name: /Mira/ }).click();
    await expect(box).toHaveValue('@oren and @mira ');
    await expect(page.getByText('Asking Oren, Mira')).toBeVisible();

    // Deleting a mention's text stops asking them.
    await box.fill('@oren only');
    await expect(page.getByText('Asking Oren', { exact: true })).toBeVisible();
    await box.fill('nobody');
    await expect(page.getByText(/^Asking /)).toBeHidden();
  });

  test('the mention button starts a mention where the cursor is', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const box = composer(page);
    await box.fill('hello');
    await page.getByRole('button', { name: 'Mention someone' }).click();
    await expect(box).toHaveValue('hello @');
    await expect(box).toBeFocused();
    await expect(page.getByRole('listbox', { name: 'People you can mention' })).toBeVisible();
  });

  test('a mentioned engineer replies and their name opens their profile', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const box = composer(page);
    await box.pressSequentially('@mi');
    await box.press('Enter');
    await box.pressSequentially('what is the weather like?');
    await box.press('Enter');
    const reply = message(page, replyFromMira);
    await expect(reply).toBeVisible();
    await expect(reply).toHaveAttribute('aria-label', /^Mira, AI engineer, Platform engineer, /);
    await reply.getByRole('button', { name: 'Mira' }).click();
    await expect(panelTitle(page)).toHaveText('Mira');
    await expect(page).toHaveURL(/panel=engineer/);
  });

  test('project context can be chosen, and is picked up from the text', async ({ app: page }) => {
    await openRoom(page, 'Security');
    const box = composer(page);
    const context = page.getByRole('button', { name: 'Add project context' });
    const chosen = page.getByRole('button', { name: 'Atlas', exact: true }).and(page.locator('.ctx'));
    const picker = page.getByRole('group', { name: 'Projects for this message' });

    // Naming a room project adds it; taking the name out removes it again.
    await box.fill('what does Atlas do on refresh?');
    await expect(chosen).toBeVisible();
    await box.fill('what does it do on refresh?');
    await expect(context).toBeVisible();
    // A detected project you take off stays off while you keep typing.
    await box.fill('what does Atlas do on refresh?');
    await chosen.click();
    await picker.getByRole('checkbox', { name: 'Atlas' }).uncheck();
    await picker.getByRole('button', { name: 'Done' }).click();
    await box.pressSequentially(' And Atlas on sign-in?');
    await expect(context).toBeVisible();
    await box.fill('');

    await context.click();
    await expect(context).toHaveAttribute('aria-expanded', 'true');
    await picker.getByRole('checkbox', { name: 'Atlas' }).check();
    await picker.getByRole('button', { name: 'Done' }).click();
    await expect(picker).toBeHidden();
    await expect(chosen).toBeVisible();
    // A chosen project stays chosen after sending.
    await send(page, 'a question about sessions');
    await expect(chosen).toBeVisible();
    await chosen.click();
    await picker.getByRole('checkbox', { name: 'Atlas' }).uncheck();
    await picker.getByRole('button', { name: 'Done' }).click();
    await expect(context).toBeVisible();
  });

  test('drafts are kept per room on this device', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await composer(page).fill('a thought for Security');
    await draftSaved(page, 'a thought for Security');
    await openRoom(page, 'Engineering');
    await expect(composer(page)).toHaveValue('');
    await openRoom(page, 'Security');
    await expect(composer(page)).toHaveValue('a thought for Security');
    await page.reload();
    await expect(composer(page)).toHaveValue('a thought for Security');
    await composer(page).press('Enter');
    await expect(message(page, 'a thought for Security')).toBeVisible();
    await page.reload();
    await expect(composer(page)).toHaveValue('');
  });

  test('with Ctrl+Enter chosen as the send key, Enter adds a line', async ({ app: page }) => {
    await page.goto('/settings');
    const saved = page.waitForResponse((r) => r.url().endsWith('/v1/preferences') && r.request().method() === 'PUT' && r.ok());
    await page.getByRole('radio', { name: '⌘/Ctrl+Enter sends, Enter adds a line' }).check();
    await saved;
    await page.reload();
    await openRoom(page, 'Security');
    const box = composer(page);
    await expect(page.getByRole('button', { name: 'Send (⌘/Ctrl+Enter)' })).toBeVisible();
    await expect(box).toHaveAccessibleDescription(/Command or Control and Enter sends; Enter adds a new line\./);
    await box.fill('one');
    await box.press('Enter');
    await box.pressSequentially('two');
    await expect(box).toHaveValue('one\ntwo');
    await box.press('Control+Enter');
    await expect(box).toHaveValue('');
    await expect(message(page, 'one')).toContainText('two');
  });
});

test.describe('messages', () => {
  test('reactions are added from the palette and toggled from the message', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await send(page, 'react to me');
    const bar = await actions(page, 'react to me');
    await bar.getByRole('button', { name: 'Add reaction' }).click();
    for (const e of ['👍', '✅', '👀', '🎉', '❤️', '🙏']) await expect(page.getByRole('button', { name: `React ${e}` })).toBeVisible();
    await page.getByRole('button', { name: 'React 🎉' }).click();

    const row = message(page, 'react to me');
    const mine = row.getByRole('button', { name: '🎉 1, including you. Remove your reaction' });
    await expect(mine).toBeVisible();
    await expect(mine).toHaveAttribute('aria-pressed', 'true');
    await mine.click();
    await expect(row.getByRole('list', { name: 'Reactions' })).toBeHidden();
  });

  test('your own message can be edited and removed', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await send(page, 'a message with a typo');

    // Cancel and Escape leave it as it was.
    let bar = await actions(page, 'a message with a typo');
    await bar.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    const edit = page.getByRole('textbox', { name: 'Edit message' });
    await expect(edit).toBeFocused();
    await expect(edit).toHaveValue('a message with a typo');
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect(edit).toBeHidden();

    bar = await actions(page, 'a message with a typo');
    await bar.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    await edit.fill('changed, then abandoned');
    await edit.press('Escape');
    await expect(edit).toBeHidden();
    await expect(message(page, 'a message with a typo')).toBeVisible();

    // Save with the button, then with Ctrl+Enter.
    bar = await actions(page, 'a message with a typo');
    await bar.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    await edit.fill('a message without a typo');
    await page.getByRole('button', { name: 'Save' }).click();
    const row = message(page, 'a message without a typo');
    await expect(row).toContainText('(edited)');

    bar = await actions(page, 'a message without a typo');
    await bar.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    await edit.fill('edited twice');
    await edit.press('Control+Enter');
    await expect(message(page, 'edited twice')).toContainText('(edited)');

    bar = await actions(page, 'edited twice');
    await bar.getByRole('button', { name: 'More actions' }).click();
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    await expect(message(page, 'This message was removed.')).toBeVisible();
    await expect(message(page, 'edited twice')).toHaveCount(0);
    await page.reload();
    await expect(message(page, 'This message was removed.')).toBeVisible();
  });

  test("an engineer's message can't be edited or removed", async ({ app: page, api }) => {
    await openRoom(page, 'Security');
    await api.post('Security', '@Mira hello', { mentions: ['Mira'] });
    const bar = await actions(page, replyFromMira);
    await expect(bar.getByRole('button', { name: 'Add reaction' })).toBeVisible();
    await expect(bar.getByRole('button', { name: 'More actions' })).toHaveCount(0);
  });

  test('copying a link to a message, and following one', async ({ app: page, api, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await seed(api, 'Security', 65);
    await openRoom(page, 'Security');
    const bar = await actions(page, 'Seed message 065');
    await bar.getByRole('button', { name: 'Copy link to message' }).click();
    await expect(page.getByText('Link copied.').first()).toBeVisible();
    const link = await page.evaluate(() => navigator.clipboard.readText());
    const id = (await api.messages('Security')).find((m) => m.body.startsWith('Seed message 065')).id;
    expect(link).toBe(`${new URL(page.url()).origin}/rooms/${api.room('Security').id}?msg=${id}`);

    // A link to a message older than the first page loads back to it.
    const oldest = (await api.messages('Security')).find((m) => m.body.startsWith('Seed message 001'));
    await page.goto(`/rooms/${api.room('Security').id}?msg=${oldest.id}`);
    const row = page.locator(`article[data-message-id="${oldest.id}"]`);
    await expect(row).toBeFocused();
    await expect(row).toHaveClass(/highlight/);
    await expect(row).toBeInViewport();
  });

  test('a thread: replies, the summary and the thread composer', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await send(page, 'start a thread here');
    const bar = await actions(page, 'start a thread here');
    await bar.getByRole('button', { name: 'Reply in thread' }).click();
    await expect(panelTitle(page)).toHaveText('Thread');
    await expect(panel(page)).toContainText('Security');
    await expect(panel(page)).toContainText('No replies yet');
    await expect(page).toHaveURL(/panel=thread/);

    const reply = panel(page).getByRole('combobox', { name: 'Reply in thread' });
    await expect(reply).toBeVisible();
    await reply.fill('the first reply');
    await reply.press('Enter');
    await expect(panel(page).getByRole('region', { name: 'Replies' }).locator('article').filter({ hasText: 'the first reply' })).toBeVisible();
    await expect(panel(page)).toContainText('1 reply');

    // Replies stay out of the room's timeline; the root shows a summary.
    await panel(page).getByRole('button', { name: 'Close panel' }).click();
    await expect(history(page).getByText('the first reply')).toHaveCount(0);
    const summary = message(page, 'start a thread here').getByRole('button', { name: /^1 reply · last reply / });
    await expect(summary).toBeVisible();
    await summary.click();
    await expect(panelTitle(page)).toHaveText('Thread');
    await expect(panel(page)).toContainText('the first reply');

    // A thread reply has its own draft.
    await reply.fill('a thread draft');
    await draftSaved(page, 'a thread draft');
    await expect(composer(page)).toHaveValue('');
  });

  test('messages render bold, code and safe links', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await send(page, 'This is **important**, see `Validate()` and [the docs](https://example.com/docs) but not [this](javascript:alert(1))');
    const row = message(page, 'This is');
    await expect(row.locator('strong')).toHaveText('important');
    await expect(row.locator('code')).toHaveText('Validate()');
    const docs = row.getByRole('link', { name: 'the docs' });
    await expect(docs).toHaveAttribute('href', 'https://example.com/docs');
    await expect(docs).toHaveAttribute('target', '_blank');
    await expect(docs).toHaveAttribute('rel', /noopener/);
    await expect(row.getByRole('link')).toHaveCount(1);
  });

  test('arrow keys, j/k, Home and End move between messages', async ({ app: page, api }) => {
    for (const w of ['alpha', 'bravo', 'charlie', 'delta']) await api.post('Security', `${w} note`);
    await openRoom(page, 'Security');
    const at = (w: string) => message(page, `${w} note`);
    // Only the newest message is in the tab order.
    await expect(at('delta')).toHaveAttribute('tabindex', '0');
    await expect(at('alpha')).toHaveAttribute('tabindex', '-1');
    await at('delta').focus();
    await page.keyboard.press('ArrowUp');
    await expect(at('charlie')).toBeFocused();
    await page.keyboard.press('k');
    await expect(at('bravo')).toBeFocused();
    await page.keyboard.press('Home');
    await expect(at('alpha')).toBeFocused();
    await page.keyboard.press('j');
    await expect(at('bravo')).toBeFocused();
    await page.keyboard.press('ArrowDown');
    await expect(at('charlie')).toBeFocused();
    await page.keyboard.press('End');
    await expect(at('delta')).toBeFocused();
    // Typing in the composer keeps its own keys.
    await composer(page).fill('typing k and j');
    await composer(page).press('ArrowUp');
    await expect(composer(page)).toBeFocused();
  });
});

test.describe('history', () => {
  test('earlier messages load from the button and by scrolling up', async ({ app: page, api }) => {
    await seed(api, 'Security', 65);
    await openRoom(page, 'Security');
    await expect(message(page, 'Seed message 065')).toBeInViewport();
    await expect(page.locator('article[data-message-id]')).toHaveCount(60);
    const older = page.getByRole('button', { name: 'Load earlier messages' });
    await expect(older).toBeAttached();
    // Clicked where it is, without scrolling it into view (which loads as well).
    await older.dispatchEvent('click');
    await expect(page.locator('article[data-message-id]')).toHaveCount(65);
    await expect(older).toHaveCount(0);

    await page.reload();
    await expect(page.locator('article[data-message-id]')).toHaveCount(60);
    await history(page).evaluate((el) => (el.scrollTop = 0));
    await expect(page.locator('article[data-message-id]')).toHaveCount(65);
    await expect(message(page, 'Seed message 001')).toBeAttached();
  });

  test('a long history offers a way back to the latest message', async ({ app: page, api }) => {
    await seed(api, 'Security', 25);
    await openRoom(page, 'Security');
    await expect(message(page, 'Seed message 025')).toBeInViewport();
    await expect(page.getByRole('separator', { name: 'Today' })).toBeAttached();
    await history(page).evaluate((el) => (el.scrollTop = 0));
    const jump = page.getByRole('button', { name: 'Jump to latest' });
    await expect(jump).toBeVisible();
    await jump.click();
    await expect(message(page, 'Seed message 025')).toBeInViewport();
    await expect(jump).toBeHidden();
  });

  test('messages you missed are marked when you come back', async ({ app: page, api }) => {
    await openRoom(page, 'Security');
    await send(page, 'before I left');
    await page.getByRole('navigation', { name: 'Workspace' }).getByRole('link', { name: 'Projects', exact: true }).click();
    await api.post('Security', '@Mira are you there?', { mentions: ['Mira'] });
    await api.waitFor("Mira's reply", async () => (await api.messages('Security')).find((m) => replyFromMira.test(m.body)));
    await openRoom(page, 'Security');
    const divider = page.getByLabel('New messages since you were here');
    await expect(divider).toBeVisible();
    // The divider sits right before the first message from someone else.
    const next = await divider.evaluate((d) => d.nextElementSibling?.textContent ?? '');
    expect(next).toMatch(replyFromMira);
  });
});

test.describe('with a slower provider', () => {
  test.use({ fakeDelay: '2s' });

  test('new messages while you read earlier ones show a count, not a jump', async ({ app: page, api }) => {
    await seed(api, 'Security', 25);
    await openRoom(page, 'Security');
    await api.post('Security', '@Mira can you fix Atlas accepting expired sessions?', { mentions: ['Mira'] });
    await expect(message(page, 'On it.')).toBeVisible();
    await history(page).evaluate((el) => (el.scrollTop = el.scrollHeight));
    await expect(message(page, 'On it.')).toBeInViewport();
    await history(page).evaluate((el) => (el.scrollTop = 0));
    await expect(page.getByRole('button', { name: 'Jump to latest' })).toBeVisible();
    // Mira's result arrives after her paced steps.
    const pill = page.getByRole('button', { name: /^\d+ new messages?$/ });
    await expect(pill).toBeVisible({ timeout: 60_000 });
    await expect(message(page, 'Seed message 001')).toBeInViewport();
    await pill.click();
    await expect(pill).toBeHidden();
    await expect(page.locator('article[data-message-id]').last()).toBeInViewport();
  });
});

test.describe('sending', () => {
  test('a send that is still going shows as sending', async ({ app: page }) => {
    await openRoom(page, 'Security');
    let release!: () => void;
    const held = new Promise<void>((r) => (release = r));
    await onPost(page, async (route) => {
      await held;
      await route.continue();
    });
    await composer(page).fill('slow to send');
    await composer(page).press('Enter');
    const pending = page.getByRole('article', { name: 'Your message, sending' });
    await expect(pending).toContainText('Sending…');
    await expect(pending).toContainText('slow to send');
    release();
    await expect(pending).toBeHidden();
    await expect(message(page, 'slow to send')).toBeVisible();
  });

  test("a send that can't reach the hub is kept for Retry, Edit or Discard", async ({ app: page }) => {
    await openRoom(page, 'Security');
    await onPost(page, (route) => route.abort('connectionfailed'));
    await send(page, 'lost in transit');
    const failed = page.getByRole('article', { name: 'Your message, not sent' });
    await expect(failed.getByRole('alert')).toHaveText(/Not sent\. Can't reach your workspace\. Your message is saved on this device\./);

    // It survives a reload.
    await page.reload();
    await expect(failed).toContainText('lost in transit');
    await expect(failed.getByRole('alert')).toContainText('Not sent yet.');

    // Discard drops it for good.
    await failed.getByRole('button', { name: 'Discard' }).click();
    await expect(failed).toHaveCount(0);
    await page.reload();
    await expect(history(page)).toBeVisible();
    await expect(failed).toHaveCount(0);

    // Edit puts it back in the composer.
    await send(page, 'second attempt');
    await failed.getByRole('button', { name: 'Edit' }).click();
    await expect(failed).toHaveCount(0);
    await expect(composer(page)).toHaveValue('second attempt');
    await expect(composer(page)).toBeFocused();

    // Retry sends it once the hub is reachable.
    await composer(page).press('Enter');
    await expect(failed).toBeVisible();
    await page.unroute(/\/v1\/rooms\/[^/]+\/messages$/);
    await failed.getByRole('button', { name: 'Retry' }).click();
    await expect(failed).toHaveCount(0);
    await expect(message(page, 'second attempt')).toHaveCount(1);
  });

  test("the hub's reason is shown when it refuses a message", async ({ app: page }) => {
    await openRoom(page, 'Security');
    await onPost(page, (route) => route.fulfill({ status: 422, json: { code: 'invalid', message: 'That message is too long.' } }));
    await send(page, 'refused');
    const failed = page.getByRole('article', { name: 'Your message, not sent' });
    await expect(failed.getByRole('alert')).toContainText('Not sent. That message is too long.');
    await expect(failed.getByRole('alert')).not.toContainText('saved on this device');
  });

  test('a message sent twice shows the original', async ({ app: page }) => {
    await openRoom(page, 'Security');
    await onPost(page, async (route) => {
      const res = await route.fetch();
      await route.fulfill({ response: res, json: { ...(await res.json()), duplicate: true } });
    });
    await send(page, 'sent once');
    await expect(page.getByText('That message was already sent; showing the original.').first()).toBeVisible();
    await expect(message(page, 'sent once')).toHaveCount(1);
  });

  test('a room that fails to load says so and can try again', async ({ app: page, api }) => {
    await api.post('Security', 'already here');
    const roomId = api.room('Security').id;
    let fail = true;
    await page.route(`**/v1/rooms/${roomId}/messages?*`, (route) =>
      fail && route.request().method() === 'GET' ? route.fulfill({ status: 503, json: { message: 'The hub is busy.' } }) : route.fallback(),
    );
    await openRoom(page, 'Security');
    const alert = page.getByRole('main').getByRole('alert');
    await expect(alert).toContainText('The hub is busy.');
    fail = false;
    await alert.getByRole('button', { name: 'Try again' }).click();
    await expect(alert).toBeHidden();
    await expect(message(page, 'already here')).toBeVisible();
  });
});
