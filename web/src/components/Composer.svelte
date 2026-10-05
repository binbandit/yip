<script lang="ts">
  // The composer. Recipients come from the mention list or selected work,
  // never from typed or pasted names. Drafts are kept per room and
  // thread on this device. A selected job keeps its context when it finishes:
  // further messages follow up with its engineer in the original thread.
  import { onMount, tick, untrack } from 'svelte';
  import { Button, CheckboxInput, Icon, IconButton, Text, VisuallyHidden } from '@astryx-svelte/core';
  import { ArrowUp, AtSign, Folder, GitCommitHorizontal, MessageSquareReply, X } from '@lucide/svelte';
  import { app, receiptKey } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { draftKey, loadDraft, saveDraft, clearDraft } from '../lib/state/drafts';
  import { filterCandidates, findMentionQuery, insertMention, projectsNamedIn, pruneSelected, resolveMentions, type MentionCandidate, type SelectedMention } from '../lib/util/mentions';
  import { deliveryReceipt, jobStateLabel } from '../lib/util/labels';
  import { isLiveJob, type PendingMessage } from '../lib/state/data';
  import type { Mention } from '../lib/api/types.gen';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import Avatar from './Avatar.svelte';
  import StateIcon from './StateIcon.svelte';
  import Notice from './Notice.svelte';
  import ReplyActivity from './ReplyActivity.svelte';

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
  let mentionBlurTimer: ReturnType<typeof setTimeout> | null = null;

  // ---- steering scope ----
  const scopeJobId = $derived(app.steer[rkey] ?? null);
  const scopeJob = $derived(scopeJobId ? app.data.jobs[scopeJobId] : undefined);
  const followUp = $derived(scopeJob && !isLiveJob(scopeJob) ? scopeJob : null);
  const scopeMissing = $derived(scopeJobId && !scopeJob ? details.jobs[scopeJobId]?.error : undefined);
  $effect(() => {
    if (scopeJobId && !scopeJob) {
      const id = scopeJobId;
      const touch = app.data.touched.jobs[id] ?? 0;
      untrack(() => details.ensureJob(id, touch));
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
      if (d.jobId && app.steer[rkey] === undefined) app.steer[rkey] = d.jobId;
    }
    void tick().then(autosize);
    window.addEventListener('pagehide', flushDraft);
    window.addEventListener('yip:before-workspace-switch', flushDraft);
    return () => {
      window.removeEventListener('pagehide', flushDraft);
      window.removeEventListener('yip:before-workspace-switch', flushDraft);
      cancelMentionBlur();
      flushDraft();
    };
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

  function cancelMentionBlur() {
    if (mentionBlurTimer) clearTimeout(mentionBlurTimer);
    mentionBlurTimer = null;
  }
  function onMentionBlur() {
    cancelMentionBlur();
    mentionBlurTimer = setTimeout(() => {
      mentionBlurTimer = null;
      query = null;
    }, 150);
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
    if (!text || (scopeJobId && !scopeJob)) return;
    const target = followUp;
    if (scopeJob && (scopeJob.source.roomId !== roomId || (target && !target.source.messageId))) {
      sendError = 'This work has no conversation here. Clear the selected work to send a room message.';
      return;
    }
    const chosenMentions = resolveMentions(text, selected);
    const mentions: Mention[] = target
      ? [{ kind: 'engineer', id: target.ownerId }, ...chosenMentions.filter((m) => m.kind !== 'engineer' || m.id !== target.ownerId)]
      : chosenMentions;
    const jobId = target ? undefined : scopeJobId ?? undefined;
    const destinationThread = target ? target.source.threadId || target.source.messageId : threadId;
    const replyToId = target ? target.source.messageId : answering?.messageId || (threadId ? restoredReplyTo || undefined : undefined);
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
    if (target) app.steer[rkey] = null;
    if (!jobId) delete app.receipts[rkey];
    await tick();
    autosize();
    textarea?.focus();
    // A thread plus an explicit recipient keeps a follow-up separate from
    // unrelated live work that the same engineer may now own in this room.
    const sending = app.send({ roomId, threadId: destinationThread, body: text, mentions, projectIds: pids, jobId, replyToId });
    if (target && destinationThread) app.go({ name: 'room', roomId }, { panel: { kind: 'thread', id: destinationThread } });
    const resp = await sending;
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
  const projectNames = $derived(projectIds.map((p) => app.data.projects[p]?.name).filter(Boolean).join(', '));
  // VisuallyHidden types only the generic HTML attributes, so the label's `for` goes in through a spread.
  const inputLabel = { for: `${uid}-input` };
</script>

<div class="composer" class:compact>
  {#if offline}
    <Notice class="offline" tone="warning" role="status">Can't reach your workspace. Your draft is saved on this device.</Notice>
  {/if}

  <div class="box" class:scoped={!!scopeJobId || !!answering}>
    {#if answering}
      <div class="scope answering" role="status">
        <Icon icon={MessageSquareReply} size="sm" color="secondary" />
        <span class="scope-text truncate">Answering {app.engineerName(answering.askerId)}'s question{answering.missingFact ? `: ${answering.missingFact}` : ''}</span>
        <Button label="Not an answer" variant="ghost" size="sm" onclick={() => (notAnswer = answering?.id ?? null)} />
      </div>
    {/if}
    {#if scopeJob}
      <div class="scope" role="status">
        <Icon icon={followUp ? MessageSquareReply : GitCommitHorizontal} size="sm" color="secondary" />
        <span class="scope-text" class:truncate={!followUp}>
          {#if followUp}
            <strong>{scopeJob.title}</strong> is {jobStateLabel(scopeJob).toLowerCase()}. Follow up with {app.engineerName(scopeJob.ownerId)} in its thread.
          {:else}
            Adding to: <strong>{scopeJob.title}</strong> · {app.engineerName(scopeJob.ownerId)}
          {/if}
        </span>
        <IconButton class="clear" label="Clear selected work: {scopeJob.title}" tooltip="Clear selected work" variant="ghost" size="sm" onclick={clearScope}>
          {#snippet icon()}<Icon icon={X} size="sm" />{/snippet}
        </IconButton>
      </div>
    {:else if scopeJobId}
      <div class="scope" role="status">
        <Icon icon={GitCommitHorizontal} size="sm" color="secondary" />
        <span class="scope-text">{scopeMissing ? 'Selected work is unavailable. Clear it to send a room message.' : 'Loading selected work. Your draft is saved.'}</span>
        <IconButton class="clear" label="Clear selected work" tooltip="Clear selected work" variant="ghost" size="sm" onclick={clearScope}>
          {#snippet icon()}<Icon icon={X} size="sm" />{/snippet}
        </IconButton>
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
              <span class="opt-handle">@{c.handle}</span>
            </div>
          {/each}
        </div>
      {/if}
      <VisuallyHidden as="label" {...inputLabel}>{placeholder}</VisuallyHidden>
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
        onfocus={cancelMentionBlur}
        onblur={onMentionBlur}
      ></textarea>
    </div>

    <div class="toolbar">
      <IconButton
        class="mention-tool"
        label="Mention someone"
        tooltip="Mention someone"
        variant="ghost"
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
        }}
      >
        {#snippet icon()}<Icon icon={AtSign} size="sm" />{/snippet}
      </IconButton>
      {#if roomProjects.length}
        <div class="projects">
          <!-- Icon-only until a project is chosen; then the project names are its label. -->
          <Button
            class={projectIds.length ? 'ctx set' : 'ctx'}
            label={projectIds.length ? projectNames : 'Add project context'}
            isIconOnly={!projectIds.length}
            tooltip={projectIds.length ? undefined : 'Add project context'}
            variant="ghost"
            size="sm"
            aria-expanded={projectsOpen}
            aria-controls="{uid}-projects"
            onclick={() => (projectsOpen = !projectsOpen)}
          >
            {#snippet icon()}<Icon icon={Folder} size="sm" />{/snippet}
          </Button>
          {#if projectsOpen}
            <fieldset class="project-pop" id="{uid}-projects">
              <VisuallyHidden as="legend">Projects for this message</VisuallyHidden>
              {#each roomProjects as p (p.id)}
                <CheckboxInput label={p.name} value={projectIds.includes(p.id)} onChange={() => toggleProject(p.id)} />
              {/each}
              <Button label="Done" size="sm" onclick={() => (projectsOpen = false)} />
            </fieldset>
          {/if}
        </div>
      {/if}
      {#if mentioned.length}
        <Text class="mentioning" type="supporting" maxLines={1}>Asking {mentioned.map((m) => app.engineerName(m.id)).join(', ')}</Text>
      {/if}
      <span class="spacer"></span>
      <IconButton class="send" label={sendLabel} tooltip={sendLabel} variant="primary" isDisabled={!body.trim() || (!!scopeJobId && !scopeJob)} onclick={send}>
        {#snippet icon()}<Icon icon={ArrowUp} size="sm" />{/snippet}
      </IconButton>
    </div>
  </div>

  <p class="hint" id="{uid}-hint">
    {#if receipt}
      <span class="receipt" class:final={receipt.delivery !== 'pending'} role="status">
        <StateIcon shape={receipt.delivery === 'immediate' ? 'check-filled' : receipt.delivery === 'queued' ? 'circle' : 'bar'} tone={receipt.delivery === 'immediate' ? 'success' : 'accent'} size={12} live={receipt.delivery === 'pending'} />
        {receipt.text}
      </span>
      {#if receipt.canRestart}
        <Button class="restart" label="Interrupt and restart now" variant="ghost" size="sm" isLoading={restarting} onclick={restartNow} />
      {/if}
    {:else if sendError}
      <span class="tone-danger">{sendError}</span>
    {:else if note}
      <span class="receipt final"><StateIcon shape="check-filled" tone="success" size={12} />{note}</span>
    {:else}
      <ReplyActivity {roomId} {threadId} />
    {/if}
    <VisuallyHidden>{app.data.preferences.sendKey === 'mod-enter' ? 'Command or Control and Enter sends; Enter adds a new line.' : 'Enter sends; Shift and Enter adds a new line.'} Type @ to mention an engineer.</VisuallyHidden>
  </p>
</div>

<style>
  .composer {
    flex: none;
    padding: 0 var(--spacing-4) calc(var(--spacing-3) + env(safe-area-inset-bottom));
    background: linear-gradient(to bottom, transparent, var(--color-background-surface) var(--spacing-3));
  }
  .compact {
    padding: 0 var(--spacing-3) calc(var(--spacing-2) + env(safe-area-inset-bottom));
  }
  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  .composer :global(.offline) {
    max-width: calc(var(--yip-measure) + 120px);
    margin-bottom: var(--spacing-2);
  }

  .box {
    position: relative;
    max-width: calc(var(--yip-measure) + 120px);
    border: 1px solid var(--color-border-emphasized);
    border-radius: var(--radius-container);
    background: color-mix(in srgb, var(--color-background-surface) 88%, transparent);
    backdrop-filter: blur(12px);
    transition:
      border-color var(--duration-fast) var(--ease-standard),
      box-shadow var(--duration-fast) var(--ease-standard);
  }
  .box:focus-within {
    border-color: color-mix(in srgb, var(--color-text-primary) 35%, var(--color-background-surface));
    box-shadow: var(--shadow-low);
  }
  .box.scoped {
    border-color: color-mix(in srgb, var(--color-text-primary) 35%, var(--color-background-surface));
  }

  .scope {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    padding: var(--spacing-1) var(--spacing-1) var(--spacing-1) var(--spacing-3);
    border-bottom: 1px solid var(--color-border);
    border-radius: calc(var(--radius-container) - 1px) calc(var(--radius-container) - 1px) 0 0;
    background: var(--color-background-muted);
    color: var(--color-text-primary);
    font-size: var(--text-body-size);
    line-height: var(--text-body-leading);
  }
  .scope-text {
    flex: 1;
  }
  .scope strong {
    font-weight: var(--font-weight-semibold);
  }

  .field-wrap {
    position: relative;
  }
  .input-area {
    display: block;
    width: 100%;
    min-height: 44px;
    max-height: 40vh;
    padding: var(--spacing-3) var(--spacing-3) var(--spacing-0-5);
    border: 0;
    background: transparent;
    resize: none;
    font-family: var(--font-family-body);
    font-size: var(--text-body-size);
    line-height: var(--text-body-leading);
    color: var(--color-text-primary);
    caret-color: var(--color-accent);
  }
  .input-area:focus-visible {
    outline: none;
  }
  /* The visible label: the <label> itself is visually hidden. */
  .input-area::placeholder {
    color: var(--color-text-secondary);
  }

  .toolbar {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-1);
    padding: var(--spacing-1) var(--spacing-2) var(--spacing-2);
    min-width: 0;
  }
  .spacer {
    flex: 1;
  }
  .toolbar :global(.mentioning) {
    min-width: 0;
    flex: 0 1 auto;
  }
  .projects {
    /* Static, so the picker positions against the whole box. */
    position: static;
    min-width: 0;
  }
  .projects :global(.ctx) {
    max-width: min(320px, 60vw);
  }
  /* Toolbar tools rest in secondary ink and firm up when used. */
  .toolbar :global(.mention-tool),
  .projects :global(.ctx) {
    color: var(--color-text-secondary);
  }
  .toolbar :global(.mention-tool:hover),
  .projects :global(.ctx:hover),
  .projects :global(.ctx[aria-expanded='true']),
  .projects :global(.ctx.set) {
    color: var(--color-text-primary);
  }
  .projects :global(.ctx.set),
  .projects :global(.ctx[aria-expanded='true']) {
    background-color: var(--color-overlay-hover);
  }
  .project-pop {
    position: absolute;
    bottom: calc(100% + var(--spacing-1-5));
    left: 44px;
    z-index: 20;
    display: grid;
    justify-items: start;
    gap: var(--spacing-2);
    width: min(320px, calc(100% - 56px));
    min-width: 0;
    max-height: min(300px, 50vh);
    overflow: auto;
    margin: 0;
    padding: var(--spacing-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    background: var(--color-background-popover);
    box-shadow: var(--shadow-med);
  }
  .project-pop :global(.astryx-checkbox-input) {
    width: 100%;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .toolbar :global(.send) {
    flex: none;
    border-radius: var(--radius-full);
  }

  .listbox {
    position: absolute;
    bottom: calc(100% + var(--spacing-2));
    left: 0;
    right: 0;
    z-index: 30;
    max-width: 520px;
    max-height: 280px;
    overflow: auto;
    padding: var(--spacing-1);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
    background: var(--color-background-popover);
    box-shadow: var(--shadow-med);
  }
  .option {
    display: grid;
    grid-template-columns: 24px auto minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--spacing-2);
    min-height: 40px;
    padding: var(--spacing-1) var(--spacing-2);
    border-radius: var(--radius-inner);
    cursor: pointer;
    font-size: var(--text-body-size);
  }
  .option.active {
    background: var(--color-accent-muted);
  }
  .option.unavailable {
    cursor: not-allowed;
  }
  .option.unavailable .opt-name {
    color: var(--color-text-secondary);
  }
  .opt-name {
    font-weight: var(--font-weight-semibold);
  }
  .opt-role {
    color: var(--color-text-secondary);
    font-size: var(--text-supporting-size);
  }
  .opt-handle {
    color: var(--color-text-secondary);
    font-family: var(--font-family-code);
    font-size: var(--text-supporting-size);
  }

  .hint {
    min-height: 18px;
    margin-top: var(--spacing-1);
    padding: 0 var(--spacing-1-5);
    font-size: var(--text-supporting-size);
    line-height: var(--text-supporting-leading);
    color: var(--color-text-secondary);
  }
  .hint :global(.restart) {
    margin-left: var(--spacing-1-5);
  }
  .receipt {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
    color: var(--color-text-primary);
    font-weight: var(--font-weight-medium);
  }
  @media (max-width: 768px) {
    .composer {
      padding: 0 var(--spacing-2) calc(var(--spacing-2) + env(safe-area-inset-bottom));
    }
    /* iOS zooms into fields set below 16px. */
    .input-area {
      font-size: 1rem;
    }
    .toolbar :global(.send) {
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
</style>
