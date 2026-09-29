// Machines with no machines at all, against a fresh hub from
// machines-empty.setup.sh (an owner, nothing paired).
window.__journeys = (() => {
  const empty = (width, height, dark) => ({
    name: `no machines yet (${width}px ${dark ? 'dark' : 'light'})`,
    width,
    height,
    path: '/machines',
    run: async (t) => {
      document.documentElement.dataset.theme = dark ? 'dark' : 'light';
      await t.waitFor(() => t.text().includes('No machines yet.'), 'the empty state');
      t.expect(t.qa('li.row').length === 0, 'no rows');
      const adds = t.qa('[role=main] button').filter((b) => b.textContent.includes('Add machine'));
      t.expect(adds.length === 1, `one way to add a machine, found ${adds.length}`);
      t.expect(t.text().includes('Engineers need a machine to run work on'), 'it says why a machine is needed');
      t.expect(t.overflowX() <= 0, `no sideways scroll at ${innerWidth}px`);
      adds[0].focus();
      await t.click(adds[0]);
      await t.waitFor(() => t.q('dialog[open] input'), 'the Add machine dialog');
      await t.press('Escape');
      await t.waitFor(() => !t.q('dialog[open]'), 'the dialog to close');
      await t.sleep(80);
      t.expect(document.activeElement === adds[0], 'focus returns to Add machine');
    },
  });
  return [empty(1440, 900, false), empty(1280, 800, true), empty(900, 800, false), empty(390, 844, true)];
})();
