<script lang="ts">
  // The first-run journey (spec §7A) as a short checklist on the Overview,
  // derived from live state: a paired machine, a provider signed in there
  // with its own tool, an engineer using it, a room, a project, and a first
  // real piece of work with its result. It never declares setup successful
  // from a connection dot alone; the last step is finished work.
  import { app } from '../lib/state/app.svelte';
  import { providerLabel } from '../lib/util/labels';
  import StateIcon from './StateIcon.svelte';

  const KEY = 'yip.gettingStarted.dismissed';
  let dismissed = $state(false);
  let expanded = $state(false);
  const hasRecordedWork = $derived(Object.values(app.data.jobs).some((j) => j.kind !== 'reply' && j.kind !== 'review' && !j.parentId));
  const showSteps = $derived(expanded || !hasRecordedWork);
  try {
    dismissed = localStorage.getItem(KEY) === '1';
  } catch {
    /* storage unavailable: show it */
  }
  function dismiss() {
    dismissed = true;
    try {
      localStorage.setItem(KEY, '1');
    } catch {
      /* ignore */
    }
  }

  const SIGN_IN: Record<string, string> = { codex: 'codex login', claude: 'claude auth login', cursor: 'agent login' };

  const nodes = $derived(Object.values(app.data.nodes).filter((n) => !n.revokedAt && n.status !== 'revoked'));
  // Real providers signed in on a connected machine (the fake one doesn't count).
  const ready = $derived.by(() => {
    const s = new Set<string>();
    for (const n of nodes) {
      if (n.status !== 'online') continue;
      for (const p of n.providers ?? []) if (p.provider !== 'fake' && p.authState === 'ready') s.add(p.provider);
    }
    return [...s];
  });
  const needsSignIn = $derived.by(() => {
    const s = new Set<string>();
    for (const n of nodes) for (const p of n.providers ?? []) if (p.authState === 'needs_signin' && !ready.includes(p.provider)) s.add(p.provider);
    return [...s];
  });
  const realEngineers = $derived(Object.values(app.data.engineers).filter((e) => !e.archived && ready.includes(e.provider.provider)));
  const rooms = $derived(
    Object.values(app.data.rooms).filter((r) => r.kind === 'room' && r.members.some((m) => m.kind === 'engineer' && realEngineers.some((e) => e.id === m.id))),
  );
  const anyRoom = $derived(Object.values(app.data.rooms).find((r) => r.kind === 'room'));
  const projects = $derived(Object.values(app.data.projects).filter((p) => (p.repos ?? []).length > 0));
  const firstWork = $derived(
    Object.values(app.data.jobs).some((j) => j.state === 'completed' && j.kind !== 'reply' && j.kind !== 'review' && realEngineers.some((e) => e.id === j.ownerId)),
  );

  const steps = $derived([
    {
      done: nodes.length > 0,
      title: 'Pair a machine',
      detail: nodes.length ? `${nodes.map((n) => n.name).join(', ')} paired.` : 'Work runs on machines you choose, not in this window.',
      href: '/machines',
      action: 'Add machine',
    },
    {
      done: ready.length > 0,
      title: 'Sign in a provider on it',
      detail: ready.length
        ? `${ready.map(providerLabel).join(' and ')} ready, using the sign-in already on the machine.`
        : needsSignIn.length
          ? `Run ${needsSignIn.map((p) => SIGN_IN[p] ?? p).join(' or ')} on the machine; yip reuses that sign-in and never asks for tokens.`
          : 'Sign in to Codex, Claude Code or Cursor on the machine with its own tool.',
      href: '/machines',
      action: 'Check machines',
    },
    {
      done: realEngineers.length > 0,
      title: 'Give an engineer that provider',
      detail: realEngineers.length ? `${realEngineers.map((e) => e.name).join(', ')} can run on it.` : 'Create an engineer, or set an existing one’s provider on their profile.',
      href: '/engineers',
      action: 'Engineers',
    },
    {
      done: rooms.length > 0,
      title: 'Bring them into a room',
      detail: rooms.length ? `In ${rooms.map((r) => r.name).join(', ')}.` : 'A room is a group with a purpose; it can range across projects.',
      // With rooms already here, add the engineer to one rather than start another.
      href: anyRoom ? `/rooms/${anyRoom.id}?panel=room%3A${anyRoom.id}` : '',
      action: anyRoom ? `Add to ${anyRoom.name}` : 'Create a room',
    },
    {
      done: projects.length > 0,
      title: 'Link a project',
      detail: projects.length ? `${projects.map((p) => p.name).join(', ')}.` : 'Connect a repository when you want the team to inspect or change code.',
      href: '/projects',
      action: 'Projects',
    },
    {
      done: firstWork,
      title: 'Ask for a small, real piece of work',
      detail: firstWork ? 'Finished, with its evidence.' : 'Mention an engineer with a small investigation; its result shows what changed, the checks and where it ran.',
      href: rooms[0] ? `/rooms/${rooms[0].id}` : '',
      action: rooms[0] ? `Open ${rooms[0].name}` : '',
    },
  ]);
  const remaining = $derived(steps.filter((s) => !s.done).length);
</script>

{#if !dismissed && remaining > 0}
  <section class="start" aria-labelledby="gs-title">
    <header>
      <h2 id="gs-title" class="section-title">Getting started</h2>
      <span class="meta">{steps.length - remaining} of {steps.length} done</span>
      <div class="actions">
        {#if hasRecordedWork}<button class="btn btn-sm btn-quiet" aria-expanded={showSteps} aria-controls="gs-steps" onclick={() => (expanded = !expanded)}>{showSteps ? 'Show less' : 'Show steps'}</button>{/if}
        <button class="btn btn-sm btn-quiet" onclick={dismiss}>Hide</button>
      </div>
    </header>
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
