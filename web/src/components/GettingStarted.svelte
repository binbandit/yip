<script lang="ts">
  import { app } from '../lib/state/app.svelte';
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
  <button class="btn btn-sm btn-quiet restore" onclick={() => setDismissed(false)}>Show getting started</button>
{:else}
  <section class="start" aria-labelledby="gs-title">
    <header>
      <h2 id="gs-title" class="section-title">Getting started</h2>
      <span class="meta">{steps.length - remaining} of {steps.length} done</span>
      <div class="actions">
        {#if hasRecordedWork}<button class="btn btn-sm btn-quiet" aria-expanded={showSteps} aria-controls="gs-steps" onclick={() => (expanded = !expanded)}>{showSteps ? 'Show less' : 'Show steps'}</button>{/if}
        <button class="btn btn-sm btn-quiet" onclick={() => setDismissed(true)}>Hide</button>
      </div>
    </header>
    {#if setup.unavailable.length > 0}
      <div class="availability meta">
        {#each setup.unavailable as issue (issue.engineer.id)}
          <p><strong>{issue.engineer.name}:</strong> {issue.reason} <a href={issue.href}>{issue.action}</a></p>
        {/each}
        <p>Your completed setup stays in place.</p>
      </div>
    {/if}
    <ol id="gs-steps" hidden={!showSteps}>
      {#each steps as s, i (s.title)}
        {@const next = !s.done && steps.slice(0, i).every((x) => x.done)}
        <li class:done={s.done} class:next>
          <StateIcon shape={s.done ? 'check-filled' : 'circle'} tone={s.done ? 'success' : 'neutral'} size={15} />
          <div class="body">
            <strong>{s.title}</strong>
            <span class="meta">{s.detail}</span>
          </div>
          {#if !s.done && s.action}
            {#if s.href}<a class="btn btn-sm" class:btn-primary={next} href={s.href}>{s.action}</a>
            {:else}<button class="btn btn-sm" class:btn-primary={next} onclick={() => (app.createRoom = { kind: 'room' })}>{s.action}</button>{/if}
          {/if}
        </li>
      {/each}
    </ol>
  </section>
{/if}

<style>
  .restore {
    margin-bottom: 20px;
  }
  .availability {
    margin: 8px 0 12px;
  }
  .availability p {
    margin: 4px 0;
  }
  .start {
    container-type: inline-size;
    margin-bottom: 28px;
    padding: 14px 16px 10px;
    border: 1px solid var(--line-strong);
    border-radius: var(--r-artifact);
  }
  header {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 6px;
  }
  .actions {
    display: flex;
    gap: 4px;
    margin-left: auto;
  }
  ol[hidden] {
    display: none;
  }
  ol {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
  }
  li {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 0;
    border-top: 1px solid var(--line-soft);
  }
  li:first-child {
    border-top: 0;
  }
  .body {
    display: grid;
    flex: 1;
    min-width: 0;
  }
  .done strong {
    color: var(--ink-secondary);
    font-weight: 500;
  }
  @container (max-width: 440px) {
    li {
      display: grid;
      grid-template-columns: 15px minmax(0, 1fr);
      align-items: start;
      row-gap: 6px;
    }
    li .btn {
      grid-column: 2;
      justify-self: start;
      max-width: 100%;
      white-space: normal;
    }
  }
</style>
