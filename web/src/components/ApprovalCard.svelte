<script lang="ts">
  // An exceptional permission request, inline where it was raised: the exact
  // action, target, scope and expiry. A stale decision reloads the recorded
  // permission; another tab may already have allowed the action.
  import { untrack } from 'svelte';
  import { Button, Card, Code, HStack, Icon, MetadataList, MetadataListItem, Text } from '@astryx-svelte/core';
  import Notice from './Notice.svelte';
  import { ChevronDown, ChevronRight, FileDiff } from '@lucide/svelte';
  import { app } from '../lib/state/app.svelte';
  import { details } from '../lib/state/details.svelte';
  import { api } from '../lib/api/endpoints';
  import { newer } from '../lib/state/data';
  import { ApiError, errorMessage } from '../lib/api/client';
  import { approvalStatusLabel, approvalVerb } from '../lib/util/labels';
  import { atTime, relative, shortSha } from '../lib/util/time';
  import StateIcon from './StateIcon.svelte';

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

<Card class="approval" role="region" aria-label="Permission request" maxWidth={620}>
  <div class="body">
    {#if !a}
      <Text as="p" type="supporting">Loading the request…</Text>
    {:else}
      <p class="kicker">
        <StateIcon
          shape={unconfirmed ? 'question' : status === 'pending' ? 'pause' : status === 'approved' || status === 'consumed' ? 'check-filled' : status === 'rejected' ? 'slash' : 'circle'}
          tone={unconfirmed || status === 'pending' ? 'attention' : status === 'approved' || status === 'consumed' ? 'success' : 'neutral'}
        />
        <span>{unconfirmed ? 'Current status not confirmed' : approvalStatusLabel(status)}</span>
        {#if a.decidedAt && status !== 'pending'}<Text type="supporting">· {app.actorName(a.decidedBy)} {atTime(a.decidedAt)}</Text>{/if}
      </p>
      <p class="summary">{a.action.summary}</p>
      {#if status !== 'pending'}
        <Button
          label={expanded ? 'Hide request and outcome' : 'View request and outcome'}
          variant="ghost"
          size="sm"
          aria-expanded={expanded}
          onclick={() => (expanded = !expanded)}
        >
          {#snippet icon()}<Icon icon={expanded ? ChevronDown : ChevronRight} size="sm" />{/snippet}
        </Button>
      {/if}
      {#if showDetail}
        <MetadataList label={{ position: 'start', width: 100 }}>
          {#if a.action.command}
            <MetadataListItem label="Exact action"><Code class="cmd">{a.action.command}</Code></MetadataListItem>
          {/if}
          {#if a.action.target}<MetadataListItem label="Target"><Code>{a.action.target}</Code></MetadataListItem>{/if}
          <MetadataListItem label="Scope">{a.scope || 'This one action only'}</MetadataListItem>
          {#if a.targetRev}<MetadataListItem label="Revision"><Code>{shortSha(a.targetRev)}</Code></MetadataListItem>{/if}
          {#if a.action.detail}<MetadataListItem label="Why">{a.action.detail}</MetadataListItem>{/if}
          <MetadataListItem label="Expires">{status === 'pending' ? relative(a.expiresAt, app.now) : atTime(a.expiresAt)}</MetadataListItem>
        </MetadataList>
        <HStack gap={2} wrap="wrap" align="center">
          {#if status === 'pending'}
            <Button
              label={busy === 'approve' ? 'Allowing…' : approvalVerb(a.action.kind)}
              variant="primary"
              size="sm"
              isDisabled={!!busy || refreshing || unconfirmed}
              onclick={() => decide('approve')}
            />
            <Button label={busy === 'reject' ? 'Rejecting…' : 'Reject'} size="sm" isDisabled={!!busy || refreshing || unconfirmed} onclick={() => decide('reject')} />
          {/if}
          <Button label="View diff" variant="ghost" size="sm" onclick={() => app.openPanel({ kind: 'job', id: a.jobId }, 'evidence')}>
            {#snippet icon()}<Icon icon={FileDiff} size="sm" />{/snippet}
          </Button>
        </HStack>
      {/if}
      {#if feedback}<Notice tone="danger" role="alert">{feedback}</Notice>{/if}
      {#if unconfirmed}
        <Button label={refreshing ? 'Checking…' : 'Check current request'} size="sm" isDisabled={refreshing} onclick={reloadApproval} />
      {/if}
      {#if status === 'pending'}
        <Text as="p" type="supporting">This allows only the action shown, on the revision shown. A reply in chat does not grant it.</Text>
      {/if}
    {/if}
  </div>
</Card>

<style>
  /* Card renders the root, so its classes are reached globally. */
  :global(.astryx-card.approval) {
    margin-top: var(--spacing-2);
  }
  .body {
    display: grid;
    gap: var(--spacing-2);
  }
  /* Standalone buttons keep their own width instead of stretching across the card. */
  .body > :global(.astryx-button) {
    justify-self: start;
  }
  .kicker {
    display: flex;
    align-items: center;
    gap: var(--spacing-1-5);
    flex-wrap: wrap;
    font-size: var(--text-supporting-size);
    font-weight: var(--font-weight-semibold);
  }
  .summary {
    font-weight: var(--font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .body :global(dd) {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .body :global(.cmd) {
    display: block;
    padding: var(--spacing-1-5) var(--spacing-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-inner);
    white-space: pre-wrap;
  }
</style>
