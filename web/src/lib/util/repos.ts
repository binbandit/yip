// Reading what a pasted remote URL already says about its repository.

/** The owner/name of a GitHub remote (https, ssh or scp-style), or null. */
export function githubRepo(url: string): string | null {
  const m = url.trim().match(/^(?:https?:\/\/|ssh:\/\/git@|git@)?(?:www\.)?github\.com[/:]([\w.-]+)\/([\w.-]+?)(?:\.git)?\/?$/i);
  return m ? `${m[1]}/${m[2]}` : null;
}
