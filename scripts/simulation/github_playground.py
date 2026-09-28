#!/usr/bin/env python3
"""Explicitly opted-in live GitHub fixtures for yip's simulation tests.

Uses gh's named accounts without changing the active account. Tokens stay in
subprocess environments, never in the state file or printed command lines.
Only the repositories listed below may be mutated. Retains fixtures as evidence.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import time

OWNER = 'binbandit'
REVIEWERS = ('sage-smudge', 'WastedHippie')
PRIVATE = OWNER + '/yip-simulation-playground'
PUBLIC = OWNER + '/yip-simulation-public'
FORK = REVIEWERS[0] + '/yip-simulation-public'
REPOS = {PRIVATE, PUBLIC, FORK}
ROOT = Path(__file__).resolve().parents[2]
CACHE = {'GOMODCACHE': '/private/tmp/yip-evaluation-go-mod',
         'GOCACHE': '/private/tmp/yip-evaluation-go-cache', 'GOPROXY': 'off'}


class Playground:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self.state_path = self.directory / 'state.json'
        self.state = json.loads(self.state_path.read_text()) if self.state_path.exists() else {'prs': {}}
        self.tokens = {}

    def env(self, actor):
        if actor not in (OWNER, *REVIEWERS):
            raise ValueError('Not a simulation account')
        if actor not in self.tokens:
            self.tokens[actor] = subprocess.check_output(
                ['gh', 'auth', 'token', '--hostname', 'github.com', '--user', actor], text=True).strip()
        return {**os.environ, 'GH_TOKEN': self.tokens[actor]}

    def api(self, actor, method, path, body=None, allow_error=False):
        if path.startswith('repos/') and '/'.join(path.split('/')[1:3]) not in REPOS:
            raise ValueError('Refusing to access a non-playground repository')
        argv = ['gh', 'api', '--method', method, '-H', 'X-GitHub-Api-Version: 2022-11-28', path]
        if body is not None:
            argv += ['--input', '-']
        result = subprocess.run(argv, input=json.dumps(body) if body is not None else None,
                                text=True, capture_output=True, env=self.env(actor), timeout=60)
        data = json.loads(result.stdout) if result.stdout.strip() else None
        if result.returncode and not allow_error:
            raise RuntimeError(f'{method} {path}: {data or result.stderr}')
        return result.returncode, data

    def save(self):
        self.state_path.write_text(json.dumps(self.state, indent=2) + '\n')

    def commit(self, repo, branch, files, message, actor=OWNER, base='main'):
        _, ref = self.api(actor, 'GET', f'repos/{repo}/git/ref/heads/{base}')
        parent = ref['object']['sha']
        _, commit = self.api(actor, 'GET', f'repos/{repo}/git/commits/{parent}')
        _, tree = self.api(actor, 'POST', f'repos/{repo}/git/trees', {
            'base_tree': commit['tree']['sha'], 'tree': [
                {'path': path, 'mode': '100644', 'type': 'blob', 'content': content}
                for path, content in files.items()]})
        _, commit = self.api(actor, 'POST', f'repos/{repo}/git/commits',
                             {'message': message, 'tree': tree['sha'], 'parents': [parent]})
        if branch == base:
            self.api(actor, 'PATCH', f'repos/{repo}/git/refs/heads/{branch}', {'sha': commit['sha'], 'force': False})
        else:
            self.api(actor, 'POST', f'repos/{repo}/git/refs', {'ref': 'refs/heads/' + branch, 'sha': commit['sha']})
        return commit['sha']

    def pr(self, key, repo, branch, title, actor=OWNER, draft=False, base='main'):
        if key in self.state['prs']:
            return self.state['prs'][key]
        _, pr = self.api(actor, 'POST', f'repos/{repo}/pulls', {
            'title': '[yip simulation] ' + title, 'head': branch, 'base': base, 'draft': draft,
            'body': 'Synthetic acceptance test for yip. This PR, its reviews and checks are intentional simulation activity. No production code or data.\n\nScenario: ' + title})
        self.state['prs'][key] = {'repo': repo, 'number': pr['number'], 'url': pr['html_url'], 'head': pr['head']['sha'], 'branch': branch}
        self.save()
        print('Created ' + pr['html_url'], flush=True)
        return self.state['prs'][key]

    def seed(self):
        readme = '# yip simulation playground\n\nSynthetic data only. All reviews, checks, conflicts and failures are intentional acceptance-test activity.\n'
        files = {
            'session.py': 'def valid(now, expires):\n    return now <= expires\n',
            'tests/test_session.py': 'import unittest\nfrom session import valid\n\nclass SessionTests(unittest.TestCase):\n    def test_before_expiry(self):\n        self.assertTrue(valid(9, 10))\n    def test_after_expiry(self):\n        self.assertFalse(valid(11, 10))\n',
            '.github/CODEOWNERS': '* @sage-smudge @WastedHippie\n',
            '.github/workflows/test.yml': 'name: Simulation CI\non: [push, pull_request]\npermissions:\n  contents: read\njobs:\n  unit:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - run: python3 -m unittest discover -s tests -v\n',
            'docs/rollout.md': '# Rollout\n\nSynthetic service. Release to a test environment after two independent reviews and passing checks.\n'}
        for repo in (PRIVATE, PUBLIC):
            code, _ = self.api(OWNER, 'GET', f'repos/{repo}/git/ref/heads/main', allow_error=True)
            if code:
                self.api(OWNER, 'PUT', f'repos/{repo}/contents/README.md', {
                    'message': 'test: initialize synthetic yip playground', 'content': base64.b64encode(readme.encode()).decode(), 'branch': 'main'})
                self.commit(repo, 'main', files, 'test: add synthetic service and CI fixture')
            for actor in REVIEWERS:
                _, invitation = self.api(OWNER, 'PUT', f'repos/{repo}/collaborators/{actor}', {'permission': 'push'})
                if invitation and invitation.get('id'):
                    self.api(actor, 'PATCH', f'user/repository_invitations/{invitation["id"]}')
        self.state['repositories'] = [PRIVATE, PUBLIC]
        self.save()
        if 'private-fix' not in self.state['prs']:
            self.commit(PRIVATE, 'simulation/private-fix', {'session.py': 'def valid(now, expires):\n    return now < expires\n'}, 'fix: reject the exact expiry boundary')
            self.pr('private-fix', PRIVATE, 'simulation/private-fix', 'Solo private project: expiry correction')
        if 'team-fix' not in self.state['prs']:
            self.commit(PUBLIC, 'simulation/team-fix', {'session.py': 'def valid(now, expires):\n    return now < expires\n'}, 'fix: reject the exact expiry boundary')
            self.pr('team-fix', PUBLIC, 'simulation/team-fix', 'Platform team: protected expiry correction')
        self.save()

    def check(self, label, key, actor=OWNER, **expected):
        pr = self.state['prs'][key]
        env = {**self.env(actor), **CACHE, 'YIP_GITHUB_TOKEN': self.tokens[actor],
               'YIP_GITHUB_REPO': pr['repo'], 'YIP_GITHUB_PR': str(pr['number'])}
        env.update({'YIP_GITHUB_SIM_' + k.upper(): str(v).lower() if isinstance(v, bool) else str(v) for k, v in expected.items()})
        log = self.directory / (label + '.log')
        with log.open('w') as output:
            result = subprocess.run(['go', 'test', '-count=1', '-timeout=4m', '-v', '-run', '^TestSimulationGitHub$', './internal/forge/github'],
                                    cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=260)
        record = {'label': label, 'actor': actor, 'pr': pr['url'], 'passed': result.returncode == 0, 'log': str(log), 'expected': expected}
        self.state.setdefault('checks', []).append(record)
        self.save()
        print(('PASS ' if record['passed'] else 'FAIL ') + label, flush=True)
        if not record['passed']:
            print(log.read_text(), flush=True)
        return record['passed']

    def private_campaign(self):
        pr = self.state['prs']['private-fix']
        path = f'repos/{PRIVATE}/pulls/{pr["number"]}'
        marker = 'simulation-pending-20260928'
        _, draft = self.api(REVIEWERS[1], 'POST', path + '/reviews', {
            'commit_id': pr['head'], 'body': 'Unsubmitted simulation review. <!-- yip-review:' + marker + ' -->'})
        self.check('private-pending-is-not-published', 'private-fix', actor=REVIEWERS[1], expect_marker_absent=marker)
        self.api(REVIEWERS[1], 'POST', path + f'/reviews/{draft["id"]}/events', {
            'event': 'COMMENT', 'body': 'Submitted simulation review. <!-- yip-review:' + marker + ' -->'})
        self.check('private-submitted-marker-reconciles', 'private-fix', actor=REVIEWERS[1], event='COMMENT', marker=marker)
        self.check('private-copied-marker-other-actor', 'private-fix', actor=REVIEWERS[0], expect_marker_absent=marker)
        old = pr['head']
        new = self.commit(PRIVATE, pr['branch'], {
            'tests/test_boundary.py': 'import unittest\nfrom session import valid\n\nclass BoundaryTests(unittest.TestCase):\n    def test_at_expiry(self):\n        self.assertFalse(valid(10, 10))\n'},
            'test: cover the exact expiry boundary after review', base=pr['branch'])
        pr['head'] = new
        self.save()
        self.check('private-stale-review-refused', 'private-fix', actor=REVIEWERS[0], event='APPROVE', commit=old, expect_error='stale', expect_head=new)
        self.check('private-rereview-approval', 'private-fix', actor=REVIEWERS[0], event='APPROVE', expect_head=new)
        self.check('private-second-reviewer', 'private-fix', actor=REVIEWERS[1], event='APPROVE', expect_head=new)
        self.api(OWNER, 'DELETE', f'repos/{PRIVATE}/collaborators/{REVIEWERS[1]}')
        code, denied = self.api(REVIEWERS[1], 'GET', path, allow_error=True)
        self.state['private_revoked_access'] = {'denied': bool(code), 'status': denied.get('status') if denied else None}
        self.save()
        if not code:
            raise AssertionError('Removed private collaborator retained read access')
        _, invitation = self.api(OWNER, 'PUT', f'repos/{PRIVATE}/collaborators/{REVIEWERS[1]}', {'permission': 'push'})
        if invitation and invitation.get('id'):
            self.api(REVIEWERS[1], 'PATCH', f'user/repository_invitations/{invitation["id"]}')
        self.check('private-access-restored', 'private-fix', actor=REVIEWERS[1], expect_head=new)

    def status(self, repo, sha, state):
        self.api(OWNER, 'POST', f'repos/{repo}/statuses/{sha}', {
            'state': state, 'context': 'simulation/security',
            'description': 'Injected simulation state; this is not a security scan.'})

    def team_campaign(self):
        self.api(OWNER, 'PUT', f'repos/{PUBLIC}/branches/main/protection', {
            'required_status_checks': {'strict': True, 'contexts': ['unit', 'simulation/security']},
            'enforce_admins': True, 'required_pull_request_reviews': {
                'dismiss_stale_reviews': True, 'require_code_owner_reviews': True,
                'required_approving_review_count': 2},
            'restrictions': None, 'required_conversation_resolution': True})
        pr = self.state['prs']['team-fix']
        self.status(PUBLIC, pr['head'], 'pending')
        self.check('team-required-check-pending', 'team-fix', expect_checks='pending', expect_merge='blocked')
        self.status(PUBLIC, pr['head'], 'failure')
        self.check('team-required-check-failed', 'team-fix', expect_checks='failure', expect_merge='blocked')
        self.status(PUBLIC, pr['head'], 'success')
        self.check('team-green-without-approvals', 'team-fix', expect_checks='success', expect_merge='blocked')
        self.check('team-first-reviewer', 'team-fix', actor=REVIEWERS[0], event='APPROVE')
        self.check('team-one-review-is-not-two', 'team-fix', expect_merge='blocked')
        self.check('team-second-reviewer', 'team-fix', actor=REVIEWERS[1], event='APPROVE')
        self.check('team-policy-satisfied', 'team-fix', expect_checks='success', expect_merge='clean')
        old = pr['head']
        pr['head'] = self.commit(PUBLIC, pr['branch'], {
            'tests/test_boundary.py': 'import unittest\nfrom session import valid\n\nclass BoundaryTests(unittest.TestCase):\n    def test_at_expiry(self):\n        self.assertFalse(valid(10, 10))\n'},
            'test: add regression coverage requiring fresh reviews', base=pr['branch'])
        self.save()
        self.check('team-new-commit-dismisses-approvals', 'team-fix', expect_merge='blocked', expect_head=pr['head'])
        self.check('team-old-head-approval-refused', 'team-fix', actor=REVIEWERS[0], event='APPROVE', commit=old, expect_error='stale')

    def fork_campaign(self):
        code, _ = self.api(REVIEWERS[0], 'GET', f'repos/{FORK}', allow_error=True)
        if code:
            self.api(REVIEWERS[0], 'POST', f'repos/{PUBLIC}/forks', {'default_branch_only': True})
        for _ in range(15):
            code, _ = self.api(REVIEWERS[0], 'GET', f'repos/{FORK}/git/ref/heads/main', allow_error=True)
            if not code:
                break
            time.sleep(2)
        if 'fork-fix' not in self.state['prs']:
            self.commit(FORK, 'simulation/contributor', {'docs/contributing.md':
                '# Contributing\n\nRun python3 -m unittest discover -s tests -v before submitting a patch.\n'},
                'docs: document the contributor check command', actor=REVIEWERS[0])
            self.pr('fork-fix', PUBLIC, REVIEWERS[0] + ':simulation/contributor',
                    'Open source: fork contributor documentation', actor=REVIEWERS[0])
        self.check('fork-contributor-identity', 'fork-fix', actor=REVIEWERS[1], expect_author=REVIEWERS[0])
        self.check('fork-author-self-approval', 'fork-fix', actor=REVIEWERS[0], event='APPROVE', expect_error='ineligible')
        self.check('fork-maintainer-requests-changes', 'fork-fix', actor=OWNER, event='REQUEST_CHANGES')
        self.check('fork-distinct-peer-approval', 'fork-fix', actor=REVIEWERS[1], event='APPROVE')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['seed', 'private', 'team', 'fork'])
    parser.add_argument('--directory', required=True)
    args = parser.parse_args()
    playground = Playground(args.directory)
    {'seed': playground.seed, 'private': playground.private_campaign,
     'team': playground.team_campaign, 'fork': playground.fork_campaign}[args.command]()
