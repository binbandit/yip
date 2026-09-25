<script lang="ts">
  // One message: avatar, name, role (first in a group), time, body, evidence,
  // reactions and thread summary. Structured kinds render distinctly. Actions
  // appear on hover and whenever the row has keyboard focus.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Message } from '../lib/api/types.gen';
  import { clock, fullTime, relative } from '../lib/util/time';
  import { pushLayer } from '../lib/ui/layers';
  import Avatar from './Avatar.svelte';
  import Icon from './Icon.svelte';
  import MessageBody from './MessageBody.svelte';
  import Reactions from './Reactions.svelte';
  import ResultCard from './ResultCard.svelte';
  import ApprovalCard from './ApprovalCard.svelte';
  import RefChips from './RefChips.svelte';
  import Menu from './Menu.svelte';

  interface Props {
    message: Message;
    continuation?: boolean;
    highlight?: boolean;
    inThread?: boolean;
    tabindex?: number;
    /** References already shown on an earlier message in this view. */
    seenRefs?: string[];
    onreply?: () => void;
  }
  let { message, continuation = false, highlight = false, inThread = false, tabindex = -1, seenRefs = [], onreply }: Props = $props();

  const QUICK = ['👍', '✅', '👀', '🎉', '❤️', '🙏'];

  const author = $derived(message.author);
  const isEngineer = $derived(author.kind === 'engineer');
  const engineer = $derived(isEngineer ? app.data.engineers[author.id] : undefined);
  const mine = $derived(author.kind === 'user' && author.id === app.me?.id);
  const systemAuthored = $derived(message.kind === 'status' || message.kind === 'system' || author.kind === 'system');
  // One-line hub facts are quiet lines; longer hub answers (e.g. the Overview
  // status reply) read as a full message from the work ledger.
  const isStatus = $derived(systemAuthored && !message.body.trim().includes('\n') && message.kind !== 'approval');
  const jobRef = $derived(message.refs.find((r) => r.kind === 'job'));
  const approvalRef = $derived(message.refs.find((r) => r.kind === 'approval'));
  const questionRef = $derived(message.refs.find((r) => r.kind === 'question'));
  const question = $derived(questionRef ? app.data.questions[questionRef.id] : undefined);
  // A settled question no longer asks anything of you.
  const settled = $derived(message.kind === 'question' && (question?.status === 'answered' || question?.status === 'cancelled'));
  const asksMe = $derived(!settled && message.mentions.some((m) => m.kind === 'user' && m.id === app.me?.id));
  // Load the question so "Answered" stays truthful; question.* events keep it current.
  $effect(() => {
    if (message.kind !== 'question' || !questionRef || question) return;
    const id = questionRef.id;
    untrack(() => void details.ensureQuestion(id));
  });
  const deleted = $derived(!!message.deletedAt);

  let editing = $state(false);
  let draft = $state('');
  let saving = $state(false);
  let emojiOpen = $state(false);
  let emojiBtn: HTMLButtonElement | undefined = $state();

  $effect(() => {
    if (!emojiOpen) return;
    const release = untrack(() => pushLayer(() => (emojiOpen = false), { returnFocus: emojiBtn }));
    return () => release(false);
  });

  async function react(emoji: string) {
    emojiOpen = false;
    const existing = message.reactions.find((r) => r.emoji === emoji);
    try {
      const m = await api.react(message.id, emoji, !!existing?.mine);
      app.data.messages[m.id] = { ...m, mentions: m.mentions ?? [], refs: m.refs ?? [], reactions: m.reactions ?? [], projectIds: m.projectIds ?? [] };
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    }
  }

  function copyLink() {
    const url = new URL(`/rooms/${message.roomId}`, window.location.origin);
    url.searchParams.set('msg', message.id);
    if (message.threadId) url.searchParams.set('panel', `thread:${message.threadId}`);
    navigator.clipboard?.writeText(url.toString()).then(
      () => app.toast('Link copied.'),
      () => app.toast("Couldn't copy the link.", 'error'),
    );
  }

  function startEdit() {
    draft = message.body;
    editing = true;
  }

  async function saveEdit() {
    if (!draft.trim()) return;
    saving = true;
    try {
      const m = await api.editMessage(message.id, draft.trim());
      app.data.messages[m.id] = { ...m, mentions: m.mentions ?? [], refs: m.refs ?? [], reactions: m.reactions ?? [], projectIds: m.projectIds ?? [] };
      editing = false;
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    } finally {
      saving = false;
    }
  }

  async function remove() {
    try {
      await api.deleteMessage(message.id);
      app.data.messages[message.id] = { ...message, deletedAt: new Date().toISOString(), body: '' };
    } catch (err) {
      app.toast(errorMessage(err), 'error');
    }
  }

  function openAuthor() {
    if (isEngineer) app.openPanel({ kind: 'engineer', id: author.id });
  }

  const threadParticipants = $derived((message.thread?.participants ?? []).slice(0, 3));
</script>

{#if isStatus}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -- roving focus between messages (feed pattern) -->
  <article
    class="status"
    class:highlight
    data-message-id={message.id}
    {tabindex}
    aria-label="Update: {message.body.slice(0, 120)}"
  >
    <div class="status-body">
      <MessageBody {message} />
      <time class="status-time" datetime={message.createdAt} title={fullTime(message.createdAt)}>{clock(message.createdAt)}</time>
      {#if approvalRef}<ApprovalCard approvalId={approvalRef.id} />{/if}
      <RefChips refs={message.refs} skip={['approval']} hide={seenRefs} />
    </div>
  </article>
{:else}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -- roving focus between messages (feed pattern) -->
  <article
    class="msg"
    class:continuation
    class:highlight
    class:mine
    class:asks-me={asksMe && !mine}
    data-message-id={message.id}
    {tabindex}
    aria-label="{app.actorName(author)}{engineer ? `, ${engineer.role}` : ''}, {clock(message.createdAt)}"
  >
    <div class="gutter">
      {#if continuation}
        <time class="hover-time" datetime={message.createdAt} title={fullTime(message.createdAt)}>{clock(message.createdAt)}</time>
      {:else}
        <Avatar actor={author} size={36} />
      {/if}
    </div>
    <div class="main">
      {#if !continuation}
        <header class="header">
          {#if isEngineer}
            <button class="name linkish" onclick={openAuthor}>{app.actorName(author)}</button>
            {#if engineer?.role}<span class="role-badge">{engineer.role}</span>{/if}
          {:else if systemAuthored}
            <span class="name">yip</span>
            <span class="role-badge">From the work ledger</span>
          {:else}
            <span class="name">{app.actorName(author)}</span>
          {/if}
          <time class="time" datetime={message.createdAt} title={fullTime(message.createdAt)}>{clock(message.createdAt)}</time>
          {#if message.editedAt && !deleted}<span class="meta">(edited)</span>{/if}
        </header>
      {/if}

      {#if deleted}
        <p class="meta removed">This message was removed.</p>
      {:else if editing}
        <div class="edit">
          <label class="vh" for="edit-{message.id}">Edit message</label>
          <textarea
            id="edit-{message.id}"
            class="textarea"
            bind:value={draft}
            rows="3"
            onkeydown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) saveEdit();
              if (e.key === 'Escape') {
                e.stopPropagation();
                editing = false;
              }
            }}
          ></textarea>
          <div class="edit-actions">
            <button class="btn btn-sm" onclick={() => (editing = false)}>Cancel</button>
            <button class="btn btn-sm btn-primary" disabled={saving} onclick={saveEdit}>Save</button>
            <span class="meta">Editing changes the text only; anyone already asked keeps the original request.</span>
          </div>
        </div>
      {:else}
        <MessageBody {message} />
        {#if message.editedAt && continuation}<span class="meta">(edited)</span>{/if}

        {#if message.kind === 'question'}
          <div class="question">
            {#if question?.status === 'answered'}
              <span class="q-state tone-success"><Icon name="check" size={15} />Answered</span>
            {:else if question?.status === 'cancelled'}
              <span class="q-state muted">No longer needed</span>
            {:else}
              <span class="q-state">{asksMe ? `${app.actorName(author)} asked you` : 'Question'}</span>
            {/if}
            {#if !inThread && !settled && !(message.thread && message.thread.replyCount > 0)}
              <button class="btn btn-sm" onclick={() => onreply?.()}><Icon name="reply" size={15} />Reply in thread</button>
            {/if}
          </div>
        {/if}

        {#if message.kind === 'approval' && approvalRef}
          <ApprovalCard approvalId={approvalRef.id} />
        {/if}
        {#if message.kind === 'result' && jobRef}
          <ResultCard jobId={jobRef.id} />
        {/if}
        <RefChips refs={message.refs} skip={message.kind === 'result' ? ['job', 'approval'] : ['approval']} hide={seenRefs} />
        <Reactions {message} />

        {#if !inThread && message.thread && message.thread.replyCount > 0}
          <button class="thread-summary" onclick={() => onreply?.()}>
            <span class="faces" aria-hidden="true">
              {#each threadParticipants as p (p.kind + p.id)}<Avatar actor={p} size={20} />{/each}
            </span>
            <strong>{message.thread.replyCount} {message.thread.replyCount === 1 ? 'reply' : 'replies'}</strong>
            <span class="meta">· last reply {relative(message.thread.lastReplyAt, app.now)}</span>
          </button>
        {/if}
      {/if}
    </div>

    {#if !deleted && !editing}
      <div class="actions" role="toolbar" aria-label="Message actions">
        <div class="emoji-wrap">
          <button bind:this={emojiBtn} class="icon-btn act" aria-label="Add reaction" aria-expanded={emojiOpen} onclick={() => (emojiOpen = !emojiOpen)}>
            <Icon name="smile" size={17} />
          </button>
          {#if emojiOpen}
            <div class="emoji-pop" role="group" aria-label="Reactions">
              {#each QUICK as e (e)}
                <button class="emoji" aria-label="React {e}" onclick={() => react(e)}>{e}</button>
              {/each}
            </div>
          {/if}
        </div>
        {#if !inThread && !message.threadId}
          <button class="icon-btn act" aria-label="Reply in thread" onclick={() => onreply?.()}><Icon name="reply" size={17} /></button>
        {/if}
        <button class="icon-btn act" aria-label="Copy link to message" onclick={copyLink}><Icon name="link" size={17} /></button>
        {#if mine}
          <Menu
            label="More actions"
            buttonClass="icon-btn act"
            items={[
              { label: 'Edit', icon: 'pencil', onselect: startEdit },
              { label: 'Remove', icon: 'trash', danger: true, onselect: remove },
            ]}
          >
            {#snippet trigger()}<Icon name="more" size={17} />{/snippet}
          </Menu>
        {/if}
      </div>
    {/if}
  </article>
{/if}

<style>
  .msg {
    position: relative;
    display: grid;
    grid-template-columns: 36px minmax(0, 1fr);
    gap: 10px;
    margin: 0 6px;
    padding: 4px 10px 4px 8px;
    border-radius: 16px;
    transition: background-color 100ms var(--ease);
  }
  .msg:not(.continuation) {
    margin-top: var(--group-gap);
  }
  .msg:hover,
  .msg:focus-within,
  .status:hover {
    background: color-mix(in srgb, var(--surface-subtle) 70%, transparent);
  }
  .msg:focus-visible,
  .status:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .msg.asks-me {
    background: color-mix(in srgb, var(--attention-subtle) 75%, transparent);
  }
  .msg.asks-me:hover,
  .msg.asks-me:focus-within {
    background: var(--attention-subtle);
  }
  .highlight {
    animation: flash 2.4s var(--ease);
  }
  @keyframes flash {
    0%,
    40% {
      background: var(--accent-subtle);
    }
  }
  .gutter {
    display: flex;
    justify-content: center;
    padding-top: 2px;
  }
  .hover-time {
    opacity: 0;
    font-size: 11px;
    color: var(--ink-secondary);
    line-height: 24px;
    font-variant-numeric: tabular-nums;
  }
  .msg:hover .hover-time,
  .msg:focus-within .hover-time {
    opacity: 1;
  }
  .main {
    min-width: 0;
  }
  .header {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 2px 6px;
    line-height: 16px;
    margin-bottom: 2px;
  }
  .name {
    font-weight: 600;
    font-size: 14px;
    line-height: 20px;
    color: var(--ink);
  }
  .linkish {
    padding: 0;
    border: 0;
    background: none;
    cursor: pointer;
    font: inherit;
    font-weight: 600;
  }
  .linkish:hover {
    text-decoration: underline;
  }
  .role-badge + .time::before {
    content: '·';
    margin-right: 6px;
    color: color-mix(in srgb, var(--ink) 40%, transparent);
  }
  .time {
    font-size: 12px;
    color: color-mix(in srgb, var(--ink-secondary) 85%, transparent);
    font-variant-numeric: tabular-nums;
  }
  .removed {
    font-style: italic;
  }
  .question {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 8px;
  }
  .q-state {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 500;
    color: var(--ink-secondary);
  }
  .thread-summary {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-top: 4px;
    padding: 2px 10px 2px 3px;
    border: 1px solid transparent;
    border-radius: var(--r-pill);
    background: none;
    color: var(--ink);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
  }
  .thread-summary:hover {
    border-color: color-mix(in srgb, var(--line-strong) 70%, transparent);
    background: var(--surface);
  }
  .faces {
    display: inline-flex;
  }
  .faces :global(.avatar + .avatar) {
    margin-left: -5px;
    box-shadow: 0 0 0 2px var(--surface);
  }
  .actions {
    position: absolute;
    top: -14px;
    right: 12px;
    display: none;
    align-items: center;
    gap: 2px;
    padding: 3px;
    border-radius: var(--r-pill);
    border: 1px solid color-mix(in srgb, var(--line-strong) 70%, transparent);
    background: color-mix(in srgb, var(--surface) 95%, transparent);
    backdrop-filter: blur(4px);
    box-shadow: 0 1px 2px rgb(0 0 0 / 0.05);
    z-index: 5;
  }
  .msg:hover .actions,
  .msg:focus-within .actions {
    display: flex;
  }
  :global(.icon-btn.act) {
    width: 30px;
    height: 30px;
    border-radius: 50%;
  }
  .emoji-wrap {
    position: relative;
  }
  .emoji-pop {
    position: absolute;
    right: 0;
    top: calc(100% + 6px);
    display: flex;
    gap: 2px;
    padding: 4px;
    border-radius: var(--r-pill);
    border: 1px solid var(--line);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
  }
  .emoji {
    width: 34px;
    height: 34px;
    border: 0;
    border-radius: 50%;
    background: none;
    font-size: 18px;
    cursor: pointer;
  }
  .emoji:hover,
  .emoji:focus-visible {
    background: var(--hover);
  }
  .edit {
    display: grid;
    gap: 8px;
    margin-top: 4px;
  }
  .edit-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .status {
    display: flex;
    justify-content: center;
    margin: 12px 16px 4px;
    padding: 2px 12px;
    border-radius: 16px;
    color: var(--ink-secondary);
    font-size: 12px;
    text-align: center;
  }
  .status-body {
    max-width: 560px;
  }
  .status-body :global(.prose) {
    display: inline;
    color: var(--ink-secondary);
  }
  .status-body :global(.prose p) {
    display: inline;
  }
  .status-time {
    margin-left: 6px;
    font-variant-numeric: tabular-nums;
    color: color-mix(in srgb, var(--ink-secondary) 80%, transparent);
  }
  .status-time::before {
    content: '·';
    margin-right: 6px;
  }

  @media (hover: none) {
    .actions {
      position: static;
      display: none;
      grid-column: 2;
      justify-self: start;
      box-shadow: none;
      margin-top: 4px;
    }
    .msg:focus-within .actions {
      display: flex;
    }
    :global(.icon-btn.act) {
      width: 40px;
      height: 40px;
    }
  }
  @media (max-width: 760px) {
    .msg {
      margin: 0 4px;
      padding-right: 6px;
    }
  }
</style>
