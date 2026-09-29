// How a person names a repository when adding it. The hub decides
// (internal/forge/github ParseRepoInput); this mirrors it so a form can show
// what it understood before saving.

export interface GitHubRepo {
  owner: string;
  name: string;
}

const OWNER = /^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,98}[A-Za-z0-9_])?$/;
const NAME = /^[A-Za-z0-9._-]{1,100}$/;
const isGitHubHost = (host: string) => ['github.com', 'www.github.com'].includes(host.toLowerCase());

/**
 * Reads owner/name, github.com/owner/name, https://github.com/owner/name(.git),
 * git@github.com:owner/name(.git) or ssh://git@github.com/owner/name(.git).
 * Anything else (another host, a local path, a pull request URL) is null.
 */
export function parseGitHubRepo(raw: string): GitHubRepo | null {
  const s = raw.trim();
  if (!s || /[\s\\?#]/.test(s)) return null;
  let path: string;
  if (s.startsWith('git@')) {
    const rest = s.slice(4);
    const colon = rest.indexOf(':');
    if (colon < 0 || !isGitHubHost(rest.slice(0, colon))) return null;
    path = rest.slice(colon + 1);
  } else if (s.includes('://')) {
    let u: URL;
    try {
      u = new URL(s);
    } catch {
      return null;
    }
    if (!isGitHubHost(u.hostname) || u.port) return null;
    const https = u.protocol === 'https:' && !u.username && !u.password;
    const ssh = u.protocol === 'ssh:' && u.username === 'git' && !u.password;
    if (!https && !ssh) return null;
    path = u.pathname;
  } else {
    const slash = s.indexOf('/');
    path = slash > 0 && isGitHubHost(s.slice(0, slash)) ? s.slice(slash + 1) : s;
  }
  const segs = path.replace(/^\/+|\/+$/g, '').split('/');
  if (segs.length !== 2) return null;
  const [owner, file] = segs;
  const name = file.replace(/\.git$/, '');
  if (!OWNER.test(owner) || !NAME.test(name) || name === '.' || name === '..') return null;
  return { owner, name };
}

/** The name a repository gets when none is given: atlas for acme/atlas or git@host:acme/atlas.git. */
export function repoNameFor(remote: string): string {
  const gh = parseGitHubRepo(remote);
  if (gh) return gh.name;
  const s = remote.trim().replace(/\/+$/, '');
  const name = s.slice(Math.max(s.lastIndexOf('/'), s.lastIndexOf(':')) + 1).replace(/\.git$/, '');
  return name === '.' || name === '..' ? '' : name;
}
