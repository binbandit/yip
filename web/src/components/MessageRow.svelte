<script lang="ts">
  // One message: avatar, name, role (first in a group), time, body, evidence,
  // reactions and thread summary. Structured kinds render distinctly. Actions
  // appear on hover and whenever the row has keyboard focus.
  import { untrack } from 'svelte';
  import { Check, Ellipsis, Link as LinkIcon, MessageSquareReply, Pencil, SmilePlus, Trash2 } from '@lucide/svelte';
  import { AvatarGroup, Button, DropdownMenu, Icon, IconButton, Link, Popover, Text, TextArea, Tooltip } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import type { Message } from '../lib/api/types.gen';
  import { clock, fullTime, relative } from '../lib/util/time';
  import Avatar from './Avatar.svelte';
  import MessageBody from './MessageBody.svelte';
  import Reactions from './Reactions.svelte';
  import ResultCard from './ResultCard.svelte';
  import ApprovalCard from './ApprovalCard.svelte';
  import RefChips from './RefChips.svelte';

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
  // The quiet text after the name: an engineer's role, or where a hub answer comes from.
  const role = $derived(isEngineer ? engineer?.role : systemAuthored ? 'From the work ledger' : undefined);
  const jobRef = $derived(message.refs.find((r) => r.kind === 'job'));
  const approvalRef = $derived(message.refs.find((r) => r.kind === 'approval'));
  const questionRef = $derived(message.refs.find((r) => r.kind === 'question'));
  // The question this message answered (a receipt that stays with it).
  const answered = $derived(
    message.kind === 'question' ? undefined : Object.values(app.data.questions).find((q) => q.answerMessageId === message.id),
  );
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
  // The emoji palette and the more menu are Astryx overlays: Escape and focus
  // return are their own. The action pill stays shown while either is open,
  // and one frame longer so the overlay can hand focus back to its trigger.
  let emojiOpen = $state(false);
  let menuOpen = $state(false);
  let pinned = $state(false);
  $effect(() => {
    if (emojiOpen || menuOpen) {
      pinned = true;
      return;
    }
    if (!untrack(() => pinned)) return;
    const frame = requestAnimationFrame(() => (pinned = false));
    return () => cancelAnimationFrame(frame);
  });

  // The pill and its overlays exist only while the row is in use, so a long
  // history carries no idle menus. CSS still decides when a mounted pill shows
  // (hover, focus within, pinned; focus or pinned alone on touch screens).
  // Roving focus lands on the article first, so Tab still reaches the pill.
  let hovered = $state(false);
  let focusWithin = $state(false);
  const active = $derived(hovered || focusWithin || pinned);

  function onFocusOut(e: FocusEvent) {
    const row = e.currentTarget as HTMLElement;
    if (e.relatedTarget instanceof Node && row.contains(e.relatedTarget)) return;
    // Check where focus settled: switching windows leaves it here, and an
    // overlay handing it back to its trigger lands here again.
    setTimeout(() => (focusWithin = row.contains(document.activeElement)));
  }

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

{#snippet time(cls: string)}
  <Tooltip content={fullTime(message.createdAt)}>
    <time class={cls} datetime={message.createdAt}>{clock(message.createdAt)}</time>
  </Tooltip>
{/snippet}

{#snippet moreIcon()}<Icon icon={Ellipsis} size="sm" />{/snippet}
{#snippet editIcon()}<Icon icon={Pencil} size="sm" />{/snippet}
{#snippet removeIcon()}<Icon icon={Trash2} size="sm" />{/snippet}

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
      {@render time('status-time')}
      {#if approvalRef}<ApprovalCard approvalId={approvalRef.id} />{/if}
      <RefChips refs={message.refs} skip={['approval']} hide={seenRefs} messageId={message.id} />
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
    class:pinned
    data-message-id={message.id}
    {tabindex}
    aria-label="{app.actorName(author)}{engineer ? `, AI engineer, ${engineer.role}` : ''}, {clock(message.createdAt)}"
    onpointerenter={(e) => (hovered = e.pointerType !== 'touch')}
    onpointerleave={() => (hovered = false)}
    onfocusin={() => (focusWithin = true)}
    onfocusout={onFocusOut}
  >
    <div class="gutter">
      {#if continuation}
        {@render time('hover-time')}
      {:else}
        <Avatar actor={author} size={36} />
      {/if}
    </div>
    <div class="main">
      {#if !continuation}
        <header class="header">
          {#if isEngineer}
            <Link class="name" color="primary" weight="semibold" onclick={openAuthor}>{app.actorName(author)}</Link>
          {:else}
            <Text class="name" weight="semibold">{systemAuthored ? 'yip' : app.actorName(author)}</Text>
          {/if}
          {#if role}
            <Text class="role-badge" type="supporting">{role}</Text>
            <span class="sep" aria-hidden="true">·</span>
          {/if}
          {@render time('time')}
          {#if message.editedAt && !deleted}<Text type="supporting">(edited)</Text>{/if}
        </header>
      {/if}

      {#if deleted}
        <Text as="p" type="supporting" class="removed">This message was removed.</Text>
      {:else if editing}
        <div class="edit">
          <TextArea
            label="Edit message"
            isLabelHidden
            hasAutoFocus
            bind:value={draft}
            rows={3}
            onkeydown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) saveEdit();
              if (e.key === 'Escape') {
                e.stopPropagation();
                editing = false;
              }
            }}
          />
          <div class="edit-actions">
            <Button label="Cancel" size="sm" onclick={() => (editing = false)} />
            <Button label="Save" variant="primary" size="sm" isDisabled={saving} onclick={saveEdit} />
            <Text type="supporting">Editing changes the text only; anyone already asked keeps the original request.</Text>
          </div>
        </div>
      {:else}
        <MessageBody {message} />
        {#if message.editedAt && continuation}<Text type="supporting">(edited)</Text>{/if}
        {#if answered}
          <Text as="p" type="supporting" class="answered">
            <Icon icon={Check} size="xsm" />Answers {app.engineerName(answered.askerId)}'s question — the work waiting on it resumed
          </Text>
        {/if}

        {#if message.kind === 'question'}
          <div class="question">
            {#if question?.status === 'answered'}
              <span class="q-state done"><Icon icon={Check} size="sm" />Answered</span>
            {:else if question?.status === 'cancelled'}
              <span class="q-state">No longer needed</span>
            {:else}
              <span class="q-state">{asksMe ? `${app.actorName(author)} asked you` : 'Question'}</span>
            {/if}
            {#if onreply && !inThread && !settled && !(message.thread && message.thread.replyCount > 0)}
              <Button label="Reply in thread" size="sm" onclick={onreply}>
                {#snippet icon()}<Icon icon={MessageSquareReply} size="sm" />{/snippet}
              </Button>
            {/if}
          </div>
        {/if}

        {#if message.kind === 'approval' && approvalRef}
          <ApprovalCard approvalId={approvalRef.id} />
        {/if}
        {#if message.kind === 'result' && jobRef}
          <ResultCard jobId={jobRef.id} />
        {/if}
        <RefChips refs={message.refs} skip={message.kind === 'result' ? ['job', 'approval'] : ['approval']} hide={seenRefs} messageId={message.id} />
        <Reactions {message} />

        {#if onreply && !inThread && message.thread && message.thread.replyCount > 0}
          {@const replies = `${message.thread.replyCount} ${message.thread.replyCount === 1 ? 'reply' : 'replies'}`}
          {@const last = `last reply ${relative(message.thread.lastReplyAt, app.now)}`}
          <Button class="thread-summary" variant="ghost" size="sm" label="{replies} · {last}" onclick={onreply}>
            <span class="faces" aria-hidden="true">
              <AvatarGroup size={20} shape="rounded">
                {#each threadParticipants as p (p.kind + p.id)}<Avatar actor={p} size={20} />{/each}
              </AvatarGroup>
            </span>
            <strong>{replies}</strong>
            <span class="last">· {last}</span>
          </Button>
        {/if}
      {/if}
    </div>

    {#if active && !deleted && !editing}
      <div class="actions" role="toolbar" aria-label="Message actions">
        <Popover
          label="Reactions"
          class="emoji-pop"
          alignment="end"
          closeButtonLabel="Close reactions"
          isOpen={emojiOpen}
          onOpenChange={(open) => (emojiOpen = open)}
        >
          <IconButton class="pill-action" label="Add reaction" tooltip="Add reaction" variant="ghost" size="sm">
            {#snippet icon()}<Icon icon={SmilePlus} size="sm" />{/snippet}
          </IconButton>
          {#snippet content()}
            <div class="emoji-row">
              {#each QUICK as e (e)}
                <IconButton label="React {e}" variant="ghost" onclick={() => react(e)}>
                  {#snippet icon()}<span class="emoji">{e}</span>{/snippet}
                </IconButton>
              {/each}
            </div>
          {/snippet}
        </Popover>
        {#if onreply && !inThread && !message.threadId}
          <IconButton class="pill-action" label="Reply in thread" tooltip="Reply in thread" variant="ghost" size="sm" onclick={onreply}>
            {#snippet icon()}<Icon icon={MessageSquareReply} size="sm" />{/snippet}
          </IconButton>
        {/if}
        <IconButton class="pill-action" label="Copy link to message" tooltip="Copy link to message" variant="ghost" size="sm" onclick={copyLink}>
          {#snippet icon()}<Icon icon={LinkIcon} size="sm" />{/snippet}
        </IconButton>
        {#if mine}
          <!-- DropdownMenu rather than MoreMenu: only its `button` takes a class. -->
          <DropdownMenu
            button={{ label: 'More actions', tooltip: 'More actions', icon: moreIcon, isIconOnly: true, variant: 'ghost', size: 'sm', class: 'pill-action' }}
            hasChevron={false}
            alignment="end"
            onOpenChange={(open) => (menuOpen = open)}
            items={[
              { label: 'Edit', icon: editIcon, onClick: startEdit },
              { label: 'Remove', icon: removeIcon, variant: 'destructive', onClick: remove },
            ]}
          />
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
    margin: 0 var(--spacing-1-5);
    padding: var(--spacing-1) 10px var(--spacing-1) var(--spacing-2);
    border-radius: var(--radius-container);
    transition: background-color var(--duration-fast) var(--ease-standard);
  }
  .msg:not(.continuation) {
    margin-top: var(--yip-group-gap);
  }
  .msg:hover,
  .msg:focus-within,
  .status:hover {
    background: color-mix(in srgb, var(--color-background-muted) 70%, transparent);
  }
  .msg:focus-visible,
  .status:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: -2px;
  }
  /* A question for you waits on a soft amber wash until it is answered. */
  .msg.asks-me {
    background: color-mix(in srgb, var(--color-warning-muted) 45%, transparent);
  }
  .msg.asks-me:hover,
  .msg.asks-me:focus-within {
    background: color-mix(in srgb, var(--color-warning-muted) 70%, transparent);
  }
  .highlight {
    animation: flash 2.4s var(--ease-standard);
  }
  @keyframes flash {
    0%,
    40% {
      background: var(--color-accent-muted);
    }
  }
  .gutter {
    display: flex;
    justify-content: center;
    padding-top: var(--spacing-0-5);
  }
  .hover-time {
    opacity: 0;
    font-size: var(--font-size-xs);
    line-height: 24px;
    /* One line, centred on the avatar column even when it's a little wider. */
    white-space: nowrap;
    color: var(--color-text-secondary);
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
    gap: var(--spacing-0-5) var(--spacing-1-5);
    margin-bottom: var(--spacing-0-5);
    line-height: 16px;
  }
  .header :global(.name) {
    line-height: 20px;
    letter-spacing: -0.015em;
  }
  .sep {
    font-size: var(--font-size-sm);
    color: color-mix(in srgb, var(--color-text-primary) 40%, transparent);
  }
  .time {
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
    font-variant-numeric: tabular-nums;
  }
  .main :global(.removed) {
    font-style: italic;
  }
  .main :global(.answered) {
    display: flex;
    align-items: center;
    gap: var(--spacing-1);
    margin: var(--spacing-0-5) 0 0;
  }
  .question {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: var(--spacing-2);
  }
  .q-state {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1);
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-medium);
    color: var(--color-text-secondary);
  }
  .q-state.done {
    color: var(--color-success);
  }
  .main :global(.thread-summary) {
    margin-top: var(--spacing-1);
    padding-inline: 3px 10px;
    border-radius: var(--radius-full);
    font-size: var(--font-size-sm);
  }
  .main :global(.thread-summary:hover) {
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-border-emphasized) 70%, transparent);
    background: var(--color-background-surface);
  }
  .faces {
    display: inline-flex;
    vertical-align: middle;
    margin-right: var(--spacing-1);
  }
  strong {
    font-weight: var(--font-weight-semibold);
  }
  .last {
    margin-left: var(--spacing-1);
    font-weight: var(--font-weight-normal);
    color: var(--color-text-secondary);
  }
  .actions {
    position: absolute;
    top: -14px;
    right: 12px;
    display: none;
    align-items: center;
    gap: var(--spacing-0-5);
    padding: 3px;
    border-radius: var(--radius-full);
    border: 1px solid color-mix(in srgb, var(--color-border-emphasized) 70%, transparent);
    background: color-mix(in srgb, var(--color-background-surface) 95%, transparent);
    backdrop-filter: blur(4px);
    box-shadow: var(--shadow-low);
    z-index: 5;
  }
  .msg:hover .actions,
  .msg:focus-within .actions,
  .msg.pinned .actions {
    display: flex;
  }
  /* The pill's own triggers only: the emoji palette renders inside .actions. */
  .actions :global(.pill-action) {
    border-radius: var(--radius-full);
  }
  .emoji-row {
    display: flex;
    gap: var(--spacing-0-5);
  }
  .emoji {
    font-size: 18px;
    line-height: 1;
  }
  .edit {
    display: grid;
    gap: var(--spacing-2);
    margin-top: var(--spacing-1);
  }
  .edit-actions {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    flex-wrap: wrap;
  }

  .status {
    display: flex;
    justify-content: center;
    margin: var(--spacing-3) var(--spacing-4) var(--spacing-1);
    padding: var(--spacing-0-5) var(--spacing-3);
    border-radius: var(--radius-container);
    color: var(--color-text-secondary);
    font-size: var(--font-size-sm);
    text-align: center;
  }
  .status-body {
    max-width: 560px;
  }
  .status-body :global(.prose),
  .status-body :global(.prose p) {
    display: inline;
  }
  .status-time {
    margin-left: var(--spacing-1-5);
    font-variant-numeric: tabular-nums;
    color: color-mix(in srgb, var(--color-text-secondary) 80%, transparent);
  }
  .status-time::before {
    content: '·';
    margin-right: var(--spacing-1-5);
  }

  @media (hover: none) {
    .actions {
      position: static;
      display: none;
      grid-column: 2;
      justify-self: start;
      box-shadow: none;
      margin-top: var(--spacing-1);
    }
    .msg:focus-within .actions,
    .msg.pinned .actions {
      display: flex;
    }
  }
  @media (pointer: coarse) {
    .actions :global(.pill-action) {
      width: 44px;
      height: 44px;
    }
  }
  @media (max-width: 768px) {
    .msg {
      margin: 0 var(--spacing-1);
      padding-right: var(--spacing-1-5);
    }
  }
</style>
