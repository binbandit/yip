// Browser journeys for the system WebKit runner (scripts/e2e/webkit.swift).
// They mirror tests/e2e/journeys.spec.ts (Playwright) for machines without a
// downloadable browser, and add layout and zoom sweeps (A26, A27). Keys are
// real AppKit key presses; text is typed through input events.
//
// Run against a demo hub:  scripts/e2e/run-webkit.sh
window.__journeys = (() => {
  // The title carries a decorative "#" (aria-hidden) before the name.
  const roomTitle = (t) => t.q('#room-title')?.textContent.replace(/^\s*#/, '').trim();
  const composer = (t) => t.waitFor(() => t.q('.room-composer textarea'), 'the composer');
  const openRoom = async (t, name) => {
    const link = await t.waitFor(() => t.qa('nav a.row').find((a) => a.textContent.trim().startsWith(name)), `the ${name} link`);
    await t.click(link);
    await t.waitFor(() => roomTitle(t) === name, `${name} to open`);
  };
  const mention = async (t, query) => {
    const box = await composer(t);
    await t.type(box, '@' + query);
    await t.waitFor(() => t.q('[role=listbox]'), 'the mention list');
    await t.press('Enter');
  };
  const noScroll = (t, where) => {
    t.expect(t.overflowX() <= 0, `${where} scrolls sideways by ${t.overflowX()}px at ${innerWidth}px`);
    // Controls must not be cut off by the edge either (a clipped header
    // doesn't scroll the page but still hides the button).
    const clipped = t
      .qa('main button, main a.btn, main input, main select')
      .filter((el) => el.offsetParent !== null && !el.closest('.table-scroll, .grants, [data-scrolls-x], .code, pre, table'))
      .find((el) => { const r = el.getBoundingClientRect(); return r.width > 0 && (r.right > innerWidth + 1 || r.left < -1); });
    t.expect(!clipped, `${where}: "${(clipped?.textContent || clipped?.getAttribute('aria-label') || '').trim()}" is cut off at ${innerWidth}px`);
  };

  // Screens every layout must fit without sideways scrolling.
  const sweep = async (t) => {
    for (const path of ['/overview', '/engineers', '/projects', '/machines', '/settings']) {
      await t.goto(path);
      await t.waitFor(() => t.q('[data-screen-title]'), path);
      await t.sleep(300);
      noScroll(t, path);
    }
    await t.goto('/engineers');
    await t.click(await t.waitFor(() => t.q('a.card-link'), 'an engineer card'));
    await t.waitFor(() => t.q('#eng-notes'), 'the engineer profile');
    noScroll(t, 'an engineer profile');
    await t.goto('/projects');
    await t.click(await t.waitFor(() => t.q('a.row[href^="/projects/"]'), 'a project'));
    await t.waitFor(() => t.q('#p-repos'), 'the project page');
    noScroll(t, 'a project page');
  };

  return [
    {
      name: 'sign in lands on the Overview with the demo label',
      width: 1440, height: 900,
      run: async (t) => {
        await t.waitFor(() => t.q('[role=note]')?.textContent.includes('Demo workspace'), 'the demo label');
        await t.waitFor(() => t.byText('h2', 'Since you were here'), 'Since you were here');
      },
    },
    {
      name: 'keyboard: the skip link moves focus to the content',
      width: 1440, height: 900,
      run: async (t) => {
        document.activeElement?.blur();
        document.body.focus();
        await t.press('Tab');
        t.expect(document.activeElement?.classList.contains('skip-link'), 'first Tab should reach "Skip to content", got ' + document.activeElement?.outerHTML.slice(0, 60));
        await t.press('Enter');
        await t.sleep(100);
        t.expect(document.activeElement?.id === 'main' || t.q('#main')?.contains(document.activeElement), 'the skip link should move focus into the content');
      },
    },
    {
      name: 'post with a structured mention chosen by keyboard',
      width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, 'Security');
        await mention(t, 'mi');
        const box = await composer(t);
        t.expect(box.value === '@mira ', `the mention should complete to "@mira ", got "${box.value}"`);
        await t.type(box, 'can you fix Atlas accepting expired sessions?');
        await t.press('Enter');
        await t.waitFor(() => t.qa('.msg .mention').some((m) => m.textContent.includes('@Mira')), 'the sent mention');
        await t.waitFor(() => t.text().includes('On it.'), "Mira's acknowledgement", 30000);
        // The result card is a labelled <section> (implicitly a region).
        await t.waitFor(() => t.q('section.result[aria-label^="Result:"]'), 'the result with evidence', 90000);
      },
    },
    {
      // Pip's Beacon investigation waits on a question, so it stays open
      // long enough to steer (the Atlas fix finishes too quickly to race).
      name: 'steering open work shows the actual delivery receipt',
      width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, 'Reverse engineering');
        await mention(t, 'pi');
        const box = await composer(t);
        await t.type(box, 'how does Beacon retry requests?');
        await t.press('Enter');
        const add = await t.waitFor(() => t.byText('.strip button', 'Add to this'), 'Add to this', 30000);
        await t.click(add);
        await t.waitFor(() => t.text().includes('Adding to: '), 'the scope banner');
        await t.type(box, 'Keep the existing API response shape.');
        await t.press('Enter');
        await t.waitFor(() => /Delivering to Pip…|Pip received your update|Queued for Pip's next step/.test(t.text()), 'a receipt');
        await t.waitFor(() => /Pip received your update|Queued for Pip's next step/.test(t.text()), 'the confirmed receipt', 30000);
      },
    },
    {
      name: 'job drawer traps focus when overlaid and Escape returns focus',
      width: 1024, height: 768,
      run: async (t) => {
        await openRoom(t, 'Security');
        const opener = await t.waitFor(() => t.q('.strip button.open') || t.q('.result button.claim'), 'a way into the work', 30000);
        opener.focus();
        await t.press('Enter');
        const drawer = await t.waitFor(() => t.q('[role=dialog]'), 'the drawer');
        await t.waitFor(() => t.qa('[role=tab]', drawer).some((x) => x.textContent.includes('Evidence')), 'the Evidence tab');
        for (let i = 0; i < 25; i++) {
          await t.press('Tab');
          t.expect(drawer.contains(document.activeElement), `Tab ${i + 1} left the drawer for ${document.activeElement?.outerHTML.slice(0, 60)}`);
        }
        await t.press('Escape');
        await t.waitFor(() => !document.contains(drawer) || drawer.offsetParent === null, 'the drawer to close');
        await t.sleep(100);
        t.expect(document.activeElement === opener || document.activeElement?.textContent === opener.textContent, 'focus should return to what opened the drawer');
      },
    },
    {
      name: 'search opens with Cmd+K, closes with Escape, and opens the actual source',
      width: 1440, height: 900,
      run: async (t) => {
        await t.press('k', ['Meta']);
        let input = await t.waitFor(() => t.q('dialog.search input'), 'the search box');
        t.expect(document.activeElement === input, 'the search box should be focused');
        await t.press('Escape');
        await t.waitFor(() => !t.q('dialog.search'), 'Escape to close search');
        await t.press('k', ['Meta']);
        input = await t.waitFor(() => t.q('dialog.search input'), 'the search box again');
        await t.type(input, 'expiry');
        const option = await t.waitFor(() => t.qa('[role=option]').find((o) => o.textContent.includes('Fix Atlas session expiry')), 'the work result', 15000);
        await t.click(option);
        await t.waitFor(() => /\/rooms\/.+panel=job/.test(location.pathname + location.search), 'the work to open in its room');
        await t.waitFor(() => t.qa('[role=tab]').some((x) => x.textContent.includes('Evidence')), 'the Evidence tab');
      },
    },
    {
      name: '390px: no sideways scroll, rooms sheet, composer and 44px send',
      width: 390, height: 844,
      run: async (t) => {
        noScroll(t, 'the Overview');
        await t.click(t.q('button[aria-label="Rooms and navigation"]'));
        const sheet = await t.waitFor(() => t.q('[role=dialog][aria-label="Rooms and navigation"]'), 'the rooms sheet');
        await t.click(t.qa('a', sheet).find((a) => a.textContent.trim().startsWith('Reverse engineering')));
        await t.waitFor(() => roomTitle(t) === 'Reverse engineering', 'the room');
        const box = (await composer(t)).getBoundingClientRect();
        t.expect(box.left >= 0 && box.right <= 390, `the composer should fit: ${box.left}–${box.right}`);
        const send = t.q('button[aria-label^="Send"]').getBoundingClientRect();
        t.expect(send.width >= 44 && send.height >= 44, `Send should be at least 44×44, is ${send.width}×${send.height}`);
        noScroll(t, 'the room');
      },
    },
    { name: 'layout sweep at 390px', width: 390, height: 844, run: sweep },
    { name: 'layout sweep at 1024px', width: 1024, height: 768, run: sweep },
    { name: 'layout sweep at 1440px', width: 1440, height: 900, run: sweep },
    {
      name: '200% zoom reflows without sideways scroll (A26)',
      width: 1280, height: 900, zoom: 2,
      run: async (t) => {
        t.expect(innerWidth <= 660, `at 200% zoom the layout width should be ~640px, is ${innerWidth}`);
        await sweep(t);
        await openRoom(t, 'Security').catch(async () => {
          await t.click(t.q('button[aria-label="Rooms and navigation"]'));
          const sheet = await t.waitFor(() => t.q('[role=dialog][aria-label="Rooms and navigation"]'), 'the rooms sheet');
          await t.click(t.qa('a', sheet).find((a) => a.textContent.trim().startsWith('Security')));
        });
        await t.waitFor(() => t.q('#room-title'), 'a room');
        noScroll(t, 'a room at 200%');
      },
    },
  ];
})();
