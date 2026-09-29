import type { Node } from '../api/types.gen';
import { providerLabel } from './labels';
import { SIGN_IN_COMMANDS } from './machines';

const guides: Record<string, { description: string; installUrl: string; installCommand?: string; installNote?: string }> = {
  codex: {
    description: 'Use the Codex CLI with your ChatGPT account.',
    installUrl: 'https://developers.openai.com/codex/cli',
  },
  claude: {
    description: 'Use your existing subscription through the command-line tool.',
    installUrl: 'https://code.claude.com/docs/en/setup',
  },
  cursor: {
    description: 'Use Cursor Agent with your Cursor account, not just the editor.',
    installUrl: 'https://cursor.com/docs/cli/installation',
  },
  opencode: {
    description: 'Run OpenCode with a model account connected through its own login.',
    installUrl: 'https://opencode.ai/docs/',
    installCommand: 'npm install -g opencode-ai@1.18.33',
    installNote: 'yip supports this audited OpenCode version. Other versions are not enabled until their permission controls have been checked.',
  },
  pi: {
    description: 'Run Pi Agent Harness with a model account you have connected in Pi.',
    installUrl: 'https://github.com/earendil-works/pi/tree/main/packages/coding-agent#quick-start',
    installCommand: 'npm install -g @earendil-works/pi-coding-agent@0.87.1',
    installNote: 'Requires Node.js 22.19 or newer. yip supports Pi 0.87.1 and the older 0.73.1 package. Custom extensions and model configuration are not loaded in yip runs.',
  },
};

export const connections = Object.keys(SIGN_IN_COMMANDS).map((id) => ({
  id,
  label: providerLabel(id),
  ...guides[id],
  signIn: SIGN_IN_COMMANDS[id],
}));

export function connectionPath(provider?: string): string {
  return provider && connections.some((p) => p.id === provider)
    ? `/connections/${encodeURIComponent(provider)}` : '/connections';
}

export function activeMachines(nodes: Node[]): Node[] {
  return nodes.filter((n) => !n.revokedAt && n.status !== 'revoked')
    .sort((a, b) => Number(b.status === 'online') - Number(a.status === 'online') || a.name.localeCompare(b.name));
}

/** A sign-in is not a promise that the machine can accept work. */
export function connectionSummary(nodes: Node[], provider: string): string {
  const active = activeMachines(nodes);
  const signedIn = active.filter((n) => n.providers.some((p) => p.provider === provider && p.authState === 'ready'));
  const online = signedIn.filter((n) => n.status === 'online');
  if (online.length) return `Signed in on ${online.map((n) => n.name).join(', ')}`;
  if (signedIn.length) return 'Signed in · machine offline';
  const reported = active.flatMap((n) => n.providers.filter((p) => p.provider === provider));
  if (reported.some((p) => p.authState === 'needs_signin')) return 'Needs sign-in';
  if (reported.some((p) => p.authState === 'error' || p.authState === 'unknown')) return 'Could not verify sign-in';
  return 'Not connected';
}
