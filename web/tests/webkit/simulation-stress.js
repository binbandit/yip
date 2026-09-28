// Narrow screens, long identifiers, and concurrent questions in a fresh demo.
window.__journeys = (() => {
  const PROJECT = 'InternationalPaymentsReconciliationAndSettlementInfrastructure';
  const ROOM = 'Independent Beacon investigations';
  const paste = async (el, value) => {
    el.focus();
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value);
    el.setSelectionRange?.(value.length, value.length);
    el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertFromPaste', data: value }));
    await new Promise((r) => setTimeout(r, 0));
  };
  const api = async (method, path, body) => {
    const boot = await (await fetch('/v1/bootstrap')).json();
    const res = await fetch(path, { method, headers: { 'Content-Type': 'application/json', 'X-Yip-Csrf': boot.csrfToken }, body: body ? JSON.stringify(body) : undefined });
    if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
    return res.json();
  };
  const ready = async () => {
    document.head.insertAdjacentHTML('beforeend', '<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>');
    const boot = await api('GET', '/v1/bootstrap');
    const project = boot.projects.find((p) => p.name === PROJECT) ?? await api('POST', '/v1/projects', {
      name: PROJECT, description: 'A realistic long internal service identifier', instructions: '', policy: {}, roomIds: [],
    });
    return { boot, project };
  };
  const fits = (t, root, what) => {
    const bounds = root.getBoundingClientRect();
    t.expect(root.scrollWidth <= root.clientWidth + 1, `${what} has horizontal overflow (${root.scrollWidth} > ${root.clientWidth})`);
    for (const el of t.qa('button, a.btn, input, select, h1, .name', root)) {
      if (!el.getClientRects().length) continue;
      const r = el.getBoundingClientRect();
      t.expect(r.left >= bounds.left - 1 && r.right <= bounds.right + 1, `${what} clips ${el.textContent.trim().slice(0, 45)}`);
      if (el.matches('h1, .name')) t.expect(el.scrollWidth <= el.clientWidth + 1, `${what} truncates its project name`);
    }
  };
  const questions = async (roomId) => {
    const page = await api('GET', `/v1/rooms/${roomId}/messages`);
    return Promise.all(page.messages.filter((m) => m.kind === 'question').map((m) => api('GET', `/v1/questions/${m.refs.find((r) => r.kind === 'question').id}`)));
  };
  return [
    ...[[320, 640], [390, 844], [844, 390], [1280, 900]].flatMap(([width, height]) => ['list', 'detail'].map((view) => ({
      name: `long enterprise project ${view} fits ${width} by ${height}`, width, height,
      run: async (t) => {
        const { project } = await ready();
        await t.goto(view === 'list' ? '/projects' : `/projects/${project.id}`);
        await t.waitFor(() => view === 'list' ? t.byText('.list .name', PROJECT) : t.byText('h1', PROJECT), 'long project name');
        fits(t, t.q('main'), `project ${view}`);
        t.expect(t.overflowX() <= 0, 'no horizontal page scroll');
      },
    }))),
    {
      name: 'two open questions require an explicit answer target', width: 1280, height: 900,
      run: async (t) => {
        const { boot } = await ready();
        const pip = boot.engineers.find((e) => e.name === 'Pip');
        const mira = boot.engineers.find((e) => e.name === 'Mira');
        const beacon = boot.projects.find((p) => p.name === 'Beacon');
        await api('PUT', `/v1/projects/${beacon.id}/grants/${mira.id}`, { access: 'write', actions: [] });
        const room = await api('POST', '/v1/rooms', { name: ROOM, purpose: 'Independent investigations requiring separate owner input', kind: 'room', private: true,
          replyMode: 'quiet', stewardId: '', engineerIds: [pip.id, mira.id], projectIds: [beacon.id] });
        for (const engineer of [pip, mira]) await api('POST', `/v1/rooms/${room.id}/messages`, {
          body: `@${engineer.handle} document how Beacon retries requests`, mentions: [{ kind: 'engineer', id: engineer.id }], clientKey: crypto.randomUUID(),
        });
        await t.goto(`/rooms/${room.id}`);
        await t.waitFor(() => t.qa('.room .question').length === 2, 'two independent questions', 90000);
        t.expect(!t.q('.room-composer .answering'), 'multiple questions must not guess which answer is intended');
        await paste(t.q('.room-composer textarea'), 'The two investigations have separate release scopes.');
        await t.click(t.q('.room-composer .send'));
        await t.waitFor(() => t.qa('.msg').some((m) => m.textContent.includes('separate release scopes')), 'ordinary conversation');
        t.expect((await questions(room.id)).filter((q) => q.status === 'open').length === 2, 'ordinary conversation resolves neither question');
        const first = t.qa('.room .msg').find((m) => m.querySelector('.question button'));
        const firstId = first.dataset.messageId;
        await t.click(first.querySelector('.question button'));
        const answer = await t.waitFor(() => t.q('.panel textarea'), 'question thread');
        await paste(answer, 'The worker is not linked. Document the gateway side and state what remains unverified.');
        await t.click(t.q('.panel .send'));
        await t.waitFor(() => t.q('.panel')?.textContent.includes('question is answered'), 'explicit answer receipt');
        const after = await questions(room.id);
        t.expect(after.find((q) => q.messageId === firstId).status === 'answered', 'the selected question is answered');
        t.expect(after.filter((q) => q.status === 'open').length === 1, 'the other question remains open');
        await t.press('Escape');
        await t.waitFor(() => t.q('.room-composer .answering'), 'the one remaining question is targeted');
      },
    },
    {
      name: 'editing a failed non-answer does not silently answer the remaining question', width: 390, height: 844,
      run: async (t) => {
        const { boot } = await ready();
        const room = boot.rooms.find((r) => r.name === ROOM);
        await t.goto(`/rooms/${room.id}`);
        const chip = await t.waitFor(() => t.q('.room-composer .answering'), 'remaining question');
        await t.click(t.byText('button', 'Not an answer', chip));
        const original = window.fetch;
        let failures = 0;
        window.fetch = async (input, init) => {
          if (init?.method === 'POST' && /\/v1\/rooms\/[^/]+\/messages$/.test(String(input))) {
            if (++failures === 2) window.fetch = original;
            throw new TypeError('Synthetic connection loss');
          }
          return original(input, init);
        };
        await paste(t.q('.room-composer textarea'), 'Scheduling note: I will be away tomorrow.');
        await t.click(t.q('.room-composer .send'));
        await t.waitFor(() => t.q('.pending.failed'), 'failed non-answer');
        await t.click(t.byText('.pending.failed button', 'Edit'));
        t.expect(!t.q('.room-composer .answering'), 'editing keeps the original non-answer intent');
        await paste(t.q('.room-composer textarea'), 'Scheduling note: I will be away until Friday.');
        await t.click(t.q('.room-composer .send'));
        await t.waitFor(() => t.qa('.msg').some((m) => m.textContent.includes('away until Friday')), 'edited note sent');
        t.expect((await questions(room.id)).filter((q) => q.status === 'open').length === 1, 'the note must leave the question open');
      },
    },
  ];
})();
