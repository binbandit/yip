<script lang="ts">
  // Everything needed to inspect one piece of work: state and blockers in
  // plain words, evidence (diff, checks, files), review history, activity and
  // tool logs, and each attempt. Stop, Retry and Accept are explicit.
  import { untrack } from 'svelte';
  import { Button, Card, Code, CodeBlock, Collapsible, Heading, Icon, Link, MetadataList, MetadataListItem, Selector, Tab, TabList, Text } from '@astryx-svelte/core';
  import { Download, File, MessageSquare, RefreshCw } from '@lucide/svelte';
  import { app, receiptKey } from '../../lib/state/app.svelte';
  import { workspaceUrl } from '../../lib/workspace';
  import { details } from '../../lib/state/details.svelte';
  import { api, artifactUrl, fetchArtifactText } from '../../lib/api/endpoints';
  import { ApiError, errorMessage } from '../../lib/api/client';
  import type { Artifact, RunActivity } from '../../lib/api/types.gen';
  import { parseUnifiedDiff, type DiffFile } from '../../lib/util/diff';
  import { workResultKey } from '../../lib/util/reviews';
  import { isLiveJob } from '../../lib/state/data';
  import {
    billingLabel,
    deliveryReceipt,
    jobShape,
    jobStateLabel,
    jobTone,
    providerLabel,
    runShape,
    runStateLabel,
    runTone,
    waitingReasonLabel,
    workId,
  } from '../../lib/util/labels';
  import { atTime, bytes, clock, duration, fullTime, relative, shortSha } from '../../lib/util/time';
  import RightPanel, { type PanelMode } from '../RightPanel.svelte';
  import StateIcon from '../StateIcon.svelte';
  import Notice from '../Notice.svelte';
  import Avatar from '../Avatar.svelte';
  import DiffView from '../DiffView.svelte';
  import ReviewDetail from '../ReviewDetail.svelte';
  import PRFacts from '../PRFacts.svelte';
  import ConfirmDialog from '../ConfirmDialog.svelte';
  import MessageBody from '../MessageBody.svelte';

  interface Props {
    jobId: string;
    mode: PanelMode;
  }
  let { jobId, mode }: Props = $props();

  $effect(() => {
    const t = app.data.touched.jobs[jobId] ?? 0;
    untrack(() => details.ensureJob(jobId, t));
  });

  const entry = $derived(details.jobs[jobId]);
  const d = $derived(entry?.data);
  const job = $derived(app.data.jobs[jobId] ?? d?.job);
  const runs = $derived(d ? [...d.runs].sort((a, b) => b.attempt - a.attempt) : []);
  const unknownRun = $derived(runs.find((r) => r.state === 'unknown'));
  // A stop is only confirmed once the machine reports the attempt ended.
  const stoppingRun = $derived(runs.find((r) => r.state === 'stopping'));
  // Why the work is in its state: the state's second line, not a notice of its own.
  const stateDetail = $derived(job && (job.state === 'waiting' || job.state === 'failed' || job.state === 'review_ready') ? (job.stateDetail ?? '') : '');
  // The hub often phrases the state as "Waiting on <missing evidence>"; list only what that line doesn't already say.
  const missing = $derived((d?.missing ?? []).filter((m) => !stateDetail.includes(m)));
  const project = $derived(job?.projectId ? app.data.projects[job.projectId] : undefined);
  const repo = $derived(project?.repos.find((r) => r.id === job?.repoId));
  // Waits a person fixes outside the work itself get a link to where to fix them.
  const setupLink = $derived.by(() => {
    if (job?.state !== 'waiting') return null;
    switch (job.waitingReason) {
      case 'provider_sign_in':
      case 'provider_allowance':
      case 'machine_availability':
        return { href: '/machines', label: 'View machines and sign-ins' };
      case 'engineer_capacity':
        return { href: `/engineers/${job.ownerId}`, label: `View ${app.engineerName(job.ownerId)}'s setup` };
    }
    return null;
  });
  const room = $derived(job ? app.data.rooms[job.source.roomId] : undefined);
  const live = $derived(!!job && isLiveJob(job));
  const canRetry = $derived(
    !!job &&
      !stoppingRun &&
      (job.state === 'failed' || job.state === 'cancelled' || (job.state === 'waiting' && (job.waitingReason === 'recovery' || job.waitingReason === 'stalled')) || !!unknownRun),
  );
  const resultKey = $derived(job ? workResultKey(job, Object.values(app.data.artifacts)) : undefined);
  const canAccept = $derived(!!job && job.requiresHumanReview && job.state === 'review_ready' && !!resultKey);

  const TABS = ['evidence', 'review', 'activity', 'runs'] as const;
  type Tab = (typeof TABS)[number];
  const tab: Tab = $derived((TABS as readonly string[]).includes(app.loc.tab ?? '') ? (app.loc.tab as Tab) : 'evidence');

  // ---- evidence: diff per revision ----
  // Revision records (file and line counts) drive the picker and stats; the
  // diff artifact is still parsed to render hunks.
  interface RevOption {
    head: string;
    artifactId: string;
    at: string;
    stats?: { files: number; ins: number; del: number; summary: string; branch: string };
  }
  const revOptions: RevOption[] = $derived.by(() => {
    if (!d) return [];
    const byId = new Map(d.artifacts.map((a) => [a.id, a]));
    if (d.revisions.length) {
      return [...d.revisions].reverse().map((r) => ({
        head: r.head,
        artifactId: r.diffArtifactId,
        at: byId.get(r.diffArtifactId)?.createdAt ?? '',
        stats: { files: r.filesChanged, ins: r.insertions, del: r.deletions, summary: r.summary, branch: r.branch },
      }));
    }
    return d.artifacts
      .filter((a) => a.kind === 'diff')
      .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
      .map((a) => ({ head: a.revision ?? '', artifactId: a.id, at: a.createdAt }));
  });
  // Picker labels carry each revision's file and line counts.
  const revChoices = $derived(
    revOptions.map((o) => ({
      value: o.artifactId,
      label: `${shortSha(o.head)}${o.head === job?.revision?.head ? ' (current)' : ''}${o.stats ? ` · ${o.stats.files} files +${o.stats.ins} −${o.stats.del}` : o.at ? ` · ${clock(o.at)}` : ''}`,
    })),
  );
  let chosenRev = $state<string | null>(null);
  // A finding's location opens the diff of the revision that was reviewed.
  $effect(() => {
    const f = app.diffFocus;
    if (!f || f.jobId !== jobId || !f.head) return;
    const o = revOptions.find((x) => x.head === f.head);
    if (o) untrack(() => (chosenRev = o.artifactId));
  });
  const rev = $derived(revOptions.find((o) => o.artifactId === chosenRev) ?? revOptions.find((o) => o.head === job?.revision?.head) ?? revOptions[0]);
  const diff = $derived(rev && d ? (d.artifacts.find((a) => a.id === rev.artifactId) ?? { id: rev.artifactId, revision: rev.head }) : undefined);
  let diffFiles = $state<DiffFile[] | null>(null);
  let diffError = $state('');
  let diffTruncated = $state(false);
  let loadedDiffId = '';
  $effect(() => {
    const a = diff;
    if (!a || tab !== 'evidence' || a.id === loadedDiffId) return;
    untrack(() => {
      loadedDiffId = a.id;
      diffFiles = null;
      diffError = '';
      fetchArtifactText(a.id)
        .then(({ text, truncated }) => {
          diffFiles = parseUnifiedDiff(text);
          diffTruncated = truncated;
        })
        .catch((e) => (diffError = e.message));
    });
  });

  // ---- check logs ----
  let openLog = $state<string | null>(null);
  let logText = $state<Record<string, string>>({});
  async function toggleLog(artifactId: string) {
    if (openLog === artifactId) {
      openLog = null;
      return;
    }
    openLog = artifactId;
    if (!logText[artifactId]) {
      try {
        logText[artifactId] = (await fetchArtifactText(artifactId, 200_000)).text || '(empty log)';
      } catch (e) {
        logText[artifactId] = (e as Error).message;
      }
    }
  }

  // ---- run tool logs ----
  let runLogs = $state<Record<string, RunActivity[] | string>>({});
  async function loadRunLog(runId: string) {
    if (runLogs[runId]) return;
    runLogs[runId] = 'loading';
    try {
      runLogs[runId] = await api.runActivity(jobId, runId);
    } catch (e) {
      runLogs[runId] = errorMessage(e);
    }
  }

  // ---- actions ----
  let confirmStop = $state(false);
  let busy = $state('');
  let actionError = $state('');

  async function stop() {
    const j = await api.cancelJob(jobId, { reason: 'Stopped by the owner', includeChildren: true });
    app.data.jobs[j.id] = j;
    app.announce(`${j.title} stopped.`);
    void details.refreshJob(jobId);
  }

  async function retry() {
    busy = 'retry';
    actionError = '';
    try {
      const j = await api.retryJob(jobId, { fromCheckpoint: true, reason: 'Retried by the owner' });
      app.data.jobs[j.id] = j;
      app.announce('A new attempt is queued.');
      void details.refreshJob(jobId);
    } catch (err) {
      actionError = errorMessage(err);
    } finally {
      busy = '';
    }
  }

  async function accept() {
    if (!job || !resultKey) return;
    busy = 'accept';
    actionError = '';
    try {
      const j = await api.acceptJob(jobId, { revision: resultKey, version: job.version, note: '' });
      app.data.jobs[j.id] = j;
      app.announce(`Accepted ${job.kind === 'code' ? 'revision' : 'document'} ${shortSha(resultKey)}.`);
      void details.refreshJob(jobId);
    } catch (err) {
      if (err instanceof ApiError && err.conflict) {
        actionError = 'This work changed since you opened it. Review the latest revision before accepting.';
        void details.refreshJob(jobId);
      } else actionError = errorMessage(err);
    } finally {
      busy = '';
    }
  }

  function steer() {
    if (!job) return;
    const key = receiptKey(job.source.roomId, job.source.threadId);
    app.steer[key] = job.id;
    const target = job.source.threadId
      ? `/rooms/${job.source.roomId}?panel=thread%3A${job.source.threadId}`
      : `/rooms/${job.source.roomId}`;
    app.navigate(target);
    queueMicrotask(() => document.querySelector<HTMLTextAreaElement>('.room-composer textarea, .panel textarea')?.focus());
  }

  // Arrow keys, Home and End move the selection with the focus, as they always
  // have here, so the strip's own focus-only roving is skipped.
  function tabKey(e: KeyboardEvent) {
    const i = TABS.indexOf(tab);
    let n = i;
    if (e.key === 'ArrowRight') n = (i + 1) % TABS.length;
    else if (e.key === 'ArrowLeft') n = (i - 1 + TABS.length) % TABS.length;
    else if (e.key === 'Home') n = 0;
    else if (e.key === 'End') n = TABS.length - 1;
    else return;
    e.preventDefault();
    app.setTab(TABS[n]);
    queueMicrotask(() => document.getElementById(`jobtab-${TABS[n]}`)?.focus());
  }

  const tabLabel: Record<Tab, string> = { evidence: 'Evidence', review: 'Review', activity: 'Activity', runs: 'Runs' };
  const counts = $derived({
    evidence: d ? d.checks.length + d.artifacts.length : 0,
    review: d ? d.reviews.length : 0,
    activity: d ? d.activity.length : 0,
    runs: d ? d.runs.length : 0,
  });
  const artifactsByKind = (list: Artifact[]) => list.filter((a) => a.kind !== 'diff');
</script>

<RightPanel title={job?.title ?? 'Work'} {mode} wide onclose={() => app.closePanel()}>
  {#snippet subtitle()}
    {#if job}{jobStateLabel(job)} · {app.engineerName(job.ownerId)}{#if project}{' · '}{project.name}{/if}{/if}
  {/snippet}

  {#if !job}
    <div class="pad">
      {#if entry?.error}
        <Notice tone="danger" role="alert">{entry.missing ? "This work doesn't exist or isn't visible to you." : entry.error}</Notice>
      {:else}
        <Text as="p" type="supporting">Loading the work…</Text>
      {/if}
    </div>
  {:else}
    <div class="pad head">
      <div class="status">
        <p class="state tone-{jobTone(job.state)}">
          <StateIcon shape={jobShape(job.state)} tone={jobTone(job.state)} size={16} live={job.state === 'running'} />
          <strong>{job.state === 'cancelled' && stoppingRun ? 'Stopping' : jobStateLabel(job)}</strong>
          {#if job.state === 'waiting'}<span class="reason">· {waitingReasonLabel(job.waitingReason)}</span>{/if}
        </p>
        {#if stoppingRun}
          <p class="why">Waiting for {app.nodeName(stoppingRun.nodeId) || 'its machine'} to confirm the attempt has stopped.</p>
        {/if}
        {#if stateDetail}
          <p class="why">{stateDetail}{#if setupLink}{' '}{@render setupAction()}{/if}</p>
        {:else if setupLink}
          <p class="why">{@render setupAction()}</p>
        {/if}
      </div>
      {#if unknownRun}
        <Notice
          tone="warning"
          title="Last heard from {app.nodeName(unknownRun.nodeId) || 'its machine'} {atTime(
            unknownRun.heartbeatAt ?? unknownRun.lastActivityAt ?? unknownRun.createdAt,
          )}. The run's outcome is not yet confirmed{unknownRun.lastActivity ? ` — last confirmed: ${unknownRun.lastActivity.toLowerCase()}` : ''}."
          description="Check before retrying anything that pushes or publishes."
        />
      {/if}
      {#if missing.length}
        <Notice tone="warning">Still needs {missing.join('; ')}.</Notice>
      {/if}

      <div class="facts">
        <MetadataList label={{ position: 'start', width: 110 }}>
          <MetadataListItem label="Owner">
            <Link color="primary" onclick={() => app.openPanel({ kind: 'engineer', id: job!.ownerId })}>
              <span class="person"><Avatar actor={{ kind: 'engineer', id: job.ownerId }} size={20} />{app.engineerName(job.ownerId)}</span>
            </Link>
          </MetadataListItem>
          {#if job.reviewerIds.length}
            <MetadataListItem label="Reviewers">
              <span class="people">
                {#each job.reviewerIds as r (r)}<span class="person"><Avatar actor={{ kind: 'engineer', id: r }} size={20} />{app.engineerName(r)}</span>{/each}
              </span>
            </MetadataListItem>
          {/if}
          {#if project}
            <MetadataListItem label="Project">
              <Link hasUnderline href={workspaceUrl(`/projects/${project.id}`)}>{project.name}</Link>{#if repo}{' '}<Text type="supporting">· <Code size="inherit">{repo.forgeRepo || repo.name}</Code></Text>{/if}
            </MetadataListItem>
          {/if}
          <MetadataListItem label="Machine">
            {job.nodeId ? app.nodeName(job.nodeId) || 'A paired machine' : 'Not assigned yet'}
          </MetadataListItem>
          {#if job.revision?.head || job.revision?.branch}
            <MetadataListItem label="Revision">
              <Code>{shortSha(job.revision.head) || '—'}</Code>{#if job.revision.branch}{' '}<Text type="supporting">on {job.revision.branch}</Text>{/if}
            </MetadataListItem>
          {/if}
          <MetadataListItem label="Last confirmed">
            {job.lastActivity || 'Nothing yet'}{#if job.lastActivityAt}{' '}<Text type="supporting"
                ><span title={fullTime(job.lastActivityAt)}>· {relative(job.lastActivityAt, app.now)}</span></Text
              >{/if}
          </MetadataListItem>
          {#if job.followsId}
            <MetadataListItem label="Follows up">
              <Link onclick={() => app.openPanel({ kind: 'job', id: job!.followsId! })} hasUnderline
                >{app.data.jobs[job.followsId]?.title ?? 'the earlier work'}</Link
              >
            </MetadataListItem>
          {/if}
          <MetadataListItem label="Work ID">
            <span title={job.id}><Code>#{workId(job.id)}</Code></span>
          </MetadataListItem>
          <MetadataListItem label="From">
            <Link hasUnderline href={workspaceUrl(`/rooms/${job.source.roomId}?msg=${job.source.messageId ?? ''}${job.source.threadId ? `&panel=thread%3A${job.source.threadId}` : ''}`)}
              >{room?.name ?? 'the source conversation'}</Link
            >
            {#if job.requiresHumanReview}{' '}<Text type="supporting">· needs your review before it completes</Text>{/if}
          </MetadataListItem>
        </MetadataList>
      </div>

      {#if actionError}<Notice tone="danger" role="alert">{actionError}</Notice>{/if}
      <div class="actions">
        {#if canAccept}
          <Button
            variant="primary"
            size="sm"
            label="Accept {job.kind === 'code' ? 'revision' : 'document'} {shortSha(resultKey)}"
            isLoading={busy === 'accept'}
            onclick={accept}
          >
            {#snippet icon()}<Icon icon="check" size="sm" />{/snippet}
          </Button>
        {/if}
        {#if live}
          <Button size="sm" label="Add to this work" onclick={steer}>
            {#snippet icon()}<Icon icon={MessageSquare} size="sm" />{/snippet}
          </Button>
          <Button size="sm" variant="destructive" label="Stop" onclick={() => (confirmStop = true)}>
            {#snippet icon()}<Icon icon="stop" size="sm" />{/snippet}
          </Button>
        {/if}
        {#if canRetry}
          <Button
            size="sm"
            label={busy === 'retry' ? (job.state === 'cancelled' ? 'Resuming…' : 'Retrying…') : job.state === 'cancelled' ? 'Resume' : 'Retry'}
            isDisabled={busy === 'retry'}
            onclick={retry}
          >
            {#snippet icon()}<Icon icon={RefreshCw} size="sm" />{/snippet}
          </Button>
        {/if}
      </div>
    </div>

    <!-- The tab bar keeps its height however long the evidence below it is. -->
    <div class="tabbar">
      <TabList role="tablist" aria-label="Work details" value={tab} onChange={(v) => app.setTab(v)} onkeydown={tabKey} hasDivider>
        {#each TABS as t (t)}
          {#snippet count()}<span class="n">{counts[t]}</span>{/snippet}
          <Tab value={t} label={tabLabel[t]} id="jobtab-{t}" panelId="jobpanel-{t}" endContent={counts[t] ? count : undefined} />
        {/each}
      </TabList>
    </div>

    <div class="pad tabpanel" role="tabpanel" id="jobpanel-{tab}" aria-labelledby="jobtab-{tab}" tabindex="-1">
      {#if !d}
        <Text as="p" type="supporting">Loading evidence…</Text>
      {:else if tab === 'evidence'}
        {#if job.summary}
          <section class="block">
            <Heading level={3}>Summary</Heading>
            <MessageBody message={{ body: job.summary, mentions: [] }} />
          </section>
        {/if}
        {#if job.acceptance.length}
          <section class="block">
            <Heading level={3}>Done when</Heading>
            <ul class="plain">{#each job.acceptance as a (a)}<li>{a}</li>{/each}</ul>
          </section>
        {/if}

        <section class="block">
          <div class="block-head">
            <Heading level={3}>Changes</Heading>
            {#if revOptions.length > 1}
              <div class="rev-pick">
                <Selector label="Revision" isLabelHidden size="sm" value={rev?.artifactId} options={revChoices} onChange={(v: string) => (chosenRev = v)} />
              </div>
            {/if}
          </div>
          {#if rev?.stats}
            <p class="rev-stats">
              <Code size="inherit">{shortSha(rev.head)}</Code>
              · {rev.stats.files} {rev.stats.files === 1 ? 'file' : 'files'} <span class="add">+{rev.stats.ins}</span> <span class="del">−{rev.stats.del}</span>
              {#if rev.stats.summary}· {rev.stats.summary}{/if}
            </p>
          {/if}
          {#if !diff}
            <Text as="p" type="supporting">No diff recorded{job.kind === 'code' ? ' yet' : ''}.</Text>
          {:else if diffError}
            <Notice tone="danger" role="alert">{diffError}</Notice>
          {:else if !diffFiles}
            <Text as="p" type="supporting">Loading the diff…</Text>
          {:else}
            {#if diff.revision !== job.revision?.head && job.revision?.head}
              <Notice tone="warning" title="This diff is for an earlier revision ({shortSha(diff.revision)})." />
            {/if}
            <DiffView files={diffFiles} focus={app.diffFocus?.jobId === jobId ? app.diffFocus : null} />
            {#if diffTruncated}
              <Text as="p" type="supporting"
                >The diff is long; <Link href={artifactUrl(diff.id)} target="_blank" rel="noopener" type="inherit" hasUnderline>open the full file</Link>.</Text
              >
            {/if}
          {/if}
        </section>

        <section class="block">
          <Heading level={3}>Checks</Heading>
          {#if d.checks.length === 0}
            <Text as="p" type="supporting">No checks recorded.{job.kind === 'code' ? ' A result without checks is unverified.' : ''}</Text>
          {:else}
            <ul class="checks">
              {#each [...d.checks].reverse() as c (c.id)}
                <li class="check-row" class:stale={!!job.revision?.head && c.revision !== job.revision.head}>
                  <div class="check-main">
                    <StateIcon shape={c.passed ? 'check-filled' : 'triangle'} tone={c.passed ? 'success' : 'danger'} />
                    <div class="check-text">
                      <p><Code>{c.command}</Code></p>
                      <Text as="p" type="supporting">
                        <span class={c.passed ? 'tone-success' : 'tone-danger'}>{c.passed ? 'Passed' : 'Failed'}</span> · exit {c.exitCode} · on
                        <Code size="inherit">{shortSha(c.revision)}</Code>{c.revision !== job.revision?.head && job.revision?.head ? ' (earlier revision)' : ''}
                        · {duration(c.durationMs)} · {app.nodeName(c.nodeId) || 'machine'} · {atTime(c.createdAt)}
                      </Text>
                      {#if c.summary}<CodeBlock code={c.summary} size="sm" width="100%" maxHeight={280} isWrapped />{/if}
                    </div>
                  </div>
                  {#if c.logArtifactId}
                    <Button
                      variant="ghost"
                      size="sm"
                      label={openLog === c.logArtifactId ? 'Hide log' : 'Show log'}
                      aria-expanded={openLog === c.logArtifactId}
                      onclick={() => toggleLog(c.logArtifactId!)}
                    />
                  {/if}
                  {#if openLog === c.logArtifactId && c.logArtifactId}
                    <div class="log"><CodeBlock code={logText[c.logArtifactId] ?? 'Loading…'} size="sm" width="100%" maxHeight={280} isWrapped /></div>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        {#if artifactsByKind(d.artifacts).length}
          <section class="block">
            <Heading level={3}>Files</Heading>
            <ul class="plain files">
              {#each artifactsByKind(d.artifacts) as a (a.id)}
                <li>
                  <Icon icon={File} size="sm" color="secondary" />
                  <Link hasUnderline href={artifactUrl(a.id)} target="_blank" rel="noopener">{a.name}</Link>
                  <Text type="supporting">{a.kind} · {bytes(a.size)}{a.revision ? ` · ${shortSha(a.revision)}` : ''}</Text>
                  <Link href={artifactUrl(a.id, true)} download label="Download {a.name}" color="secondary">
                    <Icon icon={Download} size="sm" />
                  </Link>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.decisions.length}
          <section class="block">
            <Heading level={3}>Decisions recorded</Heading>
            <ul class="plain">
              {#each d.decisions as dc (dc.id)}
                <li>
                  <Link onclick={() => app.openPanel({ kind: 'decision', id: dc.id })} hasUnderline>{dc.title}</Link>
                  <Text type="supporting">· {dc.status}</Text>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.inputs.length}
          <section class="block">
            <Heading level={3}>Your updates</Heading>
            <ul class="plain">
              {#each d.inputs as i (i.id)}
                {@const cur = app.data.inputs[i.id] ?? i}
                <li>
                  <p>“{cur.body}”</p>
                  <Text as="p" type="supporting">{deliveryReceipt(cur.delivery, app.engineerName(job.ownerId))} · {atTime(cur.createdAt)}</Text>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.questions.length || d.approvals.length}
          <section class="block">
            <Heading level={3}>Questions and permissions</Heading>
            <ul class="plain">
              {#each d.questions as q (q.id)}
                <li>
                  <p>{q.missingFact}</p>
                  <Text as="p" type="supporting">
                    {q.status === 'open' ? 'Asked' : q.status === 'answered' ? 'Answered' : 'No longer needed'} · {app.engineerName(q.askerId)}
                    {#if q.continuingWith}{' · '}meanwhile: {q.continuingWith}{/if}
                    · <Link href={workspaceUrl(`/rooms/${q.source.roomId}?msg=${q.messageId}`)} type="inherit" hasUnderline>open in conversation</Link>
                  </Text>
                </li>
              {/each}
              {#each d.approvals as a (a.id)}
                <li>
                  <p>{a.action.summary}</p>
                  <Text as="p" type="supporting"
                    >{a.status} · <Link href={workspaceUrl(`/rooms/${a.source.roomId}${a.source.messageId ? `?msg=${a.source.messageId}` : ''}`)} type="inherit" hasUnderline
                      >open in conversation</Link
                    ></Text
                  >
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if d.followUps?.length}
          <section class="block">
            <Heading level={3}>Follow-ups</Heading>
            <ul class="plain">
              {#each d.followUps as f (f.id)}
                <li>
                  <Link onclick={() => app.openPanel({ kind: 'job', id: f.id })} hasUnderline>{f.title}</Link>
                  <Text type="supporting">· {jobStateLabel(f)} · {app.engineerName(f.ownerId)}</Text>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
        {#if d.children.filter((c) => c.kind !== 'review').length}
          <section class="block">
            <Heading level={3}>Related work</Heading>
            <ul class="plain">
              {#each d.children.filter((c) => c.kind !== 'review') as c (c.id)}
                <li>
                  <Link onclick={() => app.openPanel({ kind: 'job', id: c.id })} hasUnderline>{c.title}</Link>
                  <Text type="supporting">· {jobStateLabel(c)} · {app.engineerName(c.ownerId)}</Text>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {:else if tab === 'review'}
        {#if d.reviews.length === 0 && d.pullRequests.length === 0}
          <Text as="p" type="supporting"
            >{job.requiresPeerReview ? 'No review has been requested yet. The owner asks a colleague when the work is ready.' : 'No review on this work.'}</Text
          >
        {/if}
        {#each d.reviews as r (r.id)}
          <section class="block"><ReviewDetail review={app.data.reviews[r.id] ?? r} currentHead={resultKey} /></section>
        {/each}
        {#each d.pullRequests as pr (pr.id)}
          <section class="block">
            <Heading level={3}>Pull request</Heading>
            <PRFacts pr={app.data.prs[pr.id] ?? pr} reviews={d.reviews} />
          </section>
        {/each}
      {:else if tab === 'activity'}
        {#if d.activity.length === 0}
          <Text as="p" type="supporting">No activity recorded yet.</Text>
        {:else}
          <ol class="timeline">
            {#each d.activity as a, i (i)}
              <li>
                <time datetime={a.at} title={fullTime(a.at)}>{clock(a.at)}</time>
                <span>{a.text}</span>
              </li>
            {/each}
          </ol>
        {/if}
        {#if runs.length}
          <div class="sub-h"><Heading level={3}>Tool logs</Heading></div>
          <div class="runlogs">
            {#each runs as r (r.id)}
              <Card padding={0}>
                <!-- Each attempt's log loads the first time it is opened. -->
                <Collapsible
                  trigger="Attempt {r.attempt} · {app.engineerName(r.engineerId)} · {runStateLabel(r.state)}"
                  defaultIsOpen={false}
                  onOpenChange={(open) => open && loadRunLog(r.id)}
                >
                  {#if runLogs[r.id] === 'loading' || !runLogs[r.id]}
                    <Text as="p" type="supporting">Loading…</Text>
                  {:else if typeof runLogs[r.id] === 'string'}
                    <Notice tone="danger" role="alert">{runLogs[r.id] as string}</Notice>
                  {:else}
                    {@const acts = runLogs[r.id] as RunActivity[]}
                    {#if acts.length === 0}<Text as="p" type="supporting">Nothing recorded.</Text>{/if}
                    <ol class="tools">
                      {#each acts as a (a.seq)}
                        <li class="k-{a.kind}">
                          <time datetime={a.at}>{clock(a.at)}</time>
                          <span class="tool-text">{a.text}</span>
                          {#if a.tool && !a.tool.startsWith('mcp__')}<Code size="inherit" color="secondary">{a.tool}</Code>{/if}
                        </li>
                      {/each}
                    </ol>
                  {/if}
                </Collapsible>
              </Card>
            {/each}
          </div>
        {/if}
      {:else if tab === 'runs'}
        {#if runs.length === 0}
          <Text as="p" type="supporting">No attempts yet.{job.state === 'queued' || job.state === 'waiting' ? ` ${job.stateDetail ?? ''}` : ''}</Text>
        {/if}
        <ul class="runs">
          {#each runs as r (r.id)}
            {@const late = (d.quarantined ?? []).filter((q) => q.runId === r.id)}
            <li class="run">
              <p class="run-head tone-{runTone(r.state)}">
                <StateIcon shape={runShape(r.state)} tone={runTone(r.state)} live={r.state === 'running'} />
                <strong>Attempt {r.attempt}</strong> · {runStateLabel(r.state)}
              </p>
              {#if r.state === 'unknown'}
                <Notice
                  tone="warning"
                  title="The outcome is not confirmed."
                  description="The machine stopped reporting before the attempt finished; it may or may not have made changes."
                />
              {/if}
              <div class="facts">
                <MetadataList label={{ position: 'start', width: 110 }}>
                  <MetadataListItem label="Engineer">{app.engineerName(r.engineerId)}</MetadataListItem>
                  <MetadataListItem label="Machine">{app.nodeName(r.nodeId) || 'Not assigned'}</MetadataListItem>
                  <MetadataListItem label="Provider">
                    {providerLabel(r.provider)}{r.model ? ` · ${r.model}` : ''} · {billingLabel(r.usage?.billing)}
                    {#if r.usage?.inputTokens != null}{' '}<Text type="supporting">· {r.usage.inputTokens} in / {r.usage.outputTokens ?? 0} out tokens</Text>{/if}
                  </MetadataListItem>
                  <MetadataListItem label="Mode">{r.mode === 'edit' ? 'Can edit the workspace' : r.mode === 'readonly' ? 'Read-only' : 'Conversation'}</MetadataListItem>
                  {#if r.startedAt}<MetadataListItem label="Started">{atTime(r.startedAt)}</MetadataListItem>{/if}
                  {#if r.endedAt}
                    <MetadataListItem label="Ended">{atTime(r.endedAt)}{r.terminalReason && r.terminalReason !== r.state ? ` · ${r.terminalReason}` : ''}</MetadataListItem>
                  {/if}
                  {#if r.lastActivity}
                    <MetadataListItem label="Last confirmed">{r.lastActivity}{r.lastActivityAt ? ` · ${relative(r.lastActivityAt, app.now)}` : ''}</MetadataListItem>
                  {/if}
                  {#if r.resultRev}
                    <MetadataListItem label="Result"><Code>{shortSha(r.resultRev)}{r.branch ? ` on ${r.branch}` : ''}</Code></MetadataListItem>
                  {/if}
                </MetadataList>
              </div>
              {#if late.length}
                <Collapsible trigger="{late.length} late {late.length === 1 ? 'report' : 'reports'} kept for diagnosis" defaultIsOpen={false}>
                  <div class="late">
                    <Text as="p" type="supporting">
                      {app.nodeName(late[0].nodeId) || 'The machine'} sent these after this attempt had lost its lease. They didn't change the work.
                    </Text>
                    <ul class="plain">
                      {#each late as q (q.id)}
                        <li>
                          <Text type="supporting"
                            >{clock(q.receivedAt)} · {q.kind === 'tool_call' ? 'tool call' : q.kind === 'terminal' ? 'final report' : 'event'} ·</Text
                          >
                          <Code>{q.summary}</Code>
                        </li>
                      {/each}
                    </ul>
                  </div>
                </Collapsible>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>
  {/if}
</RightPanel>

{#snippet setupAction()}
  {#if setupLink}<Link href={workspaceUrl(setupLink.href)} type="inherit" hasUnderline>{setupLink.label}</Link>{/if}
{/snippet}

{#if confirmStop && job}
  <ConfirmDialog
    title="Stop this work?"
    body="Stopping ends {job.title} and any work it started. Changes already made stay on the machine; nothing is deleted. You can retry the work later."
    confirmLabel="Stop the work"
    danger
    onconfirm={stop}
    onclose={() => (confirmStop = false)}
  />
{/if}

<style>
  .pad {
    padding: var(--spacing-3) var(--spacing-4);
  }
  .head {
    display: grid;
    gap: var(--spacing-3);
  }
  .state {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
  }
  .state strong {
    font-weight: var(--font-weight-semibold);
  }
  .reason {
    color: var(--color-text-secondary);
  }
  .status {
    display: grid;
    gap: var(--spacing-0-5);
  }
  /* The second line starts under the state's words, clear of its shape. */
  .why {
    padding-inline-start: calc(16px + var(--spacing-2));
  }
  .facts {
    container-type: inline-size;
  }
  .person {
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-1-5);
  }
  .people {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-1) var(--spacing-3);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-2);
  }
  .tabbar {
    flex: none;
  }
  .tabbar :global(.astryx-tab-list) {
    padding-inline: var(--spacing-3);
  }
  .n {
    color: var(--color-text-secondary);
    font-size: var(--font-size-sm);
    font-variant-numeric: tabular-nums;
  }
  .tabpanel {
    padding-top: var(--spacing-4);
  }
  .tabpanel:focus-visible {
    outline: none;
  }
  .block {
    display: grid;
    gap: var(--spacing-2);
    margin-bottom: var(--spacing-5);
  }
  .block-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-2);
  }
  /* Section titles in a drawer sit below its 17px title. */
  .tabpanel :global(h3.astryx-heading) {
    font-size: var(--text-heading-4-size);
    line-height: var(--text-heading-4-leading);
  }
  .sub-h {
    margin: var(--spacing-5) 0 var(--spacing-2);
  }
  .rev-stats {
    font-size: var(--font-size-sm);
    color: var(--color-text-secondary);
  }
  .add {
    color: var(--color-success);
    font-weight: var(--font-weight-semibold);
  }
  .del {
    color: var(--color-error);
    font-weight: var(--font-weight-semibold);
  }
  .plain {
    display: grid;
    gap: var(--spacing-2);
  }
  .files li {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    flex-wrap: wrap;
  }
  .checks {
    display: grid;
    gap: var(--spacing-2);
  }
  .check-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: start;
    gap: var(--spacing-1-5) var(--spacing-3);
    padding: var(--spacing-2) var(--spacing-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-container);
  }
  .check-row.stale {
    background: var(--color-background-muted);
  }
  .check-main {
    display: flex;
    gap: var(--spacing-2);
    min-width: 0;
  }
  .check-main > :global(svg) {
    margin-top: 3px;
  }
  .check-text {
    display: grid;
    gap: var(--spacing-1);
    min-width: 0;
  }
  .log {
    grid-column: 1 / -1;
    min-width: 0;
  }
  .timeline,
  .tools {
    display: grid;
    gap: var(--spacing-1);
  }
  .timeline li,
  .tools li {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) auto;
    align-items: baseline;
    gap: var(--spacing-2);
  }
  time {
    color: var(--color-text-secondary);
    font-size: var(--font-size-sm);
    font-variant-numeric: tabular-nums;
  }
  .runlogs {
    display: grid;
    gap: var(--spacing-2);
  }
  /*
   * The whole card header toggles the attempt's log, set as a row rather than
   * a heading (it sits under "Tool logs"). The Card clips its edges, so the
   * keyboard ring is drawn inside the header.
   */
  .runlogs :global(.astryx-collapsible-trigger) {
    padding: var(--spacing-2) var(--spacing-3);
    font-size: var(--font-size-base);
    font-weight: var(--font-weight-medium);
    outline-offset: calc(-1 * var(--focus-outline-width));
  }
  /* A diagnostic aside, not a section: quiet like the facts above it. */
  .run :global(.astryx-collapsible-trigger) {
    font-size: var(--font-size-sm);
    font-weight: var(--font-weight-normal);
    color: var(--color-text-secondary);
  }
  .runlogs :global(.astryx-collapsible-content) {
    padding: 0 var(--spacing-3) var(--spacing-3);
  }
  .tools .tool-text {
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }
  .tools li.k-error .tool-text,
  .tools li.k-warning .tool-text {
    color: var(--color-error);
    font-weight: var(--font-weight-medium);
  }
  .tools li.k-status .tool-text,
  .tools li.k-started .tool-text {
    color: var(--color-text-primary);
  }
  .runs {
    display: grid;
    gap: var(--spacing-4);
  }
  .run {
    display: grid;
    gap: var(--spacing-2);
    padding-bottom: var(--spacing-4);
    border-bottom: 1px solid var(--color-border);
  }
  .run-head {
    display: flex;
    align-items: center;
    gap: var(--spacing-1-5);
  }
  .late {
    display: grid;
    gap: var(--spacing-2);
    padding-top: var(--spacing-1);
  }
  .late :global(code) {
    overflow-wrap: anywhere;
  }
  /* On a phone the labels sit above their facts. */
  @container (max-width: 360px) {
    .facts :global(dl) {
      grid-template-columns: minmax(0, 1fr);
      gap: 0;
    }
    .facts :global(dd + dt) {
      margin-top: var(--spacing-2);
    }
  }
</style>
