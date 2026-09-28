// Adversarial owner workflows on a fresh demo hub. The browser performs every
// interaction; API calls below create only the disposable scenario fixtures.
window.__journeys = (() => {
  const TEAM = 'Platform reliability and developer experience';
  const PRIVATE = 'Personal finance prototype';
  const LONG_PROJECT = 'InternationalPaymentsReconciliationAndSettlementInfrastructure';
  const LONG_PERSON = 'Alexandria Montgomery-Wellington Infrastructure';
  const paste = async (el, value) => {
    el.focus();
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value);
    el.setSelectionRange?.(value.length, value.length);
    el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertFromPaste', data: value }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  };
  const fixture = async (t) => {
    let boot = await (await fetch('/v1/bootstrap')).json();
    const req = async (path, body) => {
      const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Yip-Csrf': boot.csrfToken }, body: JSON.stringify(body) });
      t.expect(res.ok, `fixture ${path}: ${res.status}`);
      return res.json();
    };
    if (!boot.rooms.some((r) => r.name === TEAM)) {
      const engineers = [];
      for (let i = 0; i < 12; i++) engineers.push(await req('/v1/engineers', {
        name: i === 0 ? LONG_PERSON : `Platform engineer ${String(i).padStart(2, '0')}`,
        handle: `platform-${i}`, role: 'Site reliability and infrastructure engineer',
        description: 'Synthetic large-team browser scenario', instructions: 'Use only the scripted demo provider.',
        capabilityTags: ['review'], provider: { provider: 'fake' },
      }));
      const project = await req('/v1/projects', { name: LONG_PROJECT, description: 'Synthetic enterprise project', instructions: '', policy: {}, roomIds: [] });
      await req('/v1/rooms', { name: TEAM, purpose: 'Synthetic large-team coordination', kind: 'room', private: false,
        replyMode: 'quiet', stewardId: '', engineerIds: engineers.map((e) => e.id), projectIds: [project.id] });
      await req('/v1/rooms', { name: PRIVATE, purpose: 'Synthetic private project', kind: 'room', private: true,
        replyMode: 'quiet', stewardId: '', engineerIds: [], projectIds: [] });
      boot = await (await fetch('/v1/bootstrap')).json();
    }
    document.head.insertAdjacentHTML('beforeend', '<style>*,*::before,*::after{animation:none!important;transition:none!important}</style>');
    return boot;
  };
  const openRoom = async (t, name) => {
    const boot = await fixture(t);
    const room = boot.rooms.find((r) => r.name === name);
    await t.goto(`/rooms/${room.id}`);
    await t.waitFor(() => t.q('#room-title')?.textContent.includes(name), name);
    return room;
  };
  const box = (t) => t.q('.room-composer textarea');
  const failNextSend = () => {
    const original = window.fetch;
    let failures = 0;
    window.fetch = async (input, init) => {
      if (init?.method === 'POST' && /\/v1\/rooms\/[^/]+\/messages$/.test(String(input))) {
        if (++failures === 2) window.fetch = original;
        throw new TypeError('Synthetic connection loss before delivery');
      }
      return original(input, init);
    };
  };
  const fits = (t, el, what) => {
    const r = el.getBoundingClientRect();
    t.expect(r.left >= 0 && r.right <= innerWidth + 1, `${what} exceeds the viewport (${Math.round(r.left)}..${Math.round(r.right)})`);
    t.expect(el.scrollWidth <= el.clientWidth + 1, `${what} has clipped or overflowing content (${el.scrollWidth} > ${el.clientWidth})`);
  };
  return [
    {
      name: 'large team keyboard mentions keep the selected engineer visible', width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, TEAM);
        t.expect(t.q('.empty-room strong')?.textContent === "Tell the team what you're working on.", 'large-team empty state stays concise');
        await paste(box(t), '@platform-');
        await t.waitFor(() => t.qa('.listbox [role=option]').length === 8, 'eight mention choices');
        for (let i = 0; i < 7; i++) await t.press('ArrowDown');
        const selected = t.q('.listbox [aria-selected=true]');
        const list = t.q('.listbox').getBoundingClientRect();
        const choice = selected.getBoundingClientRect();
        t.expect(choice.top >= list.top && choice.bottom <= list.bottom, 'keyboard selection must scroll into view before choosing an engineer');
        const handle = selected.querySelector('.opt-handle').textContent;
        await t.press('Enter');
        t.expect(box(t).value === `${handle} `, 'Enter inserts the visible selected engineer');
      },
    },
    {
      name: 'phone mention menu fits a long engineer name', width: 390, height: 844,
      run: async (t) => {
        await openRoom(t, TEAM);
        await paste(box(t), '@platform-0');
        await t.waitFor(() => t.q('.listbox .option'), 'long-name mention');
        fits(t, t.q('.listbox'), 'mention menu');
        fits(t, t.q('.listbox .option'), 'long-name mention option');
      },
    },
    {
      name: 'phone project context fits a long enterprise project name', width: 390, height: 844,
      run: async (t) => {
        await openRoom(t, TEAM);
        await t.click(t.q('.room-composer button[aria-label="Add project context"]'));
        await t.waitFor(() => t.q('.project-pop'), 'project context picker');
        fits(t, t.q('.project-pop'), 'project context picker');
        await t.click(t.q('.project-pop input'));
        await t.click(t.byText('.project-pop button', 'Done'));
        fits(t, t.q('.room-composer .toolbar'), 'selected project toolbar');
      },
    },
    {
      name: 'private project drafts stay in their own room', width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, PRIVATE);
        await paste(box(t), 'Private draft: household balance notes');
        await openRoom(t, TEAM);
        t.expect(!box(t).value.includes('household'), 'private draft does not follow navigation');
        await paste(box(t), 'Shared draft: deployment checklist');
        await openRoom(t, PRIVATE);
        t.expect(box(t).value === 'Private draft: household balance notes', 'private draft restores');
        await openRoom(t, TEAM);
        t.expect(box(t).value === 'Shared draft: deployment checklist', 'team draft restores independently');
      },
    },
    {
      name: 'editing an unsent room message preserves its chosen project', width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, TEAM);
        await t.click(t.q('.room-composer .ctx'));
        const context = t.q('.project-pop input');
        if (!context.checked) await t.click(context);
        await t.click(t.byText('.project-pop button', 'Done'));
        await paste(box(t), 'Check the release gate after the outage');
        failNextSend();
        await t.click(t.q('.room-composer .send'));
        await t.waitFor(() => t.q('.pending.failed'), 'failed message');
        await openRoom(t, PRIVATE);
        await openRoom(t, TEAM);
        await t.click(t.byText('.pending.failed button', 'Edit'));
        t.expect(box(t).value === 'Check the release gate after the outage', 'failed text returns');
        t.expect(t.q('.room-composer .ctx.set')?.textContent.includes(LONG_PROJECT), 'editing preserves the explicitly selected project after navigation');
        await paste(box(t), '');
      },
    },
    {
      name: 'editing an unsent thread message preserves a structured mention', width: 1440, height: 900,
      run: async (t) => {
        const room = await openRoom(t, TEAM);
        await paste(box(t), 'Thread for the synthetic release review');
        await t.click(t.q('.room-composer .send'));
        const msg = await t.waitFor(() => t.qa('.msg').find((m) => m.textContent.includes('Thread for the synthetic release review')), 'thread root');
        await t.goto(`/rooms/${room.id}?panel=thread:${msg.dataset.messageId}`);
        const threadBox = await t.waitFor(() => t.q('.panel textarea'), 'thread composer');
        await paste(threadBox, '@platform-1');
        await t.waitFor(() => t.q('.panel .listbox .option'), 'thread mention choice');
        await t.press('Enter');
        await paste(threadBox, threadBox.value + 'review the release notes');
        failNextSend();
        await t.click(t.q('.panel .send'));
        await t.waitFor(() => t.q('.panel .pending.failed'), 'failed thread reply');
        await t.click(t.byText('.panel .pending.failed button', 'Edit'));
        t.expect(t.q('.panel .mentioning')?.textContent.includes('Platform engineer 01'), 'thread editing retains the addressed engineer');
        await paste(threadBox, '');
        await t.press('Escape');
      },
    },
    {
      name: 'a lost response still shows the message confirmed by live events', width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, PRIVATE);
        const original = window.fetch;
        let failures = 0;
        window.fetch = async (input, init) => {
          if (init?.method === 'POST' && /\/v1\/rooms\/[^/]+\/messages$/.test(String(input))) {
            if (++failures === 2) window.fetch = original;
            else await original(input, init);
            await t.waitFor(() => t.qa('.msg').some((m) => m.textContent.includes('Delivered before the response disappeared')), 'delivery event before response loss');
            throw new TypeError('Synthetic connection loss after delivery');
          }
          return original(input, init);
        };
        await paste(box(t), 'Delivered before the response disappeared');
        await t.click(t.q('.room-composer .send'));
        await t.waitFor(() => t.qa('.msg').some((m) => m.textContent.includes('Delivered before the response disappeared')), 'confirmed message');
        await t.waitFor(() => failures === 2, 'both response attempts lost');
        await t.sleep(100);
        t.expect(!Object.keys(localStorage).some((key) => key.startsWith('yip.unsent.') && localStorage.getItem(key).includes('Delivered before the response disappeared')), 'confirmed delivery must not be persisted as an unsent message');
        t.expect(!t.q('.pending.failed'), 'confirmed delivery is not shown as a failed send');
      },
    },
    {
      name: 'reloading after a lost response does not resurrect a failed duplicate', width: 1440, height: 900,
      run: async (t) => {
        await openRoom(t, PRIVATE);
        await t.waitFor(() => t.qa('.msg').some((m) => m.textContent.includes('Delivered before the response disappeared')), 'persisted canonical message');
        t.expect(!t.qa('.pending.failed').some((m) => m.textContent.includes('Delivered before the response disappeared')), 'reopening must not restore an already delivered message as Not sent');
        t.expect(t.qa('.msg').filter((m) => m.textContent.includes('Delivered before the response disappeared')).length === 1, 'one canonical message');
      },
    },
    {
      name: 'pasted names and unavailable engineers do not become mentions', width: 390, height: 844,
      run: async (t) => {
        const room = await openRoom(t, TEAM);
        await paste(box(t), 'Quoted example: @platform-1 checks this');
        await t.click(t.q('.room-composer .send'));
        const sent = await t.waitFor(() => t.qa('.msg').find((m) => m.textContent.includes('Quoted example: @platform-1 checks this')), 'unaddressed message');
        t.expect(!sent.querySelector('.mention'), 'pasted handles are ordinary text');
        const messages = await (await fetch(`/v1/rooms/${room.id}/messages`)).json();
        t.expect(messages.messages.find((m) => m.id === sent.dataset.messageId).mentions.length === 0, 'hub receives no fabricated mention');
        await paste(box(t), '@mira');
        await t.waitFor(() => t.q('.listbox [aria-disabled=true]'), 'engineer outside this room');
        await t.press('Enter');
        t.expect(box(t).value === '@mira' && !!t.q('.listbox'), 'Enter neither selects nor sends an unavailable engineer');
        await t.press('Escape');
        await paste(box(t), '');
      },
    },
  ];
})();
