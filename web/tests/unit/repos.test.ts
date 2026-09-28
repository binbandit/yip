import { describe, expect, it } from 'vitest';
import { githubRepo } from '../../src/lib/util/repos';

describe('githubRepo', () => {
  it('reads owner/name from the ways people copy a GitHub remote', () => {
    for (const url of [
      'https://github.com/acme/atlas',
      'https://github.com/acme/atlas.git',
      'https://github.com/acme/atlas/',
      'git@github.com:acme/atlas.git',
      'ssh://git@github.com/acme/atlas.git',
      'github.com/acme/atlas',
    ]) {
      expect(githubRepo(url), url).toBe('acme/atlas');
    }
    expect(githubRepo('https://github.com/acme/atlas.js')).toBe('acme/atlas.js');
  });

  it('ignores other hosts and partial URLs', () => {
    expect(githubRepo('https://gitlab.com/acme/atlas')).toBeNull();
    expect(githubRepo('https://github.com/acme')).toBeNull();
    expect(githubRepo('https://github.com/acme/atlas/pull/4')).toBeNull();
  });
});
