#!/usr/bin/env python3
"""Download the latest encrypted backup over verified SSH, without printing the host."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--ssh-host', required=True, help='Trusted ~/.ssh/config alias')
    p.add_argument('--destination', required=True, type=Path)
    a = p.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.-]*', a.ssh_host):
        raise ValueError('Use an SSH alias')
    os.umask(0o077)
    a.destination.mkdir(parents=True, exist_ok=True, mode=0o700)
    a.destination.chmod(0o700)
    script = """import pathlib,hashlib,json
files=sorted(pathlib.Path('/var/backups/3x-ui-infra').glob('*.tar.gz.age'))
if not files: raise SystemExit(1)
p=files[-1]
with p.open('rb') as f: h=hashlib.file_digest(f,'sha256').hexdigest()
print(json.dumps({'name':p.name,'sha256':h}))
"""
    r = subprocess.run(['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                        '-o', 'ConnectTimeout=15', a.ssh_host, 'python3 -'],
                       input=script.encode(), capture_output=True, check=True, timeout=120)
    meta = json.loads(r.stdout)
    name = meta['name']
    if not re.fullmatch(r'[0-9]+\.tar\.gz\.age', name) or not re.fullmatch('[a-f0-9]{64}', meta['sha256']):
        raise ValueError('Invalid metadata')
    dest = a.destination / name
    with tempfile.TemporaryDirectory(dir=a.destination) as temp:
        file = Path(temp) / name
        subprocess.run(['scp', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                        a.ssh_host + ':/var/backups/3x-ui-infra/' + name, str(file)],
                       check=True, capture_output=True, timeout=900)
        with file.open('rb') as f:
            if hashlib.file_digest(f, 'sha256').hexdigest() != meta['sha256']:
                raise ValueError('Checksum mismatch')
        if dest.exists():
            with dest.open('rb') as f:
                if hashlib.file_digest(f, 'sha256').hexdigest() != meta['sha256']:
                    raise ValueError('Local backup already exists with different contents')
        else:
            os.replace(file, dest)
        dest.chmod(0o600)
    (a.destination / (name + '.sha256')).write_text(meta['sha256'] + '  ' + name + '\n')
    print('Encrypted backup downloaded and SHA256 verified.')


if __name__ == '__main__':
    try: main()
    except Exception:
        print('Backup download failed; no connection details or credentials logged.')
        raise SystemExit(1)
