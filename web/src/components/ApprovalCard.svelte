<script lang="ts">
  // An exceptional permission request, inline where it was raised: the exact
  // action, target, scope and expiry. A stale decision reloads the recorded
  // permission; another tab may already have allowed the action.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { api } from '../lib/api/endpoints';
  import { newer } from '../lib/state/data';
  import { ApiError, errorMessage } from '../lib/api/client';
  import { approvalStatusLabel, approvalVerb } from '../lib/util/labels';
  import { atTime, relative, shortSha } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    approvalId: string;
  }
  let { approvalId }: Props = $props();

  $effect(() => {
    const id = approvalId;
    untrack(() => void details.ensureApproval(id));
  });

  const a = $derived(app.data.approvals[approvalId]);
  const expired = $derived(!!a && a.status === 'pending' && Date.parse(a.expiresAt) <= app.now);
  const status = $derived(a ? (expired ? 'expired' : a.status) : 'loading');
  let busy = $state<'approve' | 'reject' | null>(null);
  let error = $state('');
  let conflict = $state(false);
  let conflictedVersion = $state(0);
  let refreshFailed = $state(false);
  let refreshing = $state(false);
  let expanded = $state(false);
  const showDetail = $derived(status === 'pending' || expanded);
  const unconfirmed = $derived(refreshFailed && (!a || a.version <= conflictedVersion));
  const feedback = $derived.by(() => {
    if (!conflict) return error;
    if (unconfirmed) return 'Your decision was not applied. The current outcome could not be confirmed. Check the current request before deciding again.';
    switch (status) {
      case 'approved':
        return "This request was already allowed. Check the work for the action's outcome.";
      case 'consumed':
        return "This permission was already delivered to the machine. Check the work for the action's outcome.";
      case 'rejected':
        return 'This request was already rejected.';
      case 'expired':
        return 'This request expired. If the action is still needed, a new request will appear.';
      case 'cancelled':
        return 'This request was withdrawn.';
      default:
        return 'This request changed since it was shown. Review the current action before deciding.';
    }
  });

  async function reloadApproval() {
    refreshing = true;
    try {
      const next = await api.approval(approvalId);
      if (newer(app.data.approvals[next.id], next)) app.data.approvals[next.id] = next;
      refreshFailed = false;
    } catch {
      refreshFailed = true;
    } finally {
      conflict = true;
      refreshing = false;
    }
  }

  async function decide(decision: 'approve' | 'reject') {
    if (!a || busy || refreshing || unconfirmed) return;
    const request = a;
    busy = decision;
    error = '';
    conflict = false;
    try {
      const next = await api.decideApproval(request.id, { decision, version: request.version, note: '' });
      if (newer(app.data.approvals[next.id], next)) app.data.approvals[next.id] = next;
      app.announce(decision === 'approve' ? 'Allowed.' : 'Rejected.');
    } catch (err) {
      if (err instanceof ApiError && err.conflict) {
        conflictedVersion = request.version;
        await reloadApproval();
      } else {
        error = errorMessage(err);
      }
    } finally {
      busy = null;
    }
  }
</script>

<section class="approval panel-box" class:pending={status === 'pending'} aria-label="Permission request">
  {#if !a}
    <p class="meta">Loading the request…</p>
  {:else}
    <p class="kicker">
      <StateIcon
        shape={unconfirmed ? 'question' : status === 'pending' ? 'pause' : status === 'approved' || status === 'consumed' ? 'check-filled' : status === 'rejected' ? 'slash' : 'circle'}
        tone={unconfirmed || status === 'pending' ? 'attention' : status === 'approved' || status === 'consumed' ? 'success' : 'neutral'}
      />
      <span>{unconfirmed ? 'Current status not confirmed' : approvalStatusLabel(status)}</span>
      {#if a.decidedAt && status !== 'pending'}<span class="meta">· {app.actorName(a.decidedBy)} {atTime(a.decidedAt)}</span>{/if}
    </p>
    <p class="summary">{a.action.summary}</p>
    {#if status !== 'pending'}
      <button class="link-btn disclosure" aria-expanded={expanded} onclick={() => (expanded = !expanded)}>
        <Icon name={expanded ? 'chevronDown' : 'chevronRight'} size={14} />
        {expanded ? 'Hide request and outcome' : 'View request and outcome'}
      </button>
    {/if}
    {#if showDetail}
    <dl>
      {#if a.action.command}
        <div><dt>Exact action</dt><dd><code class="cmd">{a.action.command}</code></dd></div>
      {/if}
      {#if a.action.target}<div><dt>Target</dt><dd class="mono">{a.action.target}</dd></div>{/if}
      <div><dt>Scope</dt><dd>{a.scope || 'This one action only'}</dd></div>
      {#if a.targetRev}<div><dt>Revision</dt><dd class="mono">{shortSha(a.targetRev)}</dd></div>{/if}
      {#if a.action.detail}<div><dt>Why</dt><dd>{a.action.detail}</dd></div>{/if}
      <div>
        <dt>Expires</dt>
        <dd>{status === 'pending' ? relative(a.expiresAt, app.now) : atTime(a.expiresAt)}</dd>
      </div>
    </dl>
    <div class="actions">
      {#if status === 'pending'}
        <button class="btn btn-primary btn-sm" disabled={!!busy || refreshing || unconfirmed} onclick={() => decide('approve')}>
          {busy === 'approve' ? 'Allowing…' : approvalVerb(a.action.kind)}
        </button>
        <button class="btn btn-sm" disabled={!!busy || refreshing || unconfirmed} onclick={() => decide('reject')}>{busy === 'reject' ? 'Rejecting…' : 'Reject'}</button>
      {/if}
      <button class="btn btn-sm btn-quiet" onclick={() => app.openPanel({ kind: 'job', id: a.jobId }, 'evidence')}>
        <Icon name="file" size={15} />View diff
      </button>
    </div>
    {/if}
    {#if feedback}<p class="form-error" role="alert">{feedback}</p>{/if}
    {#if unconfirmed}<button class="btn btn-sm" disabled={refreshing} onclick={reloadApproval}>{refreshing ? 'Checking…' : 'Check current request'}</button>{/if}
    {#if status === 'pending'}
      <p class="meta">This allows only the action shown, on the revision shown. A reply in chat does not grant it.</p>
    {/if}
  {/if}
</section>

<style>
  .approval {
    margin-top: 8px;
    padding: 12px 14px;
    max-width: 620px;
    display: grid;
    gap: 8px;
  }
  .disclosure {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    justify-self: start;
    font-size: 13px;
  }
  .approval.pending {
    box-shadow: inset 3px 0 0 var(--attention-fill);
  }
  .kicker {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    font-size: 13px;
    font-weight: 650;
  }
  .summary {
    font-weight: 600;
  }
  dl {
    margin: 0;
    display: grid;
    gap: 4px;
  }
  dl > div {
    display: grid;
    grid-template-columns: 100px minmax(0, 1fr);
    gap: 10px;
    font-size: 14px;
  }
  dt {
    color: var(--ink-secondary);
    font-size: 13px;
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .cmd {
    display: block;
    padding: 6px 8px;
    border-radius: 6px;
    background: var(--surface-subtle);
    border: 1px solid var(--line);
    white-space: pre-wrap;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
</style>
