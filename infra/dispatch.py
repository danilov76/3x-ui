#!/usr/bin/env python3
"""Build an ephemeral Ansible inventory from GitHub environment secrets."""
import ipaddress
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def validate(c):
    host = c['host']
    try:
        ipaddress.ip_address(host)
    except ValueError:
        if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{0,252}', host):
            raise ValueError('Invalid host')
    if c.get('user', 'root') != 'root':
        raise ValueError('Dedicated root deployment key required')
    port = int(c.get('ssh_port', 22))
    if not 1 <= port <= 65535:
        raise ValueError('Invalid port')
    return host, port


def main():
    os.umask(0o077)
    action = os.environ['OPERATION']
    if action not in ('check', 'install', 'update', 'backup', 'restore'):
        raise ValueError('Invalid operation')
    config = json.loads(os.environ['TARGET_CONFIG'])
    host, port = validate(config)
    if action == 'restore':
        config['old_master_stopped'] = os.environ.get('OLD_MASTER_STOPPED') == 'true'
        config['age_identity'] = os.environ['AGE_IDENTITY']
    with tempfile.TemporaryDirectory(prefix='xui-dispatch-') as d:
        root = Path(d)
        (root / 'key').write_text(os.environ['SSH_PRIVATE_KEY'] + '\n')
        (root / 'known_hosts').write_text(os.environ['SSH_KNOWN_HOSTS'] + '\n')
        (root / 'key').chmod(0o600)
        inventory = {'all': {'hosts': {'target': {
            'ansible_host': host, 'ansible_port': port, 'ansible_user': 'root',
            'ansible_ssh_private_key_file': str(root / 'key'),
            'ansible_ssh_common_args': '-o StrictHostKeyChecking=yes -o UserKnownHostsFile=' + str(root / 'known_hosts'),
            'ansible_python_interpreter': '/usr/bin/python3'}}}}
        (root / 'inventory.json').write_text(json.dumps(inventory))
        (root / 'vars.json').write_text(json.dumps({'operation': action, 'host_config': config}))
        r = subprocess.run(['ansible-playbook', '-i', str(root / 'inventory.json'),
                            str(Path(__file__).with_name('playbook.yml')), '-e', '@' + str(root / 'vars.json')],
                           stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        # Entire Ansible output stays private: unreachable/SSH errors can reveal an IP or user.
        if r.returncode:
            raise ValueError('Operation failed')
    print('Deployment operation passed. No configuration or credentials logged.')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Deployment operation failed. Check environment secrets and server state via SSH.')
        raise SystemExit(1)
