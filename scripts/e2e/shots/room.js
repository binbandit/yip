// Comparable completed Security conversations in both themes at four widths.
window.__journeys = [390, 900, 1280, 1440].flatMap((width) => ['light', 'dark'].map((mode) => ({
  name: `room-security-${width}-${mode}`,
  width, height: width === 390 ? 844 : width === 1280 ? 800 : 900,
  run: async (t) => {
    if (width < 760) {
      await t.click(await t.waitFor(() => t.q('button[aria-label="Rooms and navigation"]'), 'navigation menu'));
    }
    const link = await t.waitFor(() => t.qa('.sidebar a.row').find((a) => a.textContent.trim().startsWith('Security')), 'Security link');
    await t.click(link);
    await t.waitFor(() => t.q('section.result'), 'completed room');
    document.documentElement.dataset.theme = mode === 'dark' ? 'night' : 'day';
    document.head.insertAdjacentHTML('beforeend', '<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>');
    t.q('section.result').scrollIntoView({ block: 'end' });
    await t.sleep(250);
    t.expect(document.documentElement.scrollWidth <= innerWidth + 1, 'no horizontal page overflow');
  },
})));
