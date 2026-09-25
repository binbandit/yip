<script lang="ts">
  // An exceptional permission request, inline where it was raised: the exact
  // action, target, scope and expiry. Allow/Reject send the exact version; a
  // changed or expired request cannot run, and the card says so.
  import { untrack } from 'svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { api } from '../lib/api/endpoints';
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

  async function decide(decision: 'approve' | 'reject') {
    if (!a) return;
    busy = decision;
    error = '';
    try {
      const next = await api.decideApproval(a.id, { decision, version: a.version, note: '' });
      app.data.approvals[next.id] = next;
      app.announce(decision === 'approve' ? 'Allowed.' : 'Rejected.');
    } catch (err) {
      if (err instanceof ApiError && err.conflict) {
        error = 'This request changed or expired since it was shown. Nothing ran. Review the latest version below.';
        try {
          app.data.approvals[a.id] = await api.approval(a.id);
        } catch {
          /* keep what we have */
        }
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
        shape={status === 'pending' ? 'pause' : status === 'approved' || status === 'consumed' ? 'check-filled' : status === 'rejected' ? 'slash' : 'circle'}
        tone={status === 'pending' ? 'attention' : status === 'approved' || status === 'consumed' ? 'success' : 'neutral'}
      />
      <span>{approvalStatusLabel(status)}</span>
      {#if a.decidedAt && status !== 'pending'}<span class="meta">· {app.actorName(a.decidedBy)} {atTime(a.decidedAt)}</span>{/if}
    </p>
    <p class="summary">{a.action.summary}</p>
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
    {#if error}<p class="form-error" role="alert">{error}</p>{/if}
    <div class="actions">
      {#if status === 'pending'}
        <button class="btn btn-primary btn-sm" disabled={!!busy} onclick={() => decide('approve')}>
          {busy === 'approve' ? 'Allowing…' : approvalVerb(a.action.kind)}
        </button>
        <button class="btn btn-sm" disabled={!!busy} onclick={() => decide('reject')}>{busy === 'reject' ? 'Rejecting…' : 'Reject'}</button>
      {/if}
      <button class="btn btn-sm btn-quiet" onclick={() => app.openPanel({ kind: 'job', id: a.jobId }, 'evidence')}>
        <Icon name="file" size={15} />View diff
      </button>
    </div>
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
