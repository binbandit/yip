// One conversation from assignment through clarification, a genuine question,
// requested changes, re-review, completion and recall in another permitted room.
window.__journeys = (() => {
  const open = async (t, name) => {
    await t.click(await t.waitFor(() => t.qa('nav a.row').find((a) => a.textContent.trim().startsWith(name)), name));
    await t.waitFor(() => t.q('#room-title')?.textContent.includes(name), name + ' open');
  };
  const send = async (t, text, mentioned = false) => {
    const box = await t.waitFor(() => t.q('.room-composer textarea'), 'composer');
    if (mentioned) {
      await t.type(box, '@mi');
      await t.waitFor(() => t.q('[role=listbox]'), 'mention list');
      await t.press('Enter');
    }
    await t.type(box, text);
    await t.press('Enter');
  };
  return [{
    name: 'team conversation carries its own change and re-review', width: 1440, height: 900,
    run: async (t) => {
      await open(t, 'Security');
      await send(t, 'fix Atlas expiry for the client rollout', true);
      const answering = await t.waitFor(() => t.byText('.room-composer .scope', "Answering Mira's question"), 'question', 45000);
      t.expect(t.qa('.strip .row').length === 1, 'one logical assignment');
      await t.click(t.byText('button', 'Not an answer', answering));
      await send(t, 'also keep the existing error codes', true);
      await t.waitFor(() => t.text().includes("Added to Mira's"), 'clarification receipt');
      await t.waitFor(() => t.byText('.room-composer .scope', "Answering Mira's question"), 'answer targeting');
      await send(t, 'The web client release. Keep the existing session contract.');
      await t.waitFor(() => t.text().includes("Answers Mira's question"), 'answer receipt');
      await t.waitFor(() => t.q('section.result'), 'completed result', 90000);
      t.expect(t.text().includes('One change before I can approve'), 'reviewer requests a change');
      t.expect(t.text().includes('Good catch. Refresh now uses the shared validator'), 'author fixes and requests re-review');
      t.expect(t.text().includes('That fixes it. Approved.'), 'reviewer approves the correction');
      t.expect(t.qa('section.result').length === 1, 'one completion announcement');
      t.expect(!t.q('.strip'), 'no duplicate live assignment after completion');
      t.expect(!t.q('.approval'), 'routine implementation needs no approval');
      await open(t, 'Engineering');
      await send(t, 'where did we land on Atlas expiry?', true);
      await t.waitFor(() => t.text().includes('final revision') && t.text().includes('approved by Oren'), 'recall of finished work', 30000);
      t.expect(t.text().includes('resolved'), 'recall retains the review decision');
      await t.goto('/overview');
      await t.waitFor(() => t.q('.catchup')?.textContent.includes('approved the updated result after re-review'), 'catch-up with current review evidence');
      const item = t.qa('.catchup li').find((row) => row.textContent.includes('completed Fix Atlas session expiry'));
      t.expect(!!item && !!t.byText('button', 'view review', item), 'catch-up links the actual review');
      const reviewLink = t.byText('button', 'view review', item);
      reviewLink.focus();
      await t.press('Enter');
      await t.waitFor(() => t.q('#panel-title')?.textContent.includes('Review') || t.q('.panel')?.textContent.includes('Approved'), 'review evidence opens from the keyboard');

    },
  }];
})();
