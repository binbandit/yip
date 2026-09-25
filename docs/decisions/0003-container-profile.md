# 0003 — Container profile as a containerised runner

**Decision.** The "container" execution profile is provided by running a yip
runner inside a restricted Linux container (`packaging/container/`: non-root,
read-only root filesystem, one workspace volume, no host Docker socket or home,
no new privileges, resource limits). Such a runner advertises the `container`
profile; native runners advertise `native` and `readonly` and report
`container` unavailable with the reason. Projects whose policy requires the
container profile are only scheduled on containerised runners.

**Why.** Provider CLIs keep their credentials in the account that runs them.
Spawning per-job containers from a native runner would require moving or
sharing provider logins into ephemeral containers, which the architecture
forbids (no credential harvesting or proxying). A long-lived containerised
runner signs in once, inside its own volume, through the provider's own flow.

**Consequence.** Egress allowlisting is the operator's network configuration
(firewall or proxy); yip does not claim it. Container isolation is not
absolute and is documented as such.
