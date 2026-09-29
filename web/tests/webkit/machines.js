// Machines journeys for the system WebKit runner (scripts/e2e/webkit.swift).
// They run against the disposable hub from machines.setup.sh
// (scripts/e2e/machines_fixture.py): this machine (a connected, temporary
// session with finished, open and conversation workspaces), "Build server"
// (connected background service, new work paused, Claude Code needing sign-in), a
// laptop with a long name (offline, low disk, Claude Code signed in on an
// untested version without read-only support and its allowance paused,
// Codex needing sign-in) and "rack-01" (not responding, a container runner).
//
// Run with the others:  scripts/e2e/run-webkit.sh
// or alone:             scripts/e2e/run-webkit.sh /tmp/out web/tests/webkit/machines.js
window.__journeys = (() => {
  const LAPTOP = "Brayden's travel MacBook Pro (16-inch, 2019) kept in the office drawer";

  const rows = (t) => t.qa('li.row');
  const row = (t, name) => rows(t).find((r) => r.querySelector('h2')?.textContent === name);
  const thisMachine = (t) => rows(t).find((r) => r.textContent.includes('Temporary session'));
  const detailsOf = (r) => r.querySelector('button[aria-label^="Details for"]');
  const panel = (t) => t.q('aside.panel');
  const px = (el, prop) => parseFloat(getComputedStyle(el)[prop]);

  const ready = async (t) => {
    await t.waitFor(() => rows(t).length >= 4, 'four machines');
    await t.sleep(200);
  };
  const theme = async (t, name) => {
    document.documentElement.dataset.theme = name;
    await t.sleep(80);
  };
  // Nothing scrolls sideways and no control is cut off at the edges.
  const fits = (t, where, root = document) => {
    t.expect(t.overflowX() <= 0, `${where} scrolls sideways by ${t.overflowX()}px at ${innerWidth}px`);
    const clipped = t
      .qa('button, a.astryx-button, input, select, [role=tab]', root)
      .filter((el) => el.offsetParent !== null && !el.closest('pre, table'))
      .find((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && (r.right > innerWidth + 1 || r.left < -1);
      });
    t.expect(!clipped, `${where}: "${(clipped?.textContent || clipped?.getAttribute('aria-label') || '').trim()}" is cut off at ${innerWidth}px`);
    // Text doesn't spill out of its box either (long names, branches, versions).
    const spill = t
      .qa('li.row .cell, #machinepanel dd, #machinepanel .ws-main, #machinepanel .prov-main', root)
      .find((el) => el.scrollWidth > el.clientWidth + 1 && getComputedStyle(el).overflowX !== 'hidden');
    t.expect(!spill, `${where}: text overflows ${spill?.className} ("${spill?.textContent.trim().slice(0, 40)}")`);
  };
  const openDetails = async (t, name) => {
    const r = await t.waitFor(() => (typeof name === 'function' ? name(t) : row(t, name)), `the ${name} row`);
    const b = detailsOf(r);
    b.focus();
    await t.click(b);
    await t.waitFor(() => t.q('#machinepanel'), 'the details');
    return b;
  };
  const tabTo = async (t, label) => {
    const tab = await t.waitFor(() => t.qa('[role=tab]').find((x) => x.textContent.trim().startsWith(label)), `the ${label} tab`);
    await t.click(tab);
    await t.waitFor(() => tab.getAttribute('aria-selected') === 'true', `${label} selected`);
    await t.sleep(80);
  };
  const closeDetails = async (t) => {
    await t.press('Escape');
    await t.waitFor(() => !panel(t), 'the details to close');
    await t.sleep(80);
  };

  // At every width and theme: the list and each details section fit, with
  // the panel in the right mode for the width.
  const sweep = (width, height, dark) => ({
    name: `layout ${width}px ${dark ? 'dark' : 'light'}: list and every details section fit`,
    width,
    height,
    path: '/machines',
    run: async (t) => {
      await ready(t);
      await theme(t, dark ? 'dark' : 'light');
      fits(t, 'the machine list');
      // Long machine names wrap inside their column.
      const name = row(t, LAPTOP).querySelector('h2');
      t.expect(name.getBoundingClientRect().height > 30, 'the long machine name should wrap onto more lines');
      const b = await openDetails(t, LAPTOP);
      const p = panel(t);
      const mode = width >= 1200 ? 'complementary' : 'dialog';
      t.expect(p.getAttribute('role') === mode, `at ${width}px the details should be ${mode}, is ${p.getAttribute('role')}`);
      if (width <= 768) t.expect(p.querySelector('button[aria-label="Back"]'), 'on a phone the details have a Back button');
      const title = p.querySelector('h2');
      t.expect(title.textContent.trim() === LAPTOP, 'the title is the full machine name');
      if (title.scrollWidth > title.offsetWidth) {
        await t.waitFor(() => title.getAttribute('title') === LAPTOP, 'the truncated title offers the full name on hover');
      }
      const tabs = t.q('[role=tablist]');
      t.expect(tabs.scrollWidth <= tabs.clientWidth + 1, `the four sections should fit their bar (${tabs.scrollWidth} > ${tabs.clientWidth})`);
      for (const label of ['Overview', 'Connections', 'Storage', 'Diagnostics']) {
        await tabTo(t, label);
        if (label === 'Connections') for (const h of t.qa('.prov-head')) await t.click(h);
        fits(t, `${label} at ${width}px`);
      }
      await closeDetails(t);
      t.expect(document.activeElement === b, `focus returns to Details at ${width}px`);
      await theme(t, 'light');
    },
  });

  return [
    {
      name: 'several machines compare at a glance (1440)',
      width: 1440,
      height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        // Title, purpose and one primary action.
        const h1 = t.q('h1[data-screen-title]');
        t.expect(h1.textContent === 'Machines' && px(h1, 'fontSize') === 24, `title 24px, is ${px(h1, 'fontSize')}`);
        t.expect(t.qa('[role=main] .astryx-button[data-variant=primary]').length === 1, 'one primary action');
        t.expect(px(t.q('.machines-screen').firstElementChild, 'paddingLeft') === 32, 'desktop content padding is 32px');
        // Connection, work and providers are separate facts.
        const me = thisMachine(t);
        t.expect(/Connected/.test(me.textContent) && /Idle/.test(me.textContent), 'this machine: connected and idle');
        const build = row(t, 'Build server');
        t.expect(build.textContent.includes('New work paused'), 'Build server: new work paused');
        t.expect(/Claude Code\s*Needs sign-in/.test(build.textContent), 'Build server: connected but Claude Code needs sign-in');
        const laptop = row(t, LAPTOP);
        for (const s of ['Offline', 'Last heard', 'Allowance paused until', 'No read-only reviews', 'Untested version', 'Low disk space'])
          t.expect(laptop.textContent.includes(s), `the laptop row should say "${s}"`);
        t.expect(!laptop.textContent.includes('asleep'), 'offline is not called asleep');
        const rack = row(t, 'rack-01');
        t.expect(rack.textContent.includes('Not responding') && rack.textContent.includes('Last heard 2'), 'rack-01: not responding, with when');
        t.expect(rack.textContent.includes('needs the native profile'), "rack-01: can't take native work");
        // No adapter documentation or diagnostics in the list.
        t.expect(!t.text().includes('Token usage is reported') && !t.text().includes('Fingerprint'), 'no diagnostics in the list');
        // Aligned columns, readable sizes, even row padding.
        const lefts = rows(t).map((r) => Math.round(r.querySelector('.cell.conn').getBoundingClientRect().left));
        t.expect(new Set(lefts).size === 1, `connection column aligned: ${lefts}`);
        const rights = rows(t).map((r) => Math.round(detailsOf(r).getBoundingClientRect().right));
        t.expect(new Set(rights).size === 1, `Details aligned: ${rights}`);
        t.expect(px(laptop.querySelector('h2'), 'fontSize') === 17, 'machine names are 17px (the large type step)');
        t.expect(px(laptop.querySelector('.state'), 'fontSize') === 14, 'states are 14px');
        t.expect(px(laptop.querySelector('.meta'), 'fontSize') === 12, 'metadata is 12px');
        const pad = px(laptop, 'paddingTop');
        t.expect(pad >= 12 && pad <= 16, `row padding 12–16px, is ${pad}`);
        fits(t, 'the list at 1440');
      },
    },
    sweep(1440, 900, false),
    sweep(1440, 900, true),
    sweep(1280, 800, false),
    sweep(1280, 800, true),
    sweep(900, 800, false),
    sweep(900, 800, true),
    sweep(390, 844, false),
    sweep(390, 844, true),
    {
      name: 'keyboard: Tab to a machine, Enter opens its details, arrows switch sections, Escape returns',
      width: 1440,
      height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        t.q('[role=main] .astryx-button[data-variant=primary]').focus();
        let reached = null;
        for (let i = 0; i < 12 && !reached; i++) {
          await t.press('Tab');
          if (document.activeElement?.getAttribute('aria-label')?.startsWith('Details for')) reached = document.activeElement;
        }
        t.expect(reached, 'Tab should reach a Details button, got ' + document.activeElement?.outerHTML.slice(0, 80));
        // The synthetic key events here don't always count as keyboard use for
        // :focus-visible, so find the :focus-visible rules that match the
        // button and check that they draw Astryx's 2px ring.
        const rules = [];
        const walk = (list) => {
          for (const r of list) {
            if (r.cssRules) walk(r.cssRules);
            if (!r.selectorText?.includes(':focus-visible')) continue;
            try {
              if (reached.matches(r.selectorText.replaceAll(':focus-visible', ''))) rules.push(r.style);
            } catch {
              /* a selector this engine can't match without the pseudo-class */
            }
          }
        };
        for (const sheet of document.styleSheets) walk(sheet.cssRules);
        const resolve = (v) => v.replace(/var\((--[\w-]+)\)/g, (_, name) => getComputedStyle(reached).getPropertyValue(name).trim());
        const drawn = rules.map((r) => resolve(`${r.getPropertyValue('outline-width') || r.getPropertyValue('outline')} ${r.getPropertyValue('outline-style')}`)).join(' ');
        t.expect(/\b2px\b/.test(drawn) && /\bsolid\b/.test(drawn), `keyboard focus shows a 2px ring, got "${drawn}"`);
        await t.press('Enter');
        await t.waitFor(() => t.q('#machinepanel'), 'details by keyboard');
        await t.waitFor(() => panel(t).contains(document.activeElement), 'focus moves into the details');
        const first = t.q('[role=tab][aria-selected=true]');
        first.focus();
        await t.press('ArrowRight');
        await t.waitFor(() => document.activeElement?.id === 'machinetab-connections', 'ArrowRight to Connections');
        await t.press('End');
        await t.waitFor(() => document.activeElement?.id === 'machinetab-diagnostics', 'End to Diagnostics');
        await t.press('Home');
        await t.waitFor(() => document.activeElement?.id === 'machinetab-overview', 'Home to Overview');
        await closeDetails(t);
        t.expect(document.activeElement === reached, 'Escape returns focus to the Details button');
      },
    },
    {
      name: 'overlaid details (900px) trap focus and return it',
      width: 900,
      height: 800,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        const b = await openDetails(t, 'Build server');
        const p = panel(t);
        for (let i = 0; i < 20; i++) {
          await t.press('Tab');
          t.expect(p.contains(document.activeElement), `Tab ${i + 1} left the details for ${document.activeElement?.outerHTML.slice(0, 60)}`);
        }
        await closeDetails(t);
        t.expect(document.activeElement === b, 'focus returns to Details for Build server');
      },
    },
    {
      name: 'pause and resume new work with readable confirmations (real hub)',
      width: 1280,
      height: 800,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        await openDetails(t, thisMachine);
        const pause = await t.waitFor(() => t.byText('#machinepanel button', 'Pause new work'), 'Pause new work');
        pause.focus();
        await t.click(pause);
        const dlg = await t.waitFor(() => t.q('dialog[open]'), 'the confirmation');
        t.expect(/^Pause new work on .+\?$/.test(dlg.querySelector('h2').textContent), 'the confirmation names the action in plain words');
        t.expect(dlg.textContent.includes("finishes the work it's running now") && dlg.textContent.includes('Nothing is stopped.'), 'it explains that current work finishes');
        const r = dlg.getBoundingClientRect();
        const description = document.getElementById(dlg.getAttribute('aria-describedby'));
        t.expect(r.left >= 0 && r.right <= innerWidth && px(description, 'fontSize') >= 14, 'the confirmation is fully visible and readable');
        t.expect(document.activeElement?.textContent.trim() === 'Cancel', 'Cancel is focused first');
        await t.click(t.byText('dialog button', 'Pause new work'));
        await t.waitFor(() => !t.q('dialog[open]'), 'the confirmation to close');
        await t.waitFor(() => pause.textContent.trim() === 'Resume new work', 'the counterpart');
        await t.waitFor(() => document.activeElement === pause, 'focus returns to the button');
        await t.waitFor(() => thisMachine(t).textContent.includes('New work paused'), 'the row says new work is paused');
        await t.click(pause);
        await t.waitFor(() => t.q('dialog[open]')?.textContent.includes('starts taking new work again'), 'the resume confirmation');
        await t.click(t.byText('dialog button', 'Resume new work'));
        await t.waitFor(() => pause.textContent.trim() === 'Pause new work', 'taking work again');
        await t.waitFor(() => document.activeElement === pause, 'focus returns again');
        // Stopping work is a separate action, disabled with nothing running.
        const stop = t.byText('#machinepanel button', 'Stop current work');
        t.expect(stop && stop.disabled, 'Stop current work is separate and disabled while idle');
      },
    },
    {
      name: 'storage: accurate kinds, protected work, and a confirmed delete (real hub)',
      width: 1440,
      height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        await openDetails(t, thisMachine);
        await tabTo(t, 'Storage');
        const items = () => t.qa('.wss > li');
        await t.waitFor(() => items().length >= 4, 'workspaces');
        const kinds = items().map((li) => li.querySelector('.kind').textContent);
        t.expect(kinds.includes('Working checkout') && kinds.includes('Conversation scratch space'), `kinds: ${kinds}`);
        t.expect(!items().some((li) => li.querySelector('.kind').textContent === 'Review snapshot' && li.textContent.includes('@')), 'a reply’s scratch space is not labelled a review');
        for (const li of items()) {
          const title = li.querySelector('.ws-title');
          t.expect(getComputedStyle(title).textAlign === 'left' || getComputedStyle(title).textAlign === 'start', 'titles are left-aligned');
          t.expect(!/\b0 B\b/.test(li.textContent), `no measured zero: ${li.textContent}`);
        }
        const open = items().find((li) => li.textContent.includes("Document Beacon's request flow"));
        t.expect(open.textContent.includes('Protected: its work is still open') && !open.querySelector('button[aria-label^="Delete"]'), 'open work is protected');
        const actions = items().map((li) => Math.round(li.querySelector('.ws-action').getBoundingClientRect().right));
        t.expect(new Set(actions).size === 1, 'actions share one trailing column');
        const scratch = items().find((li) => li.textContent.includes('Conversation scratch space') && li.querySelector('button[aria-label^="Delete"]'));
        const wsName = scratch.querySelector('.where').textContent.split(' ')[0];
        const del = scratch.querySelector('button[aria-label^="Delete"]');
        del.focus();
        await t.click(del);
        const dlg = await t.waitFor(() => t.q('dialog[open]'), 'the delete confirmation');
        t.expect(dlg.textContent.includes('Delete this conversation scratch space?') && dlg.textContent.includes('the conversation itself stays on the hub'), 'the confirmation says what is lost');
        await t.click(t.byText('dialog button', 'Delete workspace'));
        await t.waitFor(() => !t.q('dialog[open]'), 'the delete to finish', 60000);
        await t.waitFor(() => !items().some((li) => li.textContent.includes(wsName)), 'the workspace gone from the list', 20000);
        await t.waitFor(() => document.activeElement?.id === 'machine-storage-heading', 'focus on the Workspaces heading');
      },
    },
    {
      name: 'sign-in and connection help sit where they are needed',
      width: 1440,
      height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        await openDetails(t, 'Build server');
        t.expect(t.text().includes('Background service (systemd)'), 'the Build server runs as a background service');
        await tabTo(t, 'Connections');
        const claude = t.qa('#machinepanel .prov').find((li) => li.querySelector('strong')?.textContent === 'Claude Code');
        const help = claude?.querySelector('.signin')?.textContent.replace(/\s+/g, ' ').trim();
        t.expect(help === 'Run claude auth login on this machine, then choose Check sign-in again.', `the sign-in command is beside Claude Code, got "${help}"`);
        t.expect(t.byText('#machinepanel button', 'Check sign-in again'), 'a connected machine can be re-checked');
        await closeDetails(t);
        await openDetails(t, thisMachine);
        t.expect(t.text().includes('Temporary session') && t.text().includes('yip service install runner'), 'a temporary session says how to keep it running');
        await closeDetails(t);
        await openDetails(t, 'rack-01');
        t.expect(t.text().includes("It hasn't reported since") && t.text().includes('"not confirmed"'), 'not responding explains the effect on work');
        await tabTo(t, 'Diagnostics');
        t.expect(t.byText('#machinepanel button', 'Copy'), 'the fingerprint can be copied');
        t.expect(t.qa('#machinepanel .tools dt').map((d) => d.textContent.trim()).join(',') === 'docker,git,go', 'toolchains as a name/version list');
      },
    },
    {
      name: 'add machine dialog opens, fits a phone and returns focus',
      width: 390,
      height: 844,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        const add = t.q('[role=main] .astryx-button[data-variant=primary]');
        add.focus();
        await t.click(add);
        const dlg = await t.waitFor(() => t.q('dialog[open]'), 'the Add machine dialog');
        const r = dlg.getBoundingClientRect();
        t.expect(r.left >= 0 && r.right <= innerWidth + 1, 'the dialog fits a phone');
        await t.press('Escape');
        await t.waitFor(() => !t.q('dialog[open]'), 'the dialog to close');
        await t.sleep(80);
        t.expect(document.activeElement === add, 'focus returns to Add machine');
      },
    },
    {
      // Last: revoking can't be undone.
      name: 'revoke access is its own confirmed action (real hub)',
      width: 1440,
      height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        await openDetails(t, 'rack-01');
        const revoke = await t.waitFor(() => t.byText('#machinepanel button', 'Revoke access'), 'Revoke access');
        revoke.focus();
        await t.click(revoke);
        const dlg = await t.waitFor(() => t.q('dialog[open]'), 'the confirmation');
        t.expect(dlg.textContent.includes("Revoke rack-01's access?") && dlg.textContent.includes('without pairing anew'), 'the revoke confirmation explains the loss');
        await t.click(t.byText('dialog button', 'Revoke access'));
        await t.waitFor(() => row(t, 'rack-01')?.textContent.includes('Access revoked'), 'rack-01 revoked');
        await t.waitFor(() => panel(t)?.contains(document.activeElement), 'focus stays in the details');
        t.expect(rows(t)[rows(t).length - 1] === row(t, 'rack-01'), 'revoked machines sort last');
      },
    },
  ];
})();
