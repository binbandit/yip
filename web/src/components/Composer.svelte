<script lang="ts">
  // The composer. Mentions come only from the list (never from typed or pasted
  // text) and are sent as structured {kind, id}. Drafts are kept per room and
  // thread on this device. When a live job is selected, messages are added to
  // that job and the delivery receipt says exactly what happened.
  import { onMount, tick, untrack } from 'svelte';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { draftKey, loadDraft, saveDraft, clearDraft } from '../lib/state/drafts';
  import { filterCandidates, findMentionQuery, insertMention, projectsNamedIn, pruneSelected, resolveMentions, type MentionCandidate, type SelectedMention } from '../lib/util/mentions';
  import { deliveryReceipt, jobStateLabel, waitingReasonLabel } from '../lib/util/labels';
  import { isLiveJob, jobRunState, pendingReplies, workingInRoom, type PendingMessage } from '../lib/state/data';
  import type { Mention } from '../lib/api/types.gen';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
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

  // ---- answering a question in the room ----
  // When exactly one question in this room's main timeline is waiting on
  // you, a message sent here answers it by default, as in any group chat.
  // The chip says so before you send, and one click says it isn't.
  let notAnswer = $state<string | null>(null);
  let restoredReplyTo = $state<string | null | undefined>(undefined);
  const waitingQuestion = $derived.by(() => {
    if (threadId || scopeJobId) return null;
    const me = app.me?.id;
    const open = Object.values(app.data.questions).filter(
      (q) => q.status === 'open' && q.source.roomId === roomId && !q.source.threadId && q.recipient.kind === 'user' && q.recipient.id === me,
    );
    return open.length === 1 ? open[0] : null;
  });
  const answering = $derived.by(() => {
    if (threadId || scopeJobId) return null;
    const q = restoredReplyTo === undefined ? waitingQuestion : Object.values(app.data.questions).find(
      (q) => q.status === 'open' && q.messageId === restoredReplyTo && q.source.roomId === roomId,
    );
    if (!q || notAnswer === q.id) return null;
    // Mentioning someone other than the asker means you're talking to them.
    const others = resolveMentions(body, selected).filter((m) => m.kind === 'engineer' && m.id !== q.askerId);
    return others.length ? null : q;
  });

  // ---- receipts ----
  const receipt = $derived.by(() => {
    const id = app.receipts[rkey];
    const input = id ? app.data.inputs[id] : undefined;
    if (!input) return null;
    const job = app.data.jobs[input.jobId];
    const name = job ? app.engineerName(job.ownerId) : 'the engineer';
    // A provider that only takes input between steps: offer the explicit
    // interrupt-and-restart (spec §8D) while the attempt is still running.
    const canRestart = input.delivery === 'queued' && job?.state === 'running' && !input.deliveredAt;
    return { text: deliveryReceipt(input.delivery, name), delivery: input.delivery, jobId: input.jobId, name, canRestart };
  });
  let restarting = $state(false);
  async function restartNow() {
    if (!receipt) return;
    const { jobId, name } = receipt;
    restarting = true;
    try {
      await api.restartJob(jobId);
      delete app.receipts[rkey];
      note = `Restarting ${name} with your update.`;
      app.announce(note);
    } catch (err) {
      sendError = errorMessage(err);
    } finally {
      restarting = false;
    }
  }

  // Announce receipt changes once each (pending → immediate/queued).
  let announcedReceipt = '';
  $effect(() => {
    const t = receipt?.text ?? '';
    if (t && t !== announcedReceipt) {
      announcedReceipt = t;
      untrack(() => app.announce(t));
    }
  });

  // ---- live activity, as a group chat shows it ----
  // One quiet line under the box: who is typing a reply, who is busy with
  // work in this room, and anything that is holding a reply up. Engineers'
  // intermediate output never streams into the conversation.
  function names(ids: string[]): string {
    const n = [...new Set(ids)].map((id) => app.engineerName(id));
    if (n.length <= 2) return n.join(' and ');
    return `${n[0]}, ${n[1]} and ${n.length - 2} more`;
  }
  const activity = $derived.by(() => {
    const replies = pendingReplies(app.data, roomId, threadId ?? undefined);
    const working = new Set(workingInRoom(app.data, roomId));
    const typing: string[] = [];
    const next: string[] = [];
    const held: { text: string; tone: 'attention' }[] = [];
    for (const j of replies) {
      const name = app.engineerName(j.ownerId);
      if (jobRunState(app.data, j) === 'unknown') {
        held.push({ text: `${name}'s reply stopped reporting; its outcome isn't confirmed`, tone: 'attention' });
      } else if (j.state === 'waiting') {
        held.push({ text: `${name} will reply when possible — ${j.stateDetail || waitingReasonLabel(j.waitingReason)}`, tone: 'attention' });
      } else if (j.state === 'running' || working.has(j.ownerId)) {
        typing.push(j.ownerId);
      } else {
        next.push(j.ownerId);
      }
      working.delete(j.ownerId);
    }
    const busy = threadId ? [] : [...working];
    const parts: { text: string; tone: 'accent' | 'attention'; dots?: boolean }[] = [];
    if (typing.length) parts.push({ text: `${names(typing)} ${new Set(typing).size > 1 ? 'are' : 'is'} typing`, tone: 'accent', dots: true });
    if (next.length) parts.push({ text: `${names(next)} will reply shortly`, tone: 'accent' });
    if (busy.length) {
      const job = busy.length === 1 ? Object.values(app.data.jobs).find((j) => j.ownerId === busy[0] && j.state === 'running' && j.source?.roomId === roomId && j.kind !== 'reply') : undefined;
      parts.push({ text: job ? `${names(busy)} is working on ${job.title}` : `${names(busy)} ${busy.length > 1 ? 'are' : 'is'} working`, tone: 'accent' });
    }
    for (const h of held) parts.push(h);
    return parts.slice(0, 2);
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
  $effect(() => {
    if (open) document.getElementById(`${uid}-opt-${activeIndex}`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  });

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

  // Naming a room project in the text adds it as a chip (spec §7: "Atlas
  // resolves to a project chip"). A chip you remove stays removed; a chip you
  // chose yourself isn't taken away when the name leaves the text.
  let autoProjects = new Set<string>();
  let dismissedProjects = new Set<string>();
  function detectProjects() {
    const named = projectsNamedIn(body, roomProjects);
    let next = projectIds.filter((p) => !autoProjects.has(p) || named.includes(p));
    for (const id of named) {
      if (!next.includes(id) && !dismissedProjects.has(id)) {
        next = [...next, id];
        autoProjects.add(id);
      }
    }
    for (const id of autoProjects) if (!next.includes(id)) autoProjects.delete(id);
    if (next.join() !== projectIds.join()) projectIds = next;
  }

  function onInput() {
    autosize();
    updateQuery();
    detectProjects();
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
    const replyToId = answering?.messageId || undefined;
    const pids = projectIds.filter((p) => room?.projectIds.includes(p));
    body = '';
    notAnswer = null;
    restoredReplyTo = undefined;
    selected = [];
    query = null;
    sendError = '';
    projectIds = projectIds.filter((p) => !autoProjects.has(p)); // chosen chips stay; detected ones were for this message
    autoProjects = new Set();
    dismissedProjects = new Set();
    clearDraft(key);
    if (jobId) saveDraft(key, { body: '', mentions: [], projectIds: pids, jobId });
    if (!jobId) delete app.receipts[rkey];
    await tick();
    autosize();
    textarea?.focus();
    const resp = await app.send({ roomId, threadId, body: text, mentions, projectIds: pids, jobId, replyToId });
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
    if (projectIds.includes(id)) dismissedProjects.add(id);
    else dismissedProjects.delete(id);
    autoProjects.delete(id);
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

  /** Editing changes the text while retaining who and what the failed send addressed. */
  export function restorePending(p: PendingMessage) {
    projectIds = p.projectIds.filter((id) => room?.projectIds.includes(id));
    autoProjects = new Set();
    dismissedProjects = new Set();
    restoredReplyTo = p.replyToId ?? null;
    notAnswer = null;
    app.steer[rkey] = p.jobId ?? null;
    fill(p.body, p.mentions
      .filter((m) => m.kind === 'engineer' && app.data.engineers[m.id])
      .map((m) => ({ kind: 'engineer', id: m.id, handle: app.data.engineers[m.id].handle })));
  }

  export function focus() {
    textarea?.focus();
  }

  const offline = $derived(!app.online || app.connection === 'offline');
  const roomProjects = $derived((room?.projectIds ?? []).map((id) => app.data.projects[id]).filter(Boolean));
  const sendLabel = $derived(app.data.preferences.sendKey === 'mod-enter' ? 'Send (⌘/Ctrl+Enter)' : 'Send (Enter)');
</script>

<div class="composer" class:compact>
  {#if offline}
    <div class="notice attention offline" role="status">
      <Icon name="wifiOff" size={16} />
      <span>Can't reach your workspace. Your draft is saved on this device.</span>
    </div>
  {/if}

  <div class="box" class:scoped={!!scopeJob || !!answering}>
    {#if answering}
      <div class="scope answering" role="status">
        <Icon name="reply" size={15} />
        <span class="truncate">Answering {app.engineerName(answering.askerId)}'s question{answering.missingFact ? `: ${answering.missingFact}` : ''}</span>
        <button class="btn btn-sm btn-quiet" onclick={() => (notAnswer = answering?.id ?? null)}>Not an answer</button>
      </div>
    {/if}
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
          <button
            class="chip ctx"
            class:set={projectIds.length > 0}
            aria-expanded={projectsOpen}
            aria-controls="{uid}-projects"
            aria-label={projectIds.length ? undefined : 'Add project context'}
            title={projectIds.length ? undefined : 'Add project context'}
            onclick={() => (projectsOpen = !projectsOpen)}
          >
            <Icon name="folder" size={15} />
            {#if projectIds.length}{projectIds.map((p) => app.data.projects[p]?.name).filter(Boolean).join(', ')}{/if}
          </button>
          {#if projectsOpen}
            <fieldset class="project-pop" id="{uid}-projects">
              <legend class="vh">Projects for this message</legend>
              {#each roomProjects as p (p.id)}
                <label class="check"><input type="checkbox" checked={projectIds.includes(p.id)} onchange={() => toggleProject(p.id)} /><span>{p.name}</span></label>
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
      {#if receipt.canRestart}
        <button class="btn btn-sm btn-quiet restart" disabled={restarting} onclick={restartNow}>Interrupt and restart now</button>
      {/if}
    {:else if sendError}
      <span class="tone-danger">{sendError}</span>
    {:else if note}
      <span class="receipt final"><StateIcon shape="check-filled" tone="success" size={12} />{note}</span>
    {:else if activity.length}
      <span class="activity truncate" aria-label="Engineer activity">
        {#each activity as a, i (a.text)}
          {#if i}<span class="sep" aria-hidden="true">·</span>{/if}
          <span class:tone-attention={a.tone === 'attention'}>{a.text}{#if a.dots}<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>{/if}</span>
        {/each}
      </span>
    {/if}
    <span class="vh">{app.data.preferences.sendKey === 'mod-enter' ? 'Command or Control and Enter sends; Enter adds a new line.' : 'Enter sends; Shift and Enter adds a new line.'} Type @ to mention an engineer.</span>
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
    display: block;
    min-width: 0;
  }
  .sep {
    margin: 0 6px;
    opacity: 0.6;
  }
  .dots {
    display: inline-flex;
    gap: 2px;
    margin-left: 3px;
    vertical-align: middle;
  }
  .dots i {
    width: 3px;
    height: 3px;
    border-radius: 50%;
    background: currentColor;
    animation: blink 1.2s infinite ease-in-out;
  }
  .dots i:nth-child(2) {
    animation-delay: 0.15s;
  }
  .dots i:nth-child(3) {
    animation-delay: 0.3s;
  }
  @keyframes blink {
    0%,
    80%,
    100% {
      opacity: 0.25;
    }
    40% {
      opacity: 1;
    }
  }
  .offline {
    margin-bottom: 8px;
  }
  .box {
    position: relative;
    border: 1px solid var(--line-strong);
    border-radius: var(--r-surface);
    background: color-mix(in srgb, var(--surface) 88%, transparent);
    backdrop-filter: blur(12px);
    box-shadow: 0 1px 2px rgb(0 0 0 / 0.04);
    transition:
      border-color var(--t-fast) var(--ease),
      box-shadow var(--t-fast) var(--ease);
    max-width: calc(var(--measure) + 120px);
  }
  .box:focus-within {
    border-color: color-mix(in srgb, var(--ink) 35%, var(--surface));
    box-shadow: 0 1px 6px rgb(0 0 0 / 0.06);
  }
  .box.scoped {
    border-color: color-mix(in srgb, var(--ink) 35%, var(--surface));
  }
  .scope {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 6px 6px 12px;
    border-bottom: 1px solid var(--line);
    border-radius: 15px 15px 0 0;
    background: var(--surface-subtle);
    color: var(--ink);
    font-size: 13px;
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
    padding: 12px 14px 2px;
    border: 0;
    background: transparent;
    resize: none;
    font-size: 14px;
    line-height: 20px;
    color: var(--ink);
  }
  .input-area:focus-visible {
    outline: none;
  }
  .input-area::placeholder {
    color: color-mix(in srgb, var(--ink-secondary) 85%, transparent);
  }
  .toolbar {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    padding: 4px 8px 8px 8px;
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
    border-color: transparent;
    background: none;
    color: var(--ink-secondary);
  }
  .ctx:hover,
  .ctx[aria-expanded='true'] {
    background: var(--hover);
    color: var(--ink);
  }
  .ctx:not(.set) {
    min-width: 30px;
    padding: 0;
    justify-content: center;
  }
  .ctx.set {
    color: var(--ink);
    background: var(--hover);
  }
  .projects {
    position: static;
  }
  .project-pop {
    position: absolute;
    bottom: calc(100% + 6px);
    left: 44px;
    z-index: 20;
    display: grid;
    gap: 4px;
    width: min(320px, calc(100% - 56px));
    min-width: 0;
    max-height: min(300px, 50vh);
    overflow: auto;
    margin: 0;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    box-shadow: var(--shadow-pop);
  }
  .project-pop .check {
    align-items: flex-start;
  }
  .project-pop .check span {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .send {
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    flex: none;
    border: 0;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-ink);
    cursor: pointer;
    transition: opacity var(--t-fast) var(--ease);
  }
  .send:disabled {
    opacity: 0.35;
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
    min-height: 18px;
    margin-top: 5px;
    padding: 0 6px;
    font-size: 11.5px;
    color: color-mix(in srgb, var(--ink-secondary) 85%, transparent);
  }
  .restart {
    margin-left: 6px;
    min-height: 26px;
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
