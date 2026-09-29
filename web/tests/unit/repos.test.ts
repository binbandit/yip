import { describe, expect, it } from 'vitest';
import { parseGitHubRepo, repoNameFor } from '../../src/lib/util/repos';

// Mirrors internal/forge/github TestParseRepoInput.
describe('GitHub repository input', () => {
  it('reads owner/name and GitHub URLs', () => {
    const atlas = { owner: 'acme', name: 'atlas' };
    for (const input of [
      'acme/atlas',
      '  acme/atlas.git ',
      'github.com/acme/atlas',
      'https://github.com/acme/atlas',
      'https://github.com/acme/atlas.git',
      'https://GitHub.com/acme/atlas/',
      'git@github.com:acme/atlas.git',
      'ssh://git@github.com/acme/atlas.git',
    ]) {
      expect(parseGitHubRepo(input), input).toEqual(atlas);
    }
    expect(parseGitHubRepo('acme-co/atlas_v2.js')).toEqual({ owner: 'acme-co', name: 'atlas_v2.js' });
  });

  it('leaves everything else to be a plain remote', () => {
    for (const input of [
      '',
      'atlas',
      'acme/atlas/extra',
      '/srv/git/atlas.git',
      './acme/atlas',
      '../acme/atlas',
      '~/acme/atlas',
      'https://gitlab.com/acme/atlas.git',
      'git@gitlab.com:acme/atlas.git',
      'gitlab.com/acme/atlas',
      'https://github.com/acme/atlas/pull/42',
      'https://github.com/acme',
      'http://github.com/acme/atlas',
      'https://token@github.com/acme/atlas.git',
      'https://x:y@github.com/acme/atlas.git',
      'ssh://root@github.com/acme/atlas.git',
      'git://github.com/acme/atlas.git',
      'acme/.',
      'acme/..',
      '-acme/atlas',
      'acme/at las',
      'acme/atlas?x=1',
    ]) {
      expect(parseGitHubRepo(input), input).toBeNull();
    }
  });

  it('names a repository after its remote', () => {
    expect(repoNameFor('acme/atlas')).toBe('atlas');
    expect(repoNameFor('git@gitlab.com:acme/beacon.git')).toBe('beacon');
    expect(repoNameFor('/srv/git/beacon.git/')).toBe('beacon');
    expect(repoNameFor('')).toBe('');
  });
});
