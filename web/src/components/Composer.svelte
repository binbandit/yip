<script lang="ts">
  // The composer. Mentions come only from the list (never from typed or pasted
  // text) and are sent as structured {kind, id}. Drafts are kept per room and
  // thread on this device. When a live job is selected, messages are added to
  // that job and the delivery receipt says exactly what happened.
  import { onMount, tick, untrack } from 'svelte';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { draftKey, loadDraft, saveDraft, clearDraft } from '../lib/state/drafts';
  import { filterCandidates, findMentionQuery, insertMention, pruneSelected, resolveMentions, type MentionCandidate, type SelectedMention } from '../lib/util/mentions';
  import { deliveryReceipt, jobStateLabel, waitingReasonLabel } from '../lib/util/labels';
  import { isLiveJob, jobRunState, pendingReplies, workingInRoom } from '../lib/state/data';
  import type { Mention } from '../lib/api/types.gen';
  import Icon from './Icon.svelte';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';

  interface Props {
    roomId: string;
    threadId?: string;
    placeholder: string;
    /** Compact variant for the thread panel. */
    compact?: boolean;
  }
  let { roomId, threadId, placeholder, compact = false }: Props = $props();

  const key = $derived(draftKey(roomId, threadId));
  const rkey = $derived(receiptKey(roomId, threadId));
  const room = $derived(app.data.rooms[roomId]);
  const uid = `c-${Math.random().toString(36).slice(2, 8)}`;

  let body = $state('');
  let selected = $state<SelectedMention[]>([]);
  let projectIds = $state<string[]>([]);
  let textarea: HTMLTextAreaElement | undefined = $state();
  let query = $state<{ start: number; query: string } | null>(null);
  let activeIndex = $state(0);
  let projectsOpen = $state(false);
  let sendError = $state('');
  let note = $state('');
  let composing = false;
  let saveTimer: ReturnType<typeof setTimeout> | null = null;

  // ---- steering scope ----
  const scopeJobId = $derived(app.steer[rkey] ?? null);
  const scopeJob = $derived(scopeJobId ? app.data.jobs[scopeJobId] : undefined);
  $effect(() => {
    // A job that finished can't take more input: drop the scope and say so.
    if (scopeJob && !isLiveJob(scopeJob)) {
      untrack(() => {
        app.steer[rkey] = null;
        app.toast(`${scopeJob.title} is ${jobStateLabel(scopeJob).toLowerCase()}; new messages go to the room.`);
      });
    }
  });

  // ---- receipts ----
  const receipt = $derived.by(() => {
    const id = app.receipts[rkey];
    const input = id ? app.data.inputs[id] : undefined;
    if (!input) return null;
    const job = app.data.jobs[input.jobId];
    const name = job ? app.engineerName(job.ownerId) : 'the engineer';
    return { text: deliveryReceipt(input.delivery, name), delivery: input.delivery };
  });

  // Announce receipt changes once each (pending → immediate/queued).
  let announcedReceipt = '';
  $effect(() => {
    const t = receipt?.text ?? '';
    if (t && t !== announcedReceipt) {
      announcedReceipt = t;
      untrack(() => app.announce(t));
    }
  });

  // ---- live activity near the composer ----
  const activity = $derived.by(() => {
    const lines: { key: string; engineerId: string; text: string; tone: 'accent' | 'attention' }[] = [];
    const replies = pendingReplies(app.data, roomId, threadId ?? undefined);
    const working = new Set(workingInRoom(app.data, roomId));
    for (const j of replies) {
      const name = app.engineerName(j.ownerId);
      if (jobRunState(app.data, j) === 'unknown') {
        lines.push({ key: j.id, engineerId: j.ownerId, text: `${name}'s reply stopped reporting; its outcome isn't confirmed`, tone: 'attention' });
      } else if (j.state === 'waiting') {
        lines.push({
          key: j.id,
          engineerId: j.ownerId,
          text: `${name} will reply when possible — ${j.stateDetail || waitingReasonLabel(j.waitingReason)}`,
          tone: 'attention',
        });
      } else if (j.state === 'running' || working.has(j.ownerId)) {
        lines.push({ key: j.id, engineerId: j.ownerId, text: `${name} is replying…`, tone: 'accent' });
      } else {
        lines.push({ key: j.id, engineerId: j.ownerId, text: `${name} will pick this up next`, tone: 'accent' });
      }
      working.delete(j.ownerId);
    }
    if (!threadId) {
      for (const id of working) lines.push({ key: 'w' + id, engineerId: id, text: `${app.engineerName(id)} is working`, tone: 'accent' });
    }
    return lines.slice(0, 3);
  });

  // ---- mention candidates ----
  const candidates: MentionCandidate[] = $derived.by(() => {
    if (!room) return [];
    const inRoom = new Set(room.members.filter((m) => m.kind === 'engineer').map((m) => m.id));
    const out: MentionCandidate[] = [];
    for (const e of Object.values(app.data.engineers)) {
      const member = inRoom.has(e.id);
      if (!member && e.archived) continue;
      out.push({
        kind: 'engineer',
        id: e.id,
        handle: e.handle,
        name: e.name,
        role: e.role,
        unavailable: e.archived ? 'Archived' : member ? undefined : 'Not in this room — add them in room settings first',
      });
    }
    return out;
  });
  const options = $derived(query ? filterCandidates(candidates, query.query) : []);
  const open = $derived(!!query && options.length > 0);
  const mentioned = $derived(pruneSelected(body, selected));

  // ---- drafts ----
  onMount(() => {
    const d = loadDraft(key);
    if (d) {
      body = d.body;
      selected = d.mentions;
      projectIds = d.projectIds.filter((p) => room?.projectIds.includes(p));
      if (d.jobId && app.data.jobs[d.jobId] && isLiveJob(app.data.jobs[d.jobId]) && app.steer[rkey] === undefined) app.steer[rkey] = d.jobId;
    }
    void tick().then(autosize);
    return () => flushDraft();
  });

  function scheduleSave() {
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = setTimeout(flushDraft, 250);
  }
  function flushDraft() {
    if (saveTimer) clearTimeout(saveTimer);
    saveTimer = null;
    saveDraft(key, { body, mentions: pruneSelected(body, selected), projectIds, jobId: scopeJobId ?? undefined });
  }

  function autosize() {
    if (!textarea) return;
    textarea.style.height = 'auto';
    textarea.style.height = Math.min(textarea.scrollHeight, Math.round(window.innerHeight * 0.4)) + 'px';
  }

  function updateQuery() {
    if (!textarea) return;
    const q = findMentionQuery(body, textarea.selectionStart ?? body.length);
    if (q?.query !== query?.query || q?.start !== query?.start) activeIndex = 0;
    query = q;
  }

  function onInput() {
    autosize();
    updateQuery();
    scheduleSave();
    if (sendError) sendError = '';
  }

  async function choose(c: MentionCandidate) {
    if (c.unavailable || !query || !textarea) return;
    const r = insertMention(body, query.start, textarea.selectionStart ?? body.length, c.handle);
    body = r.text;
    if (!selected.some((s) => s.kind === c.kind && s.id === c.id)) selected = [...selected, { kind: c.kind, id: c.id, handle: c.handle }];
    query = null;
    await tick();
    textarea.focus();
    textarea.setSelectionRange(r.caret, r.caret);
    autosize();
    scheduleSave();
  }

  function onKeydown(e: KeyboardEvent) {
    if (composing || e.isComposing || e.keyCode === 229) return;
    if (open) {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        activeIndex = (activeIndex + 1) % options.length;
        return;
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        activeIndex = (activeIndex - 1 + options.length) % options.length;
        return;
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        const c = options[activeIndex];
        if (c && !c.unavailable) {
          e.preventDefault();
          void choose(c);
          return;
        }
        if (c?.unavailable && e.key === 'Enter') {
          e.preventDefault();
          return;
        }
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        query = null;
        return;
      }
    }
    if (e.key === 'Escape' && scopeJobId && !body) {
      e.preventDefault();
      e.stopPropagation();
      clearScope();
      return;
    }
    if (e.key === 'Enter') {
      const modEnter = app.data.preferences.sendKey === 'mod-enter';
      const wantsSend = modEnter ? e.metaKey || e.ctrlKey : !e.shiftKey && !e.metaKey && !e.ctrlKey && !e.altKey;
      if (wantsSend) {
        e.preventDefault();
        void send();
      }
    }
  }

  async function send() {
    const text = body.trim();
    if (!text) return;
    const mentions: Mention[] = resolveMentions(text, selected);
    const jobId = scopeJobId ?? undefined;
    const pids = projectIds.filter((p) => room?.projectIds.includes(p));
    body = '';
    selected = [];
    query = null;
    sendError = '';
    clearDraft(key);
    if (jobId) saveDraft(key, { body: '', mentions: [], projectIds: pids, jobId });
    if (!jobId) delete app.receipts[rkey];
    await tick();
    autosize();
    textarea?.focus();
    const resp = await app.send({ roomId, threadId, body: text, mentions, projectIds: pids, jobId });
    if (resp?.duplicate) app.toast('That message was already sent; showing the original.');
    const answered = (resp?.resolvedQuestionIds ?? []).map((id) => app.data.questions[id]).filter(Boolean);
    if (resp?.resolvedQuestionIds?.length) {
      const who = answered.length ? app.engineerName(answered[0].askerId) : 'The engineer';
      note = `${who}'s question is answered; the step that was waiting on it resumes.`;
      app.announce(note);
    } else {
      note = '';
    }
  }

  function clearScope() {
    app.steer[rkey] = null;
    delete app.receipts[rkey];
    flushDraft();
    textarea?.focus();
  }

  function toggleProject(id: string) {
    projectIds = projectIds.includes(id) ? projectIds.filter((p) => p !== id) : [...projectIds, id];
    scheduleSave();
  }

  /** Put text back into the composer (e.g. editing an unsent message). */
  export function fill(text: string, mentions: SelectedMention[] = []) {
    body = text;
    selected = mentions;
    void tick().then(() => {
      autosize();
      textarea?.focus();
    });
    scheduleSave();
  }

  export function focus() {
    textarea?.focus();
  }

  const offline = $derived(!app.online || app.connection === 'offline');
  const roomProjects = $derived((room?.projectIds ?? []).map((id) => app.data.projects[id]).filter(Boolean));
  const sendLabel = $derived(app.data.preferences.sendKey === 'mod-enter' ? 'Send (⌘/Ctrl+Enter)' : 'Send (Enter)');
</script>

<div class="composer" class:compact>
  {#if activity.length}
    <ul class="activity" aria-label="Engineer activity">
      {#each activity as a (a.key)}
        <li>
          <Avatar actor={{ kind: 'engineer', id: a.engineerId }} size={18} />
          {#if a.tone === 'attention'}<StateIcon shape="pause" tone="attention" size={12} />{:else}<StateIcon shape="bar" tone="accent" size={12} live />{/if}
          <span class="truncate">{a.text}</span>
        </li>
      {/each}
    </ul>
  {/if}

  {#if offline}
    <div class="notice attention offline" role="status">
      <Icon name="wifiOff" size={16} />
      <span>Can't reach your workspace. Your draft is saved on this device.</span>
    </div>
  {/if}

  <div class="box" class:scoped={!!scopeJob}>
    {#if scopeJob}
      <div class="scope" role="status">
        <Icon name="commit" size={15} />
        <span class="truncate">Adding to: <strong>{scopeJob.title}</strong> · {app.engineerName(scopeJob.ownerId)}</span>
        <button class="icon-btn clear" aria-label="Stop adding to {scopeJob.title}" onclick={clearScope}><Icon name="x" size={15} /></button>
      </div>
    {/if}

    <div class="field-wrap">
      {#if open}
        <div class="listbox" role="listbox" id="{uid}-list" aria-label="People you can mention">
          {#each options as c, i (c.id)}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div
              id="{uid}-opt-{i}"
              class="option"
              class:active={i === activeIndex}
              class:unavailable={!!c.unavailable}
              role="option"
              aria-selected={i === activeIndex}
              aria-disabled={!!c.unavailable}
              tabindex="-1"
              onmousedown={(e) => e.preventDefault()}
              onclick={() => choose(c)}
            >
              <Avatar actor={{ kind: c.kind, id: c.id }} size={24} />
              <span class="opt-name">{c.name}</span>
              <span class="opt-role truncate">{c.unavailable ?? c.role}</span>
              <span class="opt-handle mono">@{c.handle}</span>
            </div>
          {/each}
        </div>
      {/if}
      <label class="vh" for="{uid}-input">{placeholder}</label>
      <textarea
        bind:this={textarea}
        bind:value={body}
        id="{uid}-input"
        class="input-area"
        rows="1"
        {placeholder}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? `${uid}-list` : undefined}
        aria-activedescendant={open ? `${uid}-opt-${activeIndex}` : undefined}
        aria-describedby="{uid}-hint"
        oninput={onInput}
        onkeydown={onKeydown}
        onclick={updateQuery}
        onkeyup={(e) => (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Home' || e.key === 'End') && updateQuery()}
        oncompositionstart={() => (composing = true)}
        oncompositionend={() => (composing = false)}
        onblur={() => setTimeout(() => (query = null), 150)}
      ></textarea>
    </div>

    <div class="toolbar">
      <button
        class="icon-btn"
        aria-label="Mention someone"
        onclick={() => {
          const at = textarea?.selectionStart ?? body.length;
          const pre = body.slice(0, at);
          const needsSpace = pre.length > 0 && !/\s$/.test(pre);
          body = pre + (needsSpace ? ' @' : '@') + body.slice(at);
          void tick().then(() => {
            const c = at + (needsSpace ? 2 : 1);
            textarea?.focus();
            textarea?.setSelectionRange(c, c);
            updateQuery();
          });
        }}><Icon name="at" size={17} /></button
      >
      {#if roomProjects.length}
        <div class="projects">
          <button class="chip ctx" aria-expanded={projectsOpen} aria-controls="{uid}-projects" onclick={() => (projectsOpen = !projectsOpen)}>
            <Icon name="folder" size={14} />
            {#if projectIds.length}
              Context: {projectIds.map((p) => app.data.projects[p]?.name).filter(Boolean).join(', ')}
            {:else}
              Add project context
            {/if}
          </button>
          {#if projectsOpen}
            <fieldset class="project-pop" id="{uid}-projects">
              <legend class="vh">Projects for this message</legend>
              {#each roomProjects as p (p.id)}
                <label class="check"><input type="checkbox" checked={projectIds.includes(p.id)} onchange={() => toggleProject(p.id)} />{p.name}</label>
              {/each}
              <button class="btn btn-sm" onclick={() => (projectsOpen = false)}>Done</button>
            </fieldset>
          {/if}
        </div>
      {/if}
      {#if mentioned.length}
        <span class="mentioning meta truncate">Asking {mentioned.map((m) => app.engineerName(m.id)).join(', ')}</span>
      {/if}
      <span class="spacer"></span>
      <button class="send" aria-label={sendLabel} title={sendLabel} disabled={!body.trim()} onclick={send}>
        <Icon name="send" size={18} />
      </button>
    </div>
  </div>

  <p class="hint" id="{uid}-hint">
    {#if receipt}
      <span class="receipt" class:final={receipt.delivery !== 'pending'} role="status">
        <StateIcon shape={receipt.delivery === 'immediate' ? 'check-filled' : receipt.delivery === 'queued' ? 'circle' : 'bar'} tone={receipt.delivery === 'immediate' ? 'success' : 'accent'} size={12} live={receipt.delivery === 'pending'} />
        {receipt.text}
      </span>
    {:else if sendError}
      <span class="tone-danger">{sendError}</span>
    {:else if note}
      <span class="receipt final"><StateIcon shape="check-filled" tone="success" size={12} />{note}</span>
    {:else}
      <span class="keys">{app.data.preferences.sendKey === 'mod-enter' ? '⌘/Ctrl+Enter to send · Enter for a new line' : 'Enter to send · Shift+Enter for a new line'} · @ to mention</span>
    {/if}
  </p>
</div>

<style>
  .composer {
    flex: none;
    padding: 0 16px calc(12px + env(safe-area-inset-bottom));
    background: linear-gradient(to bottom, transparent, var(--surface) 12px);
  }
  .compact {
    padding: 0 12px calc(10px + env(safe-area-inset-bottom));
  }
  .activity {
    list-style: none;
    margin: 0 0 6px;
    padding: 0 4px;
    display: grid;
    gap: 2px;
  }
  .activity li {
    display: flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    font-size: 13px;
    color: var(--ink-secondary);
  }
  .offline {
    margin-bottom: 8px;
  }
  .box {
    border: 1px solid var(--control-edge);
    border-radius: 14px;
    background: var(--surface);
    transition:
      border-color var(--t-fast) var(--ease),
      box-shadow var(--t-fast) var(--ease);
    max-width: calc(var(--measure) + 120px);
  }
  .box:focus-within {
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
  }
  .box.scoped {
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
  }
  .scope {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 12px;
    border-bottom: 1px solid var(--line);
    border-radius: 13px 13px 0 0;
    background: var(--accent-subtle);
    color: var(--ink);
    font-size: 13.5px;
  }
  .scope span {
    flex: 1;
  }
  .clear {
    width: 28px;
    height: 28px;
  }
  .field-wrap {
    position: relative;
  }
  .input-area {
    display: block;
    width: 100%;
    min-height: 44px;
    max-height: 40vh;
    padding: 11px 14px 4px;
    border: 0;
    background: transparent;
    resize: none;
    font-size: 15px;
    line-height: 1.5;
    color: var(--ink);
  }
  .input-area:focus-visible {
    outline: none;
  }
  .input-area::placeholder {
    color: var(--ink-secondary);
  }
  .toolbar {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    padding: 4px 6px 6px 8px;
    min-width: 0;
  }
  .spacer {
    flex: 1;
  }
  .mentioning {
    min-width: 0;
  }
  .ctx {
    min-height: 28px;
    max-width: min(320px, 60vw);
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .projects {
    position: relative;
  }
  .project-pop {
    position: absolute;
    bottom: calc(100% + 6px);
    left: 0;
    z-index: 20;
    display: grid;
    gap: 4px;
    min-width: 200px;
    margin: 0;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: var(--shadow-pop);
  }
  .send {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    flex: none;
    border: 0;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-ink);
    cursor: pointer;
    transition: opacity var(--t-fast) var(--ease);
  }
  .send:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .listbox {
    position: absolute;
    bottom: calc(100% + 8px);
    left: 0;
    right: 0;
    z-index: 30;
    max-width: 520px;
    max-height: 280px;
    overflow: auto;
    padding: 4px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: var(--shadow-pop);
  }
  .option {
    display: grid;
    grid-template-columns: 24px auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 10px;
    min-height: 40px;
    padding: 4px 10px;
    border-radius: 8px;
    cursor: pointer;
    font-size: 14px;
  }
  .option.active {
    background: var(--accent-subtle);
    box-shadow: inset 0 0 0 1px var(--accent);
  }
  .option.unavailable {
    cursor: not-allowed;
  }
  .option.unavailable .opt-name {
    color: var(--ink-secondary);
  }
  .opt-name {
    font-weight: 600;
  }
  .opt-role {
    color: var(--ink-secondary);
    font-size: 13px;
  }
  .opt-handle {
    color: var(--ink-secondary);
    font-size: 12px;
  }
  .hint {
    min-height: 20px;
    margin-top: 5px;
    padding: 0 4px;
    font-size: 12.5px;
    color: var(--ink-secondary);
  }
  .receipt {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--ink);
    font-weight: 560;
  }
  @media (max-width: 760px) {
    .composer {
      padding: 0 8px calc(8px + env(safe-area-inset-bottom));
    }
    .keys {
      display: none;
    }
    .input-area {
      font-size: 16px;
    }
    .send {
      width: 44px;
      height: 44px;
    }
    .opt-handle {
      display: none;
    }
    .option {
      grid-template-columns: 24px auto minmax(0, 1fr);
      min-height: 44px;
    }
  }
  @media (pointer: coarse) {
    .ctx {
      min-height: 40px;
    }
  }
</style>
