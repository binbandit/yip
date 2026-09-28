import type { Node, ProviderInstallation, ProviderPreference } from '../api/types.gen';

export function providerReadiness(nodes: Node[], preference: ProviderPreference) {
  const reported = nodes.filter((n) => !n.revokedAt && n.status !== 'revoked').flatMap((node) =>
    (node.providers ?? []).filter((provider) => provider.provider === preference.provider).map((provider) => ({ node, provider })));
  const signedIn = reported.filter(({ provider: p }) => p.authState === 'ready' && (!preference.profileId || p.profileId === preference.profileId));
  const connected = signedIn.filter(({ node: n }) => n.status === 'online' && !n.draining);
  const permitted = connected.filter(({ provider: p }) => p.billing !== 'api' || preference.allowApiBilling);
  // Read-only by itself, or because the owner allowed the provider's own
  // always-allow rules on that machine.
  const rulesOnly = (p: ProviderInstallation) => (p.capabilities?.execPolicyRules?.length ?? 0) > 0;
  const ready = permitted.filter(({ node: n, provider: p }) => p.capabilities?.readOnly || (rulesOnly(p) && (n.trustedRules ?? []).includes(p.provider)));
  const reason = ready.length ? ''
    : !signedIn.length ? preference.profileId ? 'The selected account is not signed in on a paired machine.' : 'Not signed in on a paired machine.'
      : !connected.length ? 'Signed in, but no machine is available for new work.'
        : !permitted.length ? 'Only API-billed sign-ins are available. API billing is not enabled for this engineer.'
          : permitted.some(({ provider: p }) => rulesOnly(p))
            ? "Its own always-allow rules stop read-only conversations and reviews. You can allow them in Machines."
            : 'The available provider cannot enforce read-only conversations or reviews.';
  return { ready, signedIn, reason };
}
