# Skills available to every engineer

Yip ships the four [bin-stack](https://github.com/binbandit/bin-stack) skills:
`arena`, `babysit-pr`, `bro`, and `file-pr`. Each run's instructions list their
descriptions. The engineer loads a matching skill with `skill_read` on the
existing yip bridge. Skill contents are immutable, identified by their source
revision and SHA-256, and available without network access or host skill links.

This applies to every provider in edit, review and conversation modes, on native
runners and through the Docker worker's bridge. It also works when a provider
deliberately disables inherited user skills. These are yip-provided skills;
they do not appear as native harness slash commands or enable host plugins.

Skills supply workflows, not authority. Run modes, repository scope, permission
checks, durable jobs, exact-revision checks and independent review still apply.
A skill cannot add a missing harness tool, grant model spending, or authorize a
push, external message, merge or deployment. Arena uses yip's coordination tools
where applicable and its sequential fallback when parallel agents are unavailable.

The pinned source and MIT license are in
[`internal/skills/bin-stack`](../internal/skills/bin-stack/README.md). Updates are
reviewed source changes rather than a runtime download. Upgrade the hub and every
host runner together. For Docker runners, also rebuild or update the configured
agent image from `packaging/container/Dockerfile.agent` at the same YIP revision:
that image contains its own `/usr/local/bin/yip` worker and bridge. An old image
will not advertise `skill_read`, even after the host runner is upgraded. See the
[agent image build instructions](ephemeral-agents.md) and restart runners with the
matching image before starting new runs. Existing binaries, containers and runs
do not gain the bundle automatically.

No global skill installation is required for these four skills. Other host skills
remain subject to the existing import rules, including the Docker importer's
rejection of symlinks.
