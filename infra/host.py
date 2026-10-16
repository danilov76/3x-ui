#!/usr/bin/env python3
"""Root-side operations. Never print configuration, URLs, credentials or subprocess output."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import sqlite3
import ssl
import subprocess
import tarfile
import tempfile
import time
import urllib.request

BASE = Path(__file__).resolve().parent
RESTORE_ROOT = Path('/')
DB = Path('/etc/x-ui/x-ui.db')
PANEL = Path('/usr/local/x-ui/x-ui')
XRAY = Path('/usr/local/x-ui/bin/xray-linux-amd64')
RELEASE = 'https://github.com/danilov76/3x-ui/releases/download/tunnels-3.8.5-patch4/3x-ui-tunnels-linux-amd64.tar.gz'
RELEASE_SHA = '427e6ef21351d51b7759cbae53c72953763ef2aa0234769f4bca7ce50f29d231'
PANEL_SHA = '5a7bf589f17888b621b1f8e4e47b461d667c07ac642d9323e1e8ccaa2fe06413'
ROOTS = ('etc/x-ui', 'usr/local/x-ui', 'usr/local/share/x-ui-tunnels',
         'etc/olcrtc', 'opt/olcrtc', 'etc/turnrelay-vk', 'opt/turnrelay-vk',
         'root/cert', 'root/.acme.sh')
UNITS = ('x-ui.service', 'olcrtc-telemost.service', 'turnrelay-vk.service')
EXACT = ('etc/default/x-ui', 'usr/bin/x-ui', 'usr/local/bin/x-ui', 'etc/cron.d/xui-infra-acme')
STATE_MEMBER = 'var/lib/3x-ui-infra/backup-state.json'


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, **kwargs)


def digest(path):
    with open(path, 'rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def allowed(name):
    p = PurePosixPath(name)
    if p.is_absolute() or '..' in p.parts or str(p) != name.rstrip('/'):
        return False
    if name == STATE_MEMBER:
        return True
    if name in EXACT or any(name == r or name.startswith(r + '/') for r in ROOTS):
        return True
    if re.fullmatch(r'(etc|opt)/xui-tunnel-(vk|telemost)-[a-f0-9]{16}(/.*)?', name):
        return True
    unit = name.removeprefix('etc/systemd/system/')
    return name.startswith('etc/systemd/system/') and (
        unit in UNITS or re.fullmatch(r'xui-tunnel-(vk|telemost)-[a-f0-9]{16}\.service', unit) is not None)


def validate_archive(t):
    seen = set()
    total = 0
    for m in t.getmembers():
        if m.name in seen or not allowed(m.name) or not (m.isfile() or m.isdir()):
            raise ValueError('Unsupported archive member')
        seen.add(m.name)
        total += m.size
        if total > 8 * 1024**3:
            raise ValueError('Archive too large')
    if not {'etc/x-ui/x-ui.db', 'usr/local/x-ui/x-ui', STATE_MEMBER}.issubset(seen):
        raise ValueError('Incomplete backup')


def units():
    return sorted(p.name for p in Path('/etc/systemd/system').glob('*.service')
                  if allowed('etc/systemd/system/' + p.name))


def settings():
    with sqlite3.connect(DB.as_uri() + '?mode=ro', uri=True) as c:
        if c.execute('pragma integrity_check').fetchone()[0] != 'ok':
            raise ValueError('Database integrity failed')
        return dict(c.execute('select key,value from settings'))


def health(config):
    s = settings()
    scheme = 'https' if s.get('webCertFile') else 'http'
    url = f"{scheme}://127.0.0.1:{int(s['webPort'])}/{s['webBasePath'].strip('/')}/"
    # Loopback only: no public TLS verification is disabled.
    op = urllib.request.build_opener(urllib.request.ProxyHandler({}),
            urllib.request.HTTPSHandler(context=ssl._create_unverified_context()))
    for _ in range(15):
        try:
            run('systemctl', 'is-active', '--quiet', 'x-ui')
            with op.open(url, timeout=3) as r:
                if r.status != 200:
                    raise ValueError('Panel status')
            break
        except Exception:
            time.sleep(2)
    else:
        raise ValueError('Panel check failed')
    run(str(XRAY), 'run', '-test', '-config', '/usr/local/x-ui/bin/config.json')
    for port in config.get('probe_socks_ports', []):
        port = int(port)
        if not 1 <= port <= 65535:
            raise ValueError('Invalid probe port')
        r = run('curl', '-q', '--noproxy', '', '--proxy', f'socks5h://127.0.0.1:{port}',
                '--connect-timeout', '10', '--max-time', '25', '-sS', '-o', '/dev/null',
                '-w', '%{http_code}', 'https://www.google.com/generate_204')
        if r.stdout != b'204':
            raise ValueError('Internet probe failed')


def backup(config):
    recipient = config['age_recipient']
    if not re.fullmatch(r'age1[0-9a-z]{58}', recipient):
        raise ValueError('Use an age X25519 recipient')
    folder = Path('/var/backups/3x-ui-infra')
    folder.mkdir(mode=0o700, parents=True, exist_ok=True)
    folder.chmod(0o700)
    result = folder / (str(time.time_ns()) + '.tar.gz.age')
    active = [u for u in units() if subprocess.run(['systemctl', 'is-active', '--quiet', u],
              capture_output=True).returncode == 0]
    # Stop only the panel to take a coherent config/DB snapshot; tunnel services can remain running.
    if 'x-ui.service' in active:
        run('systemctl', 'stop', 'x-ui')
    try:
        with tempfile.TemporaryDirectory(dir=folder) as d:
            archive = Path(d) / 'backup.tgz'
            paths = [Path('/') / r for r in ROOTS + EXACT]
            paths += list(Path('/etc').glob('xui-tunnel-*')) + list(Path('/opt').glob('xui-tunnel-*'))
            paths += [Path('/etc/systemd/system') / u for u in units()]
            state = Path(d) / 'backup-state.json'
            state.write_text(json.dumps({'active': active, 'enabled': [u for u in units() if subprocess.run(['systemctl', 'is-enabled', '--quiet', u], capture_output=True).returncode == 0]}))
            with tarfile.open(archive, 'w:gz', dereference=True) as t:
                t.add(state, arcname=STATE_MEMBER)
                for p in paths:
                    if p.exists() and allowed(str(p).lstrip('/')):
                        t.add(p, arcname=str(p).lstrip('/'))
            with tarfile.open(archive) as t:
                validate_archive(t)
            run('age', '-r', recipient, '-o', str(result), str(archive))
            result.chmod(0o600)
    except Exception:
        result.unlink(missing_ok=True)
        raise
    finally:
        if 'x-ui.service' in active:
            run('systemctl', 'start', 'x-ui')
    return result


def fresh(config):
    run('python3', str(BASE / 'vendor/install-fork.py'), '--host', config['public_host'],
        '--port', str(int(config.get('panel_port', 2053))))
    health(config)


def update(config):
    # Do not run migrations from a different panel version.
    if run(str(PANEL), '-v').stdout.strip() != b'3.8.5':
        raise ValueError('Only panel 3.8.5 is supported')
    if digest(PANEL) == PANEL_SHA:
        health(config)
        return
    backup(config)
    before = digest(XRAY)
    with tempfile.TemporaryDirectory(prefix='infra-update-') as d:
        archive = Path(d) / 'release.tgz'
        run('curl', '-q', '-fsSL', '--max-time', '300', '-o', str(archive), RELEASE)
        if digest(archive) != RELEASE_SHA:
            raise ValueError('Release checksum mismatch')
        with tarfile.open(archive) as t:
            for m in t.getmembers():
                if PurePosixPath(m.name).is_absolute() or '..' in PurePosixPath(m.name).parts or not (m.isfile() or m.isdir()):
                    raise ValueError('Invalid release')
            t.extractall(d, filter='data')
        release = Path(d) / '3x-ui-tunnels-release-3.8.5'
        s = settings()
        url = f"{'https' if s.get('webCertFile') else 'http'}://127.0.0.1:{int(s['webPort'])}/{s['webBasePath'].strip('/')}/"
        # The release installer backs up and rolls back the binary on a panel health failure.
        run('python3', str(release / 'deploy.py'), 'install', '--db', 'sqlite',
            '--health-url', url, '--loopback-insecure')
    if digest(XRAY) != before:
        raise ValueError('Xray changed')
    health(config)


def restore(config):
    if config.get('old_master_stopped') is not True:
        raise ValueError('Confirm old master stopped')
    if DB.exists() or PANEL.exists():
        raise ValueError('Restore requires a clean VPS')
    source = Path(config['restore_path'])
    if not source.is_absolute() or source.is_symlink() or not source.is_file():
        raise ValueError('Invalid encrypted backup path')
    if digest(source) != config['restore_sha256']:
        raise ValueError('Encrypted backup checksum mismatch')
    with tempfile.TemporaryDirectory(prefix='infra-restore-') as d:
        key = Path(d) / 'identity'
        key.write_text(config['age_identity'] + '\n'); key.chmod(0o600)
        archive = Path(d) / 'restore.tgz'
        run('age', '-d', '-i', str(key), '-o', str(archive), str(source))
        with tarfile.open(archive) as t:
            validate_archive(t)
            stage = Path(d) / 'stage'
            t.extractall(stage, filter='data')
        state = json.loads((stage / STATE_MEMBER).read_text())
        expected_units = {p.name for p in (stage / 'etc/systemd/system').glob('*.service')}
        if any(u not in expected_units for u in state['active'] + state['enabled']):
            raise ValueError('Invalid service state')
        # Validate DB and config before writing any application files.
        with sqlite3.connect(stage / 'etc/x-ui/x-ui.db') as c:
            if c.execute('pragma integrity_check').fetchone()[0] != 'ok':
                raise ValueError('Database integrity failed')
        files = [p for p in stage.rglob('*') if p.is_file() and str(p.relative_to(stage)) != STATE_MEMBER]
        for p in files:
            dst = RESTORE_ROOT / p.relative_to(stage)
            if dst.exists() or dst.is_symlink() or any(x.is_symlink() for x in dst.parents):
                raise ValueError('Restore destination exists')
        for p in files:
            dst = RESTORE_ROOT / p.relative_to(stage)
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(p, dst)
        # Preserve archive directory modes, including directories readable by DynamicUser services.
        for p in sorted((p for p in stage.rglob('*') if p.is_dir()), key=lambda p: len(p.parts), reverse=True):
            dst = RESTORE_ROOT / p.relative_to(stage)
            if dst.is_dir():
                dst.chmod(p.stat().st_mode & 0o777)
    (RESTORE_ROOT / 'var/log/x-ui').mkdir(mode=0o750, parents=True, exist_ok=True)
    try:
        run(str(XRAY), 'run', '-test', '-config', '/usr/local/x-ui/bin/config.json')
        run('systemctl', 'daemon-reload')
        for u in sorted(units(), key=lambda x: x == 'x-ui.service'):
            if u in state['enabled']:
                run('systemctl', 'enable', u)
            if u in state['active']:
                run('systemctl', 'start', u)
        health(config)
    except Exception:
        for u in units():
            subprocess.run(['systemctl', 'disable', '--now', u], capture_output=True)
        raise  # Keep files for diagnosis; never switch DNS or start the old master automatically.


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['check', 'install', 'update', 'backup', 'restore'])
    parser.add_argument('config', type=Path)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ValueError('Root required')
    with open('/run/lock/3x-ui-infra.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        config = json.loads(args.config.read_text())
        if args.action == 'check': health(config)
        elif args.action == 'backup':
            backup(config)
            health(config)
        elif args.action == 'install': fresh(config)
        elif args.action == 'update': update(config)
        elif args.action == 'restore': restore(config)
    print('Operation completed')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # No exception text: a subprocess, URL or config may contain credentials.
        print('Operation failed; inspect protected server state with SSH.')
        raise SystemExit(1)
