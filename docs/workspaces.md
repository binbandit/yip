# Multiple workspaces

One `yip hub` installation can host independent workspaces. The original
workspace keeps its existing URL and data directory. Create another through
the workspace menu or the authenticated `POST /v1/workspaces` endpoint.

## Using work and personal workspaces

Open the workspace name at the top of the sidebar (or the top bar on a phone)
and choose **Create workspace**. Give it a name such as **Work** or
**Personal**. The new workspace starts with just Overview; add its engineers,
rooms, projects, and machines independently.

Choose another name from the same menu to switch. The browser remembers the
page you left in each workspace and preserves composer drafts and failed
sends separately. Existing drafts in the original workspace survive the
upgrade. Each tab's URL determines its workspace, so work and personal tabs
can stay open together. Browser Back refreshes the restored workspace before
resuming live updates.

Sign-in is shared across the installation. Profiles, preferences, history,
search, and access grants are workspace-local. These are separate application
contexts, not independent OS accounts or a security boundary against the hub
administrator.

## Storage and operation

Additional workspaces live at `<data-dir>/workspaces/<id>` and are discovered
when the hub restarts. Each directory contains its own SQLite database, hub
key, and artifacts. Creation initializes a hidden staging directory and only
publishes it by rename after the owner and Overview are committed. Hidden,
incomplete creations are not routed or discovered.

All workspace hubs remain running when the browser switches workspaces; work
continues in their independent scheduler loops. Installation shutdown closes
every hub. The automatic local runner and demo seeding remain exclusive to the
original root workspace. Other workspaces require explicit runner pairing.

To use the same machine in several workspaces, follow **Machines → Add
machine** in each. The displayed pair and run commands use a separate runner
state directory for each additional workspace. Keep each runner process
running in its own terminal; don't reuse another workspace's pairing files.

Human credentials and sessions exist only in the root database. Child owner
rows keep the stable root user ID but have no usable password hash. Each
request authenticates the root session and loads the child's local owner.
Password recovery on the root invalidates sessions across the installation.
The root CA supplies common transport trust, but each workspace checks runner
node IDs, certificate serials, revocation, enrollments, and run ownership
against its own database; sharing TLS trust does not share authorization.

Use `yip backup --data <data-dir> --out <backup-file> --encrypt` to back up the
entire installation, then `yip restore --from <backup-file> --data <new-dir>`.
The backup snapshots the root and every committed workspace database online,
preserves each workspace's artifacts and hub key, and includes the shared
root `pki` only once. Hidden staging directories are skipped. Snapshots are
consistent per database, not globally simultaneous across workspaces.
A child directory alone does not include the
installation's human credentials or shared CA. There is no supported workspace
rename/delete or live directory move operation.
