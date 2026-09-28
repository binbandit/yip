import type { Node, ProviderPreference } from '../api/types.gen';

export function providerReadiness(nodes: Node[], preference: ProviderPreference) {
  const reported = nodes.filter((n) => !n.revokedAt && n.status !== 'revoked').flatMap((node) =>
    (node.providers ?? []).filter((provider) => provider.provider === preference.provider).map((provider) => ({ node, provider })));
  const signedIn = reported.filter(({ provider: p }) => p.authState === 'ready' && (!preference.profileId || p.profileId === preference.profileId));
  const connected = signedIn.filter(({ node: n }) => n.status === 'online' && !n.draining);
  const permitted = connected.filter(({ provider: p }) => p.billing !== 'api' || preference.allowApiBilling);
  const ready = permitted.filter(({ provider: p }) => p.capabilities?.readOnly);
  const reason = ready.length ? ''
    : !signedIn.length ? preference.profileId ? 'The selected account is not signed in on a paired machine.' : 'Not signed in on a paired machine.'
      : !connected.length ? 'Signed in, but no machine is available for new work.'
        : !permitted.length ? 'Only API-billed sign-ins are available. API billing is not enabled for this engineer.'
          : 'The available provider cannot enforce read-only conversations or reviews.';
  return { ready, signedIn, reason };
}
