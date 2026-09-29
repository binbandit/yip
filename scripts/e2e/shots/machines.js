// Temporary screenshot set taken against the disposable Machines hub.
// Not part of the regular journeys; use a temporary output directory:
//   scripts/e2e/run-webkit.sh OUT scripts/e2e/shots/machines.js
// Each "journey" leaves the page in the state to capture; the runner saves
// it as OUT/<name>.png.
window.__journeys = (() => {
  const LAPTOP = "Brayden's travel MacBook Pro (16-inch, 2019) kept in the office drawer";
  const ready = (t) => t.waitFor(() => t.qa('li.row').length >= 4, 'four machines');
  const theme = async (t, name) => {
    document.documentElement.dataset.theme = name === 'dark' ? 'night' : 'day';
    // The offscreen view doesn't advance animations: show end states.
    if (!t.q('#shots-still')) document.head.insertAdjacentHTML('beforeend', '<style id="shots-still">*,*::before,*::after{animation:none!important;transition:none!important}</style>');
    await t.sleep(120);
  };
  const shots = [];
  for (const w of [390, 900, 1280, 1440]) {
    for (const dark of [false, true]) {
      const mode = dark ? 'dark' : 'light';
      shots.push({
        name: `machines-${w}-${mode}`,
        width: w, height: w === 390 ? 844 : 900,
        path: '/machines',
        run: async (t) => {
          await ready(t);
          await theme(t, mode);
        },
      });
      shots.push({
        name: `machines-details-laptop-${w}-${mode}`,
        width: w, height: w === 390 ? 844 : 900,
        path: '/machines',
        run: async (t) => {
          await ready(t);
          await theme(t, mode);
          const row = t.qa('li.row').find((r) => r.querySelector('h2')?.textContent === LAPTOP);
          await t.click(row.querySelector('button[aria-label^="Details for"]'));
          await t.waitFor(() => t.q('#machinepanel'), 'the details');
          await t.sleep(400);
        },
      });
    }
  }
  for (const tab of ['Connections', 'Storage', 'Diagnostics']) {
    shots.push({
      name: `machines-details-thismachine-${tab.toLowerCase()}-1440-light`,
      width: 1440, height: 900,
      path: '/machines',
      run: async (t) => {
        await ready(t);
        await theme(t, 'light');
        const row = t.qa('li.row').find((r) => r.textContent.includes('Temporary session'));
        await t.click(row.querySelector('button[aria-label^="Details for"]'));
        const tb = await t.waitFor(() => t.qa('[role=tab]').find((x) => x.textContent.trim().startsWith(tab)), tab);
        await t.click(tb);
        await t.sleep(400);
      },
    });
  }
  return shots;
})();
