<script lang="ts">
  import { onMount } from 'svelte';
  import { Button, Code, CodeBlock, Heading, Link, Selector, Text } from '@astryx-svelte/core';
  import { app } from '../lib/state/app.svelte';
  import { workspaceUrl } from '../lib/workspace';
  import { api } from '../lib/api/endpoints';
  import { errorMessage } from '../lib/api/client';
  import { activeMachines, connections, connectionSummary } from '../lib/util/connections';
  import { authStateLabel, billingLabel } from '../lib/util/labels';
  import { providerReadiness } from '../lib/util/providerReadiness';
  import { atTime } from '../lib/util/time';
  import Screen from '../components/Screen.svelte';
  import Notice from '../components/Notice.svelte';
  import AddMachineDialog from '../components/AddMachineDialog.svelte';

  let { provider }: { provider?: string } = $props();
  const guide = $derived(connections.find((p) => p.id === provider));
  const harness = $derived(provider === 'opencode' || provider === 'pi');
  const nodes = $derived(activeMachines(Object.values(app.data.nodes)));
  let selectedNode = $state('');
  const node = $derived(nodes.find((n) => n.id === selectedNode));
  const nodeId = $derived(node?.id);
  const installation = $derived(node?.providers.find((p) => p.provider === provider));
  const signedIn = $derived(installation?.authState === 'ready');
  const readiness = $derived(providerReadiness(node ? [node] : [], { provider: provider ?? '', allowApiBilling: true }));
  const engineers = $derived(Object.values(app.data.engineers).filter((e) => !e.archived && e.provider.provider === provider));
  let adding = $state(false);
  let loadError = $state('');
  let checkError = $state('');
  let copied = $state(false);
  let requestId = 0;
  let check = $state<{ id: number; nodeId: string; before: string; timedOut: boolean } | null>(null);
  const report = (id: string) => `${app.data.nodeReportSeq[id] ?? 0}:${JSON.stringify(app.data.nodes[id]?.providers ?? [])}`;
  const received = $derived(!!check && report(check.nodeId) !== check.before);
  const checking = $derived(!!check && !received && !check.timedOut);

  $effect(() => {
    if (!selectedNode && nodes.length) selectedNode = nodes[0].id;
  });

  onMount(() => {
    let alive = true;
    api.nodes().then((ns) => {
      if (alive) for (const n of ns ?? []) app.data.nodes[n.id] = n;
    }).catch((err) => { if (alive) loadError = errorMessage(err); });
    return () => { alive = false; };
  });

  $effect(() => {
    if (!check || received || check.timedOut) return;
    const pending = check;
    const timer = setTimeout(() => {
      if (check === pending) check = { ...pending, timedOut: true };
    }, 90_000);
    return () => clearTimeout(timer);
  });

  $effect(() => {
    void nodeId;
    void provider;
    check = null;
    checkError = '';
    copied = false;
  });

  async function recheck() {
    if (!node || checking) return;
    const id = node.id;
    const pending = { id: ++requestId, nodeId: id, before: report(id), timedOut: false };
    check = pending;
    checkError = '';
    try {
      await api.probeNode(id);
    } catch (err) {
      if (check?.id === pending.id) {
        check = null;
        checkError = errorMessage(err);
      }
    }
  }

  async function copySignIn() {
    if (!guide) return;
    try {
      await navigator.clipboard.writeText(guide.signIn);
      copied = true;
      app.announce('Sign-in command copied.');
    } catch {
      app.toast("Couldn't copy — select the command and copy it manually.", 'error');
    }
  }
</script>

<Screen title={guide ? `Connect ${guide.label}` : 'Connections'}
  subtitle={guide ? 'Use the account you already have, on the machine that runs your engineers.' : 'Bring your existing AI subscriptions and agent harnesses. Choose your tool to connect it.'}
  width={920}>
  {#snippet crumb()}{#if provider}<Link href={workspaceUrl('/connections')} hasUnderline>All connections</Link>{/if}{/snippet}
  {#if loadError}<Notice tone="danger" role="alert">Could not refresh machines: {loadError} Reload this page to try again.</Notice>{/if}

  {#if guide}
    <div class="guide">
      {#if harness}
        <Notice title="A harness, not another subscription" description={`${guide.label} runs models from accounts you connect inside it. Use its supported login method for your model provider; subscriptions are not interchangeable between tools. Your account’s usage limits still apply.`} />
      {:else}
        <p class="explain">There is no subscription to buy from yip and no API key to paste here. Sign in with {guide.label} on your machine; yip uses that local sign-in. Your provider’s plan and usage limits still apply.</p>
      {/if}
      {#if guide.id === 'cursor'}<Notice title="Experimental connection" description="The Cursor adapter has not been verified with a real account. Its billing mode is reported as unknown, so yip cannot confirm that runs use your subscription." />{/if}
      <ol class="steps">
        <li>
          <div class="step">
            <Heading level={2}>Choose where it runs</Heading>
            <p>The sign-in must be on the computer running <Code>yip runner</Code>, under the same operating-system user. Signing in on this browser’s computer will not connect a different machine.</p>
            {#if nodes.length}
              <Selector label="Machine" width="100%" value={node?.id ?? ''}
                options={nodes.map((n) => ({ value: n.id, label: `${n.name}${n.status !== 'online' ? ' — offline' : n.draining ? ' — new work paused' : ''}` }))}
                onChange={(v: string) => (selectedNode = v)} />
              {#if selectedNode && !node}<Notice>The selected machine is no longer available. Choose another machine to continue.</Notice>{/if}
              <Button label="Add another machine" size="sm" variant="ghost" onclick={() => (adding = true)} />
            {:else}
              <Notice title="Pair a machine first" description="This can be your own computer or an always-on server. Keep this page open while you pair it; the next steps appear when it connects." />
              <Button label="Add machine" variant="primary" onclick={() => (adding = true)} />
            {/if}
          </div>
        </li>
        <li>
          <div class="step">
            <Heading level={2}>Install and sign in{node ? ` on ${node.name}` : ''}</Heading>
            <p>{guide.description} If you already use it on this machine, skip signing in and check the connection below.</p>
            <Link href={guide.installUrl} target="_blank" rel="noreferrer" hasUnderline>Open {guide.label} installation guide (new tab)</Link>
            {#if guide.installCommand}
              <Text as="p" type="supporting">{guide.installNote}</Text>
              <CodeBlock code={guide.installCommand} hasCopyButton isWrapped size="sm" width="100%" aria-label="Installation command" />
            {/if}
            {#if node}
              <p>In a terminal on <strong>{node.name}</strong>, run:</p>
              <CodeBlock code={guide.signIn} hasCopyButton={false} isWrapped size="sm" width="100%" aria-label="Sign-in command" />
              <Button label={copied ? 'Copied sign-in command' : 'Copy sign-in command'} size="sm" onclick={copySignIn} />
              {#if guide.id === 'pi'}
                <p>Inside Pi, run <Code>/login</Code> and choose your model provider. Use <Code>/model</Code> to see your models, then choose the model in your yip engineer’s settings. You can close Pi when setup is complete.</p>
              {:else if guide.id === 'opencode'}
                <p>Choose the model provider you want OpenCode to use and complete its sign-in. Run <Code>opencode models</Code> to find a model ID for that account, then enter it in your engineer’s model setting. Listing a model does not grant access to it.</p>
              {/if}
              <Text as="p" type="supporting">Finish signing in with the provider, then come back here. Your sign-in stays on the runner machine; passwords and tokens are not sent to the browser or hub.</Text>
            {/if}
          </div>
        </li>
        <li>
          <div class="step">
            <Heading level={2}>Check the connection</Heading>
            {#if node}
              <div class="report" role="status" aria-live="polite">
                <strong>{node.status !== 'online' ? `${node.name} is offline` : signedIn ? `Signed in on ${node.name}` : installation ? authStateLabel(installation.authState, installation.authDetail) : 'Not detected yet'}</strong>
                {#if node.status !== 'online'}
                  <p>Start <Code>yip runner</Code> on this machine, or restart its background service. Any sign-in shown below is from its last report.</p>
                {/if}
                {#if installation}
                  <p>{installation.account || 'No account reported'} · {billingLabel(installation.billing)}</p>
                  <Text as="p" type="supporting">Last checked {atTime(installation.updatedAt)}</Text>
                  {#if installation.authDetail}<p>{installation.authDetail}</p>{/if}
                {:else}
                  <p>This runner has not reported {guide.label}. Make sure it is enabled in the runner’s provider list, then check again.</p>
                {/if}
                {#if signedIn && installation?.billing === 'api'}
                  <p>{harness ? 'This harness has API-billed accounts configured, possibly alongside subscriptions. yip requires explicit API billing permission for this connection.' : 'This is an API-billed sign-in, not a subscription connection.'} Sign in with your subscription account instead, or explicitly allow API billing in the engineer’s provider settings.</p>
                {:else if signedIn && installation?.billing === 'unknown'}
                  <p>The provider has not confirmed how this account is billed. Check your plan with the provider before starting work; yip cannot guarantee subscription usage.</p>
                {/if}
                {#if signedIn && !readiness.ready.length}<p>{readiness.reason}</p>{/if}
                {#if signedIn && !installation?.capabilities?.readOnly}
                  <p>Signing in alone does not enable conversations or reviews. Review the machine’s adapter notes before assigning this provider to an engineer.</p>
                {/if}
                {#if checking}<p>Check requested. Waiting for a new report from {node.name}…</p>
                {:else if received}<p>New machine report received.</p>
                {:else if check?.timedOut}<p>No new report yet. Check that the runner is still connected, then try again.</p>{/if}
              </div>
              {#if checkError}<Notice tone="danger" role="alert">{checkError}</Notice>{/if}
              <div class="actions">
                <Button label={checking ? 'Checking connection…' : 'Check connection'} variant="primary" isDisabled={node.status !== 'online' || checking} onclick={recheck} />
                <Button label="Machine details" href={workspaceUrl(`/connections/${guide.id}?panel=machine%3A${encodeURIComponent(node.id)}&tab=connections`)} />
              </div>
              <details>
                <summary>Signed in, but not detected?</summary>
                <div class="troubleshooting">
                  <p>Make sure the provider’s command works in a terminal on <strong>{node.name}</strong>, as the same user who runs yip.</p>
                  <p>A new sign-in or a tool installed on the runner’s existing PATH only needs another check. If you changed PATH or the sign-in’s home directory, restart the runner with that environment. A background service does not inherit changes to your terminal.</p>
                  <p>If you start the runner with <Code>--providers</Code>, include <Code>{guide.id}</Code> in that list and restart it. For a runner hosted inside the hub, use the hub’s <Code>--local-providers</Code> option and restart the hub instead. Keep your other providers enabled.</p>
                  <p>Sign-ins are checked automatically every five minutes. Use Check connection after signing in rather than waiting for the next automatic check.</p>
                </div>
              </details>
            {:else}
              <p>Once your machine connects, yip can check its sign-in here. This page updates automatically.</p>
            {/if}
          </div>
        </li>
        <li>
          <div class="step">
            <Heading level={2}>Choose who uses it</Heading>
            <p>A connection does not change your engineers automatically. Choose <strong>{guide.label}</strong> in an engineer’s provider preference. Several engineers can share one sign-in.</p>
            {#if harness}<p>Also choose the underlying model in the engineer’s model setting. Its provider determines billing and usage limits.{#if guide.id === 'opencode'} Enter the full <Code>provider/model</Code> ID; OpenCode’s model list is not fetched automatically.{/if}</p>{/if}
            {#each engineers as engineer (engineer.id)}
              <Link href={workspaceUrl(`/engineers/${engineer.id}#eng-prov`)} hasUnderline>{engineer.name}’s provider settings</Link>
            {/each}
            <Button label={engineers.length ? 'Manage engineers' : 'Choose an engineer'} href={workspaceUrl('/engineers')} />
            {#if harness}
              <details>
                <summary>What carries over from my local setup?</summary>
                <div class="troubleshooting">
                  <p>yip reuses the harness’s local model sign-ins, not its custom agents, plugins, extensions or project configuration. Your engineer’s instructions and yip’s scoped tools control the run.</p>
                  <p>{guide.id === 'opencode' ? 'OpenCode’s native shell and subagents are disabled. Engineers run checks and publish changes through yip’s runner-owned tools instead.' : 'Pi’s edit and shell tools require runner approval. Conversation and review runs have no native write or shell tools.'}</p>
                  <p>These are tool restrictions, not an operating-system sandbox. Use a dedicated machine account if you need stronger separation.</p>
                </div>
              </details>
            {/if}
          </div>
        </li>
      </ol>
    </div>
  {:else}
    <div class="intro">
      <Heading level={2}>Which tool do you use?</Heading>
      <p>yip runs the tool on a paired machine using its existing sign-in. You do not connect a subscription to the browser or upload credentials.</p>
      {#if provider}<Notice>No setup guide exists for “{provider}”. Choose a supported connection below.</Notice>{/if}
    </div>
    <ul class="catalog" aria-label="Supported connections">
      {#each connections as p (p.id)}
        <li>
          <div>
            <Heading level={2}>{p.label}</Heading>
            <p>{p.description}</p>
            {#if p.id === 'cursor'}<Text as="p" type="supporting">Experimental · real-account use not verified</Text>{/if}
            <Text as="p" type="supporting">{connectionSummary(nodes, p.id)}</Text>
          </div>
          <Button label={`Set up ${p.label}`} href={workspaceUrl(`/connections/${p.id}`)}>Set up</Button>
        </li>
      {/each}
    </ul>
    <div class="harness-note">
      <Heading level={2}>Your harness and your subscription are different choices</Heading>
      <p>OpenCode and Pi run models from accounts you connect inside those tools. Choose the harness here, then choose its model in your engineer’s settings. A sign-in to one tool does not transfer a subscription to another.</p>
    </div>
    <details class="billing">
      <summary>Will this use my subscription or charge an API account?</summary>
      <p>That depends on how the tool is signed in on your machine. Each connection shows the billing mode reported by that tool: subscription, API, or unknown. API-billed accounts require explicit permission in an engineer’s settings. Unknown billing is not a guarantee of subscription usage.</p>
    </details>
  {/if}
</Screen>

{#if adding}<AddMachineDialog onclose={() => (adding = false)} />{/if}

<style>
  .intro, .guide, .harness-note {
    display: grid;
    gap: var(--spacing-3);
    margin-block: var(--spacing-4) var(--spacing-5);
  }
  p { max-width: 72ch; }
  .explain, .intro > p, .catalog p, .harness-note > p { color: var(--color-text-secondary); }
  .catalog { list-style: none; margin: 0; padding: 0; }
  .catalog li {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--spacing-4);
    padding-block: var(--spacing-5);
    border-top: 1px solid var(--color-border);
  }
  .catalog li > div { display: grid; gap: var(--spacing-1-5); }
  .catalog :global(h2), .harness-note :global(h2), .step :global(h2) { font-size: var(--font-size-lg); }
  .harness-note { padding-block: var(--spacing-4); border-top: 1px solid var(--color-border); }
  .steps { list-style: decimal; margin: 0; padding-left: var(--spacing-6); }
  .steps > li { padding: var(--spacing-5) 0 var(--spacing-5) var(--spacing-2); border-top: 1px solid var(--color-border); }
  .steps > li::marker { color: var(--color-text-secondary); font-weight: var(--font-weight-semibold); }
  .step { display: grid; gap: var(--spacing-3); min-width: 0; justify-items: start; }
  .step :global(.astryx-selector), .report { width: 100%; }
  .step :global(code) { user-select: all; overflow-wrap: anywhere; }
  .report { display: grid; gap: var(--spacing-2); padding: var(--spacing-4); background: var(--color-background-muted); border-radius: var(--radius-element); overflow-wrap: anywhere; }
  .actions { display: flex; gap: var(--spacing-3); flex-wrap: wrap; align-items: center; }
  summary { cursor: pointer; font-weight: var(--font-weight-medium); padding-block: var(--spacing-2); }
  summary:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
  .troubleshooting { display: grid; gap: var(--spacing-2); padding-block: var(--spacing-2); }
  .billing { border-top: 1px solid var(--color-border); padding-top: var(--spacing-2); }
  @media (max-width: 560px) {
    .catalog li { grid-template-columns: minmax(0, 1fr); justify-items: start; gap: var(--spacing-3); }
    .steps { padding-left: var(--spacing-4); }
    .steps > li { padding-left: var(--spacing-1); }
  }
</style>
