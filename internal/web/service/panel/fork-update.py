"""Embedded stable fork updater; never invokes the upstream installer."""
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import sqlite3
import subprocess
import tarfile
import tempfile
import time
import urllib.request

REPO = 'https://github.com/danilov76/3x-ui'
API = 'https://api.github.com/repos/danilov76/3x-ui/releases/latest'
ASSET = '3x-ui-tunnels-linux-amd64.tar.gz'
ROOT = '3x-ui-tunnels-release-3.8.5'


def release_asset(release):
    tag = release.get('tag_name', '')
    match = re.fullmatch(r'tunnels-3\.8\.5-patch([0-9]+)', tag)
    if not match or int(match[1]) < 5 or release.get('draft') or release.get('prerelease'):
        raise ValueError('No compatible stable fork release (patch5 or later)')
    assets = [a for a in release.get('assets', []) if a.get('name') == ASSET]
    if len(assets) != 1:
        raise ValueError('Missing fork archive')
    asset = assets[0]
    if asset.get('browser_download_url') != f'{REPO}/releases/download/{tag}/{ASSET}':
        raise ValueError('Unexpected release source')
    digest = asset.get('digest', '')
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', digest):
        raise ValueError('GitHub asset SHA256 is required')
    return asset['browser_download_url'], digest[7:]


def unpack(archive, dest):
    with tarfile.open(archive) as t:
        seen = set()
        total = 0
        for m in t.getmembers():
            p = Path(m.name)
            total += m.size
            if (p.is_absolute() or '..' in p.parts or not p.parts or p.parts[0] != ROOT
                    or not (m.isfile() or m.isdir()) or m.name in seen or total > 1024**3):
                raise ValueError('Unsafe release archive')
            seen.add(m.name)
        t.extractall(dest)


def main():
    os.umask(0o077)
    if os.geteuid() != 0 or platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise ValueError('Fork updates require root on Linux amd64')
    if os.environ.get('XUI_UPDATE_TAG'):
        raise ValueError('Only stable fork releases are supported')
    if os.environ.get('XUI_MAIN_FOLDER', '/usr/local/x-ui') != '/usr/local/x-ui':
        raise ValueError('Custom install directories require manual deployment')
    db = Path('/etc/x-ui/x-ui.db')
    # Refuse unsupported databases before downloading or stopping services.
    with sqlite3.connect(db.as_uri() + '?mode=ro', uri=True) as c:
        settings = dict(c.execute('select key,value from settings'))
    scheme = 'https' if settings.get('webCertFile') else 'http'
    port = int(settings['webPort'])
    if not 1 <= port <= 65535:
        raise ValueError('Invalid panel port')
    url = f"{scheme}://127.0.0.1:{port}/{settings['webBasePath'].strip('/')}/"
    with urllib.request.urlopen(API, timeout=30) as response:
        download, checksum = release_asset(json.load(response))
    with tempfile.TemporaryDirectory(prefix='xui-fork-update-') as d:
        archive = Path(d) / 'release.tgz'
        with urllib.request.urlopen(download, timeout=120) as response, archive.open('wb') as out:
            h = hashlib.sha256()
            size = 0
            while block := response.read(1024 * 1024):
                size += len(block)
                if size > 512 * 1024**2:
                    raise ValueError('Release archive too large')
                h.update(block)
                out.write(block)
        if h.hexdigest() != checksum:
            raise ValueError('Release checksum mismatch')
        unpack(archive, d)
        result = subprocess.run(['python3', str(Path(d) / ROOT / 'deploy.py'), 'install',
                                 '--db', 'sqlite', '--health-url', url, '--loopback-insecure'],
                                capture_output=True)
        if result.returncode:
            raise ValueError('Fork deployment failed; inspect local deployment backup')


def entrypoint():
    code = 1
    try:
        main()
        code = 0
    except Exception:
        # Never echo URLs, configuration or subprocess output into public logs.
        print('Fork update failed; existing update status reports failure', flush=True)
    finally:
        status = os.environ.get('XUI_UPDATE_STATUS_FILE')
        run = os.environ.get('XUI_UPDATE_RUN_ID', '')
        if status and run.isdigit():
            p = Path(status)
            temp = p.with_name(p.name + '.tmp.' + run)
            temp.write_text(json.dumps({'runId': run, 'state': 'success' if code == 0 else 'failed',
                                        'exitCode': code, 'finishedAt': int(time.time())}))
            temp.chmod(0o600)
            os.replace(temp, p)
    return code


if __name__ == '__main__':
    raise SystemExit(entrypoint())
