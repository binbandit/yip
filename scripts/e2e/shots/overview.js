// Overview must fit its actual available width, including beside evidence.
// The fixture and preference reset affect only this disposable test hub.
window.__journeys = [390, 900, 1280, 1440].flatMap((width) => ['light', 'dark'].flatMap((mode) => [false, true].map((evidence) => ({
  name: `overview-${evidence ? 'review' : 'summary'}-${width}-${mode}`,
  width, height: width === 390 ? 844 : 900,
  path: '/machines',
  run: async (t) => {
    const boot = await (await fetch('/v1/bootstrap')).json();
    const reset = await fetch('/v1/preferences', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-Yip-Csrf': boot.csrfToken },
      body: JSON.stringify({ preferences: { ...boot.preferences, lastSeenAt: new Date(Date.now() - 86400000).toISOString() } }) });
    t.expect(reset.ok, 'reset the disposable catch-up window');
    document.head.insertAdjacentHTML('beforeend', '<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>');
    await t.goto('/overview');
    await t.waitFor(() => t.q('.catchup')?.textContent.includes('completed Fix Atlas session expiry'), 'current work in catch-up');
    document.documentElement.dataset.theme = mode === 'dark' ? 'night' : 'day';
    t.expect(t.q('#gs-steps')?.hidden, 'setup starts compact when work exists');
    const fits = (root, label) => {
      const bounds = root.getBoundingClientRect();
      t.expect(root.scrollWidth <= root.clientWidth + 1, `${label} overflows its column`);
      for (const el of t.qa('button, a.btn, .c-title, .c-detail, .start .body', root)) {
        if (!el.getClientRects().length) continue;
        const r = el.getBoundingClientRect();
        t.expect(r.left >= bounds.left - 1 && r.right <= bounds.right + 1, `${label}: control or text clipped: ${el.textContent.trim().slice(0, 55)}`);
      }
    };
    if (evidence) {
      const link = t.byText('.catchup button', 'view review');
      t.expect(!!link, 'review evidence link');
      link.focus();
      await t.press('Enter');
      await t.waitFor(() => t.q('.panel')?.textContent.includes('Approved'), 'the review');
      t.expect(!t.q('.overview .convo'), 'conversation yields space to evidence');
      fits(t.q('.panel'), 'review');
      if (width >= 1200) {
        t.expect(t.q('.overview .main').getBoundingClientRect().width >= 420, 'summary keeps a readable column');
        fits(t.q('.overview .main'), 'summary beside evidence');
      }
      await t.press('Escape');
      await t.waitFor(() => !t.q('.panel'), 'Escape closes evidence');
      t.expect(width < 1180 || !!t.q('.overview .convo'), 'conversation returns after closing evidence');
    }
    const show = t.byText('.start button', 'Show steps');
    await t.click(show);
    await t.waitFor(() => !t.q('#gs-steps').hidden, 'expanded setup');
    fits(t.q('.overview .main'), 'expanded setup and summary');
    await t.click(t.byText('.start button', 'Show less'));
    if (evidence) {
      await t.click(t.byText('.catchup button', 'view review'));
      await t.waitFor(() => t.q('.panel')?.textContent.includes('Approved'), 'review capture');
    }
    t.expect(t.overflowX() <= 0, 'no horizontal page overflow');
    if (t.q('.overview .main')) t.q('.overview .main').scrollTop = 0;
    await t.sleep(120);
  },
}))));
