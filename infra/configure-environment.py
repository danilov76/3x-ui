#!/usr/bin/env python3
"""Upload local protected files to environment secrets; never display their contents."""
import argparse
import json
from pathlib import Path
import re
import subprocess


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--repo', required=True)
    p.add_argument('--environment', required=True)
    p.add_argument('--config', required=True, type=Path)
    p.add_argument('--key', required=True, type=Path)
    p.add_argument('--known-hosts', required=True, type=Path)
    a = p.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', a.repo) or not re.fullmatch(r'[A-Za-z0-9_-]+', a.environment):
        raise ValueError('Invalid repository or environment')
    def gh(*args, data=None):
        return subprocess.run(['gh', *args], input=data, capture_output=True, check=True).stdout
    branch = json.loads(gh('api', 'repos/' + a.repo))['default_branch']
    endpoint = 'repos/' + a.repo + '/environments/' + a.environment
    gh('api', '-X', 'PUT', endpoint, '--input', '-', data=json.dumps({
        'deployment_branch_policy': {'protected_branches':False, 'custom_branch_policies':True}}).encode())
    policies = json.loads(gh('api', endpoint + '/deployment-branch-policies'))['branch_policies']
    # Refuse unexpected policies instead of silently granting extra branches/tags.
    if any(x['name'] != branch or x.get('type', 'branch') != 'branch' for x in policies):
        raise ValueError('Environment permits unexpected refs')
    if not policies:
        gh('api', '-X', 'POST', endpoint + '/deployment-branch-policies', '--input', '-',
           data=json.dumps({'name':branch, 'type':'branch'}).encode())
    for name, path in [('TARGET_CONFIG',a.config),('SSH_PRIVATE_KEY',a.key),('SSH_KNOWN_HOSTS',a.known_hosts)]:
        gh('secret','set',name,'--repo',a.repo,'--env',a.environment,data=path.read_bytes())
    print('Environment configured; only the default branch can deploy.')

if __name__ == '__main__':
    try: main()
    except Exception:
        print('Environment setup failed; no credentials printed.')
        raise SystemExit(1)
