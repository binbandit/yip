<script lang="ts">
  import { Button, Heading, Link, Text } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import { providerLabel } from '../lib/util/labels';
  import { roomSettings, setupReadiness } from '../lib/util/setup';
  import StateIcon from './StateIcon.svelte';

  const KEY = 'yip.gettingStarted.dismissed';
  let dismissed = $state(false);
  let expanded = $state(false);
  const hasRecordedWork = $derived(Object.values(app.data.jobs).some((j) => j.kind !== 'reply' && j.kind !== 'review' && !j.parentId));
  const showSteps = $derived(expanded || !hasRecordedWork);
  try {
    dismissed = localStorage.getItem(KEY) === '1';
  } catch { /* storage unavailable: show it */ }
  function setDismissed(value: boolean) {
    dismissed = value;
    if (!value) expanded = true;
    try {
      if (value) localStorage.setItem(KEY, '1');
      else localStorage.removeItem(KEY);
    } catch { /* ignore */ }
  }

  const setup = $derived(setupReadiness(app.data));
  const engineer = $derived(setup.engineer);
  const room = $derived(setup.room);
  const project = $derived(setup.project);
  const reviewCandidate = $derived(setup.reviewerCandidate);
  const reviewerInRoom = $derived(!!room && !!reviewCandidate && room.members.some((m) => m.kind === 'engineer' && m.id === reviewCandidate.id));
  const projectHref = $derived(project ? `/projects/${project.id}` : '/projects');
  const SIGN_IN: Record<string, string> = { codex: 'codex login', claude: 'claude auth login', cursor: 'agent login' };
  const signInProvider = $derived(engineer?.provider.provider ?? setup.providerInstalled.find((p) => p.authState === 'ready')?.provider ?? setup.providerInstalled[0]?.provider);
  const steps = $derived([
    {
      done: setup.nodes.length > 0,
      title: 'Pair a machine',
      detail: setup.nodes.length ? `${setup.nodes.map((n) => n.name).join(', ')} paired.` : 'Your engineers work on machines you choose.',
      href: '/machines', action: 'Add machine',
    },
    {
      done: setup.providerSignedIn,
      title: 'Sign in a provider on it',
      detail: setup.providerSignedIn
        ? `${providerLabel(signInProvider ?? '')} sign-in is connected${app.data.demo && signInProvider === 'fake' ? ' for the scripted demo' : ''}.`
        : signInProvider && SIGN_IN[signInProvider]
          ? `Run ${SIGN_IN[signInProvider]} on your machine; yip uses that sign-in.`
          : 'Sign in to Codex, Claude Code or Cursor on your machine with its own tool.',
      href: '/machines', action: 'Check machines',
    },
    {
      done: !!engineer,
      title: 'Choose your first engineer',
      detail: engineer ? `${engineer.name} uses ${providerLabel(engineer.provider.provider)}.` : 'Give them a name, a role and a provider. You can change these later.',
      href: '/engineers', action: 'Engineers',
    },
    {
      done: setup.inRoom,
      title: 'Bring them into a room',
      detail: setup.inRoom ? `${engineer?.name} is in ${room?.name}. You can talk there now.` : 'A room is a conversation with your team. Projects can come later.',
      href: room ? roomSettings(room) : '', action: room ? `Set up ${room.name}` : 'Create a room',
    },
    {
      done: setup.projectReady,
      title: 'Connect a project when you need code',
      detail: setup.projectReady
        ? `${engineer?.name} can inspect ${project?.name} from ${room?.name}.`
        : !project ? 'A project connects a repository and sets what your engineers can do.'
          : !project.repos.length ? `Add a repository to ${project.name}.`
          : !setup.projectLinked ? `Link ${project.name} to ${room?.name ?? 'your room'} in room settings.`
          : !setup.inRoom ? `Add ${engineer?.name ?? 'an engineer'} to ${room?.name} first.`
          : `Choose ${engineer?.name}'s access to ${project.name}. Read access is enough for an investigation.`,
      href: project?.repos.length && !setup.projectLinked && room ? roomSettings(room) : `${projectHref}${project ? project.repos.length ? '#p-access' : '#p-repos' : ''}`,
      action: !project ? 'Projects' : !project.repos.length ? 'Add repository' : !setup.projectLinked && room ? 'Link to room' : 'Choose access',
    },
    {
      done: setup.reviewReady,
      title: 'Give the work a second pair of eyes',
      detail: !setup.needsReviewer ? 'An investigation can finish without peer review in this project. Code changes still need a colleague.'
        : setup.reviewer ? `${setup.reviewer.name} can review ${project?.name} in ${room?.name}. Your engineers arrange the review.`
          : !reviewCandidate ? 'Projects require peer review by default. Add a second engineer; they can share the same provider and machine.'
          : !reviewerInRoom ? `Invite ${reviewCandidate.name} to ${room?.name ?? 'your room'} so the team can coordinate reviews.`
          : `Give ${reviewCandidate.name} read access to ${project?.name ?? 'the project'} so they can review the work.`,
      href: !reviewCandidate ? '/engineers' : !reviewerInRoom ? room ? roomSettings(room) : '' : `${projectHref}${project ? '#p-access' : ''}`,
      action: !reviewCandidate ? 'Add a colleague' : !reviewerInRoom ? room ? 'Invite to room' : 'Create a room' : 'Choose review access',
    },
    {
      done: setup.completed,
      title: 'Ask for a small, real piece of work',
      detail: setup.completed ? 'Your team has finished its first piece of work.'
        : setup.projectReady && setup.reviewReady ? `Ask @${engineer?.handle} to investigate one small thing in ${project?.name}. The team handles the work and review in the conversation.`
          : 'Start a conversation now. For repository work, finish the project and review choices above.',
      href: setup.inRoom && room ? `/rooms/${room.id}` : '',
      action: setup.inRoom && room ? `Open ${room.name}` : '',
    },
  ]);
  const remaining = $derived(steps.filter((s) => !s.done).length);
</script>

{#if dismissed}
  <div class="restore">
    <Button label="Show getting started" variant="ghost" size="sm" onclick={() => setDismissed(false)} />
  </div>
{:else}
  <section class="start" aria-labelledby="gs-title">
    <header>
      <Heading level={2} id="gs-title">Getting started</Heading>
      <Text type="supporting">{steps.length - remaining} of {steps.length} done</Text>
      <div class="actions">
        {#if hasRecordedWork}
          <Button
            label={showSteps ? 'Show less' : 'Show steps'}
            variant="ghost"
            size="sm"
            aria-expanded={showSteps}
            aria-controls="gs-steps"
            onclick={() => (expanded = !expanded)}
          />
        {/if}
        <Button label="Hide" variant="ghost" size="sm" onclick={() => setDismissed(true)} />
      </div>
    </header>
    {#if setup.unavailable.length > 0}
      <div class="availability">
        {#each setup.unavailable as issue (issue.engineer.id)}
          <Text as="p" display="block" type="supporting">
            <strong>{issue.engineer.name}:</strong> {issue.reason} <Link href={workspaceUrl(issue.href)} type="inherit" hasUnderline>{issue.action}</Link>
          </Text>
        {/each}
        <Text as="p" display="block" type="supporting">Your completed setup stays in place.</Text>
      </div>
    {/if}
    <ol id="gs-steps" hidden={!showSteps}>
      {#each steps as s, i (s.title)}
        {@const next = !s.done && steps.slice(0, i).every((x) => x.done)}
        <li>
          <StateIcon shape={s.done ? 'check-filled' : 'circle'} tone={s.done ? 'success' : 'neutral'} size={15} />
          <div class="body">
            <Text weight={s.done ? 'medium' : 'semibold'} color={s.done ? 'secondary' : 'primary'}>{s.title}</Text>
            <Text type="supporting">{s.detail}</Text>
          </div>
          {#if !s.done && s.action}
            {#if s.href}
              <Button label={s.action} href={workspaceUrl(s.href)} size="sm" variant={next ? 'primary' : 'secondary'} />
            {:else}
              <Button label={s.action} size="sm" variant={next ? 'primary' : 'secondary'} onclick={() => (app.createRoom = { kind: 'room' })} />
            {/if}
          {/if}
        </li>
      {/each}
    </ol>
  </section>
{/if}

<style>
  .restore {
    margin-bottom: var(--spacing-5);
  }
  .start {
    container-type: inline-size;
    margin-bottom: var(--spacing-7);
    padding: var(--spacing-3) var(--spacing-4) var(--spacing-2);
    border: 1px solid var(--color-border-emphasized);
    border-radius: var(--radius-container);
  }
  header {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: var(--spacing-2) var(--spacing-3);
    margin-bottom: var(--spacing-1-5);
  }
  header :global(h2) {
    font-size: var(--font-size-lg);
  }
  .actions {
    display: flex;
    gap: var(--spacing-1);
    margin-left: auto;
  }
  .availability {
    display: grid;
    gap: var(--spacing-1);
    margin: var(--spacing-2) 0 var(--spacing-3);
  }
  ol {
    display: grid;
  }
  ol[hidden] {
    display: none;
  }
  li {
    display: flex;
    align-items: center;
    gap: var(--spacing-2);
    padding: var(--spacing-2) 0;
    border-top: 1px solid var(--color-border);
  }
  li:first-child {
    border-top: 0;
  }
  .body {
    display: grid;
    flex: 1;
    min-width: 0;
  }
  @container (max-width: 440px) {
    li {
      display: grid;
      grid-template-columns: 15px minmax(0, 1fr);
      align-items: start;
      row-gap: var(--spacing-1-5);
    }
    /* Long actions ("Set up <room>") wrap rather than truncate; one line keeps the small button's height. */
    li > :global(.astryx-button) {
      grid-column: 2;
      justify-self: start;
      max-width: 100%;
      height: auto;
      padding-block: var(--spacing-1);
      white-space: normal;
      text-align: start;
    }
  }
</style>
