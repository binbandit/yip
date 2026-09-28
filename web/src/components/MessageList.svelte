<script lang="ts">
  // The scrolling history. Appends stick to the bottom only when you're
  // already there; otherwise a "New messages" pill appears without jumping.
  // Loading older history preserves your reading position. Up/Down arrows move
  // between messages for keyboard and screen-reader users.
  import { tick, untrack, type Snippet } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import type { Message } from '../lib/api/types.gen';
  import type { PendingMessage } from '../lib/state/data';
  import { dayLabel, sameDay, toDate } from '../lib/util/time';
  import MessageRow from './MessageRow.svelte';
  import PendingRow from './PendingRow.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    items: Message[];
    pending?: PendingMessage[];
    hasMore?: boolean;
    loaded?: boolean;
    onloadolder?: () => Promise<boolean>;
    /** Messages after this seq (from others) are "new" since the room was opened. */
    newAfterSeq?: number | null;
    highlightId?: string | null;
    inThread?: boolean;
    label: string;
    onreply?: (m: Message) => void;
    onbottomchange?: (atBottom: boolean) => void;
    oneditpending?: (p: PendingMessage) => void;
    top?: Snippet;
    empty?: Snippet;
  }
  let {
    items,
    pending = [],
    hasMore = false,
    loaded = true,
    onloadolder,
    newAfterSeq = null,
    highlightId = null,
    inThread = false,
    label,
    onreply,
    onbottomchange,
    oneditpending,
    top,
    empty,
  }: Props = $props();

  let scroller: HTMLDivElement | undefined = $state();
  let atBottom = $state(true);
  let newCount = $state(0);
  let loadingOlder = $state(false);
  let activeId = $state<string | null>(null);

  const GROUP_WINDOW = 10 * 60 * 1000;

  interface Row {
    m: Message;
    day: string | null;
    showNew: boolean;
    continuation: boolean;
    /** References already shown on an earlier message (kind:id). */
    seenRefs: string[];
  }

  const rows: Row[] = $derived.by(() => {
    const out: Row[] = [];
    let prev: Message | null = null;
    let newShown = false;
    // A job or review is linked once, where it first comes up; later messages
    // about the same work read as plain conversation.
    const seen = new Set<string>();
    const now = new Date(app.now);
    for (const m of items) {
      const d = toDate(m.createdAt);
      const pd = prev ? toDate(prev.createdAt) : null;
      const day = !pd || !d || !sameDay(pd, d) ? dayLabel(m.createdAt, now) : null;
      const fromOther = !(m.author.kind === 'user' && m.author.id === app.me?.id);
      const showNew = !newShown && newAfterSeq != null && m.seq > newAfterSeq && fromOther;
      if (showNew) newShown = true;
      const structured = (k: string) => k === 'status' || k === 'system' || k === 'approval' || k === 'result';
      const continuation =
        !!prev &&
        !day &&
        !showNew &&
        prev.author.kind === m.author.kind &&
        prev.author.id === m.author.id &&
        !structured(m.kind) &&
        !structured(prev.kind) &&
        !!d &&
        !!pd &&
        d.getTime() - pd.getTime() < GROUP_WINDOW;
      const keys = m.refs.map((r) => `${r.kind}:${r.id}`);
      out.push({ m, day, showNew, continuation, seenRefs: keys.filter((k) => seen.has(k)) });
      for (const k of keys) seen.add(k);
      prev = m;
    }
    return out;
  });

  function isAtBottom(): boolean {
    if (!scroller) return true;
    return scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 48;
  }

  function scrollToBottom(smooth = false) {
    if (!scroller) return;
    scroller.scrollTo({ top: scroller.scrollHeight, behavior: smooth ? 'smooth' : 'auto' });
    newCount = 0;
  }

  function onScroll() {
    const b = isAtBottom();
    if (b !== atBottom) {
      atBottom = b;
      onbottomchange?.(b);
    }
    if (b) newCount = 0;
    if (scroller && scroller.scrollTop < 200 && hasMore && !loadingOlder) void loadOlder();
  }

  async function loadOlder() {
    if (!onloadolder || loadingOlder) return;
    loadingOlder = true;
    try {
      await onloadolder();
    } catch (err) {
      app.toast(err instanceof Error ? err.message : 'Could not load earlier messages.', 'error');
    } finally {
      loadingOlder = false;
    }
  }

  // --- keep position across updates ---
  const signature = $derived(
    `${items[0]?.id ?? ''}|${items[items.length - 1]?.id ?? ''}|${items.length}|${pending.length}`,
  );
  let snap = { height: 0, top: 0, bottom: true, first: '', last: '', count: 0 };
  let initialized = false;

  $effect.pre(() => {
    void signature;
    untrack(() => {
      if (!scroller) return;
      snap = {
        height: scroller.scrollHeight,
        top: scroller.scrollTop,
        bottom: isAtBottom(),
        first: snap.first,
        last: snap.last,
        count: snap.count,
      };
    });
  });

  $effect(() => {
    void signature;
    // Tracked so an empty room still initialises (and reports "at bottom").
    const isLoaded = loaded;
    untrack(() => {
      if (!scroller || !isLoaded) return;
      const first = items[0]?.id ?? '';
      const last = items[items.length - 1]?.id ?? '';
      if (!initialized) {
        initialized = true;
        void initialPosition();
      } else if (first !== snap.first && last === snap.last && items.length > snap.count) {
        // Older history prepended: keep the same content under the reader's eyes.
        scroller.scrollTop = scroller.scrollHeight - snap.height + snap.top;
      } else if (snap.bottom) {
        scrollToBottom();
      } else if (last !== snap.last && items.length > snap.count) {
        const added = items.slice(items.findIndex((m) => m.id === snap.last) + 1);
        newCount += added.filter((m) => !(m.author.kind === 'user' && m.author.id === app.me?.id)).length || 0;
        const lastMsg = items[items.length - 1];
        if (lastMsg && lastMsg.author.kind === 'user' && lastMsg.author.id === app.me?.id) scrollToBottom(true);
      }
      snap.first = first;
      snap.last = last;
      snap.count = items.length;
    });
  });

  async function initialPosition() {
    await tick();
    if (!scroller) return;
    if (highlightId && (await revealMessage(highlightId))) return;
    const divider = scroller.querySelector<HTMLElement>('.new-divider');
    if (divider && scroller.scrollHeight > scroller.clientHeight) {
      scroller.scrollTop = Math.max(0, divider.offsetTop - 80);
    } else {
      scrollToBottom();
    }
    const b = isAtBottom();
    atBottom = b;
    onbottomchange?.(b);
  }

  async function revealMessage(id: string): Promise<boolean> {
    for (let i = 0; i < 12; i++) {
      await tick();
      const el = scroller?.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(id)}"]`);
      if (el) {
        el.scrollIntoView({ block: 'center' });
        activeId = id;
        el.focus({ preventScroll: true });
        return true;
      }
      if (!hasMore || !onloadolder) return false;
      const got = await onloadolder();
      if (!got) return false;
    }
    return false;
  }

  $effect(() => {
    const id = highlightId;
    if (!id || !initialized) return;
    untrack(() => void revealMessage(id));
  });

  // --- keyboard navigation between messages ---
  function articles(): HTMLElement[] {
    return scroller ? [...scroller.querySelectorAll<HTMLElement>('article[data-message-id]')] : [];
  }

  function onKey(e: KeyboardEvent) {
    const t = e.target as HTMLElement;
    if (t.closest('textarea, input, [role="menu"], .emoji-pop')) return;
    const list = articles();
    if (!list.length) return;
    const current = t.closest<HTMLElement>('article[data-message-id]');
    let i = current ? list.indexOf(current) : -1;
    if (e.key === 'ArrowDown' || e.key === 'j') i = Math.min(list.length - 1, i + 1);
    else if (e.key === 'ArrowUp' || e.key === 'k') i = i < 0 ? list.length - 1 : Math.max(0, i - 1);
    else if (e.key === 'Home' && current) i = 0;
    else if (e.key === 'End' && current) i = list.length - 1;
    else return;
    if (e.key.length === 1 && !current) return;
    e.preventDefault();
    const el = list[i];
    activeId = el.dataset.messageId ?? null;
    el.focus();
    el.scrollIntoView({ block: 'nearest' });
  }

  function onFocusIn(e: FocusEvent) {
    const a = (e.target as HTMLElement).closest<HTMLElement>('article[data-message-id]');
    if (a) activeId = a.dataset.messageId ?? null;
  }

  const rovingId = $derived(activeId && items.some((m) => m.id === activeId) ? activeId : (items[items.length - 1]?.id ?? null));
</script>

<div class="wrap">
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="scroller" bind:this={scroller} onscroll={onScroll} onkeydown={onKey} onfocusin={onFocusIn} role="region" aria-label={label}>
    {#if top}{@render top()}{/if}
    {#if hasMore}
      <div class="older">
        <button class="btn btn-sm btn-quiet" onclick={loadOlder} disabled={loadingOlder}>
          {loadingOlder ? 'Loading earlier messages…' : 'Load earlier messages'}
        </button>
      </div>
    {/if}
    {#if !loaded}
      <p class="loading meta" aria-busy="true">Loading messages…</p>
    {:else if items.length === 0 && pending.length === 0}
      {#if empty}{@render empty()}{/if}
    {/if}
    <div class="items">
      {#each rows as row (row.m.id)}
        {#if row.day}
          <div class="day" role="separator" aria-label={row.day}><span>{row.day}</span></div>
        {/if}
        {#if row.showNew}
          <div class="new-divider" role="separator" aria-label="New messages since you were here"><span>New</span></div>
        {/if}
        <MessageRow
          message={row.m}
          continuation={row.continuation}
          highlight={row.m.id === highlightId}
          {inThread}
          tabindex={row.m.id === rovingId ? 0 : -1}
          seenRefs={row.seenRefs}
          onreply={onreply ? () => onreply(row.m) : undefined}
        />
      {/each}
      {#each pending as p (p.clientKey)}
        <PendingRow {p} onedit={oneditpending ? () => oneditpending(p) : undefined} />
      {/each}
    </div>
    <div class="end" aria-hidden="true"></div>
  </div>

  {#if newCount > 0 && !atBottom}
    <button class="jump" onclick={() => scrollToBottom(true)}>
      <Icon name="chevronDown" size={15} />{newCount} new {newCount === 1 ? 'message' : 'messages'}
    </button>
  {:else if !atBottom && items.length > 20}
    <button class="jump quiet" onclick={() => scrollToBottom(true)}><Icon name="chevronDown" size={15} />Jump to latest</button>
  {/if}
</div>

<style>
  .wrap {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .scroller {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    overscroll-behavior: contain;
    padding: 8px 0 16px;
  }
  .scroller:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  .items {
    max-width: calc(var(--measure) + 120px);
  }
  .older {
    display: flex;
    justify-content: center;
    padding: 8px;
  }
  .loading {
    padding: 24px;
  }
  .day {
    display: flex;
    justify-content: center;
    margin: 20px 0 6px;
    pointer-events: none;
  }
  .day span {
    padding: 3px 10px;
    border: 1px solid color-mix(in srgb, var(--line-strong) 80%, transparent);
    border-radius: var(--r-pill);
    background: var(--surface);
    font-size: 11px;
    font-weight: 500;
    color: var(--ink-secondary);
  }
  .new-divider {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 14px 16px 2px;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--ink);
  }
  .new-divider::before,
  .new-divider::after {
    content: '';
    flex: 1;
    height: 1px;
    background: color-mix(in srgb, var(--ink) 40%, transparent);
  }
  .end {
    height: 1px;
  }
  .jump {
    position: absolute;
    left: 50%;
    bottom: 12px;
    transform: translateX(-50%);
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 12px 0 9px;
    border: 0;
    border-radius: var(--r-pill);
    background: var(--accent);
    color: var(--accent-ink);
    font-size: 11px;
    font-weight: 500;
    letter-spacing: 0.02em;
    box-shadow: var(--shadow-pop);
    cursor: pointer;
    z-index: 6;
    animation: rise var(--t-slow) var(--ease);
  }
  .jump.quiet {
    background: color-mix(in srgb, var(--surface) 85%, transparent);
    backdrop-filter: blur(6px);
    color: var(--ink);
    border: 1px solid color-mix(in srgb, var(--line-strong) 60%, transparent);
  }
  @keyframes rise {
    from {
      opacity: 0;
      transform: translate(-50%, 6px);
    }
  }
  @media (pointer: coarse) {
    .jump {
      height: 44px;
    }
  }
</style>
