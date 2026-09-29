# 0015 — GitHub repositories by owner/name, reached with the GitHub CLI's sign-in

**Decision.** A repository can be added (to a new or existing project) by just
its GitHub `owner/name`. The hub turns it into the HTTPS clone URL, links the
forge, names it, and asks GitHub for the default branch: through `gh repo
view` on the hub's machine first, then the REST API with the stored forge
credential or anonymously. A repository GitHub doesn't show is refused unless
a default branch is given, so a typo doesn't become a broken project.

Private repositories are reached with the GitHub CLI's existing sign-in, never
a token stored in the repository URL or in yip. For a github.com HTTPS remote,
the runner clones with `gh auth git-credential` as the credential helper and
records that helper in the repository's replica only (as `gh auth setup-git`
would, but scoped to one repository), after any helpers the owner configured.
`yip forge github add --from-gh` stores the CLI's token as the hub's forge
credential when the owner asks for it.

**Why.** "Add a project by remote repository" (spec 02, journey A) should
not require composing a clone URL, a forge identity and a branch by hand, and
most private repositories are already reachable through `gh auth login`.

**Consequence.** The runner's fetches, and pushes an engineer is granted or the
owner approves (0006), authenticate as the machine's `gh` account, exactly as
they would with the owner's SSH agent. Checks still run without any credential
helper (0005): their environment clears every helper, including this one.
