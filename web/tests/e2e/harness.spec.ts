// Verify that scripted review verdicts still respect real check failures.
import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import type { JobDetail } from '../../src/lib/api/types.gen';

test('a failing check prevents the scripted reviewer from approving', async ({ api, hub }) => {
  const repo = join(hub.dataDir, 'fixtures', 'src', 'atlas');
  writeFileSync(join(repo, 'session', 'failure_test.go'), 'package session\nimport "testing"\nfunc TestInjectedFailure(t *testing.T) { t.Fatal("browser regression sentinel") }\n');
  const git = (...args: string[]) => execFileSync('git', ['-C', repo, ...args], { env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null' } });
  git('add', 'session/failure_test.go');
  git('-c', 'user.name=Browser test', '-c', 'user.email=test@yip.invalid', 'commit', '-m', 'Inject a failing check');
  git('push', join(hub.dataDir, 'fixtures', 'atlas.git'), 'main');
  const job = await api.atlasFix({ until: 'created' });
  await expect.poll(async () => {
    const detail = await api.req<JobDetail>('GET', `/v1/jobs/${job.id}`);
    return detail.reviews.map((review) => review.state);
  }, { timeout: 20_000 }).toContain('unable_to_review');
  const detail = await api.req<JobDetail>('GET', `/v1/jobs/${job.id}`);
  expect(detail.reviews.some((review) => review.state === 'approved')).toBe(false);
  expect(detail.checks.some((check) => !check.passed)).toBe(true);
});
