#!/usr/bin/env python3
"""Fresh Debian/Ubuntu installation of the pinned VK/Telemost fork bundle."""
import argparse
import hashlib
import ipaddress
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import sqlite3
import subprocess
import tarfile
import tempfile
import time

BASE = 'https://github.com/danilov76/3x-ui/releases/download/tunnels-3.8.5-patch2'
FORK_HASH = '947dadead4ad64fb58cda65f68a3bf8f9bb3d58c66fc486212dbb3464d618142'
UPSTREAM = 'https://github.com/MHSanaei/3x-ui/releases/download/v3.8.5/x-ui-linux-amd64.tar.gz'
UPSTREAM_HASH = '6a85c110a04a727613c933c54ae602b8d37dab8876c6e20a6d46623010dd9d3c'
PANEL_HASH = '27c5e084435952016674ed59c52aedb7b55162cf09da5a7d7dfa4d1951713f80'
DESTINATIONS = ['/usr/local/x-ui', '/etc/x-ui', '/usr/local/share/x-ui-tunnels',
                '/etc/systemd/system/x-ui.service', '/etc/default/x-ui', '/usr/bin/x-ui',
                '/etc/turnrelay-vk', '/etc/olcrtc']


def unpack(archive, target):
    with tarfile.open(archive) as t:
        for m in t.getmembers():
            if (m.name.startswith('/') or '..' in Path(m.name).parts
                    or not (m.isfile() or m.isdir())):
                raise ValueError('Unsafe archive member')
        t.extractall(target)


def host_value(value):
    try:
        ipaddress.ip_address(value)
    except ValueError:
        if not re.fullmatch(r'[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?', value):
            raise argparse.ArgumentTypeError('Use an IP address or DNS hostname')
    return value


def preflight():
    if os.geteuid() != 0:
        raise ValueError('Run as root')
    if os.uname().machine != 'x86_64':
        raise ValueError('Only Linux amd64 is supported')
    release = dict(line.split('=', 1) for line in Path('/etc/os-release').read_text().splitlines() if '=' in line)
    distro = release.get('ID', '').strip('"')
    major = int(release.get('VERSION_ID', '0').strip('"').split('.')[0])
    if not ((distro == 'ubuntu' and major >= 24) or (distro == 'debian' and major >= 12)):
        raise ValueError('Supported: Ubuntu 24.04+ or Debian 12+')
    if not Path('/run/systemd/system').is_dir():
        raise ValueError('A running systemd is required')
    for item in DESTINATIONS:
        if os.path.lexists(item):
            raise ValueError('Existing installation/configuration found: ' + item + '. Use the upgrade installer.')
    unit = subprocess.run(['systemctl', 'show', 'x-ui.service', '-p', 'LoadState', '--value'], capture_output=True, text=True)
    if unit.stdout.strip() not in ('', 'not-found'):
        raise ValueError('An x-ui service already exists')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--host', type=host_value, required=True, help='Public IP or DNS name for panel URL/certificate')
    p.add_argument('--port', type=int, default=2053)
    p.add_argument('--check', action='store_true', help='Read-only preflight; do not download or install')
    a = p.parse_args()
    if not 1024 <= a.port <= 65535:
        p.error('port must be 1024..65535')
    preflight()
    with socket.socket() as probe:
        probe.bind(('0.0.0.0', a.port))
    if a.check:
        print('Fresh-install preflight passed')
        return
    os.umask(0o077)
    logpath = Path('/var/log/3x-ui-fork-install-' + str(time.time_ns()) + '.log')
    with logpath.open('x') as log, tempfile.TemporaryDirectory(prefix='3x-ui-fork-') as tmp:
        stage = Path(tmp)
        def run(*args, cwd=None):
            subprocess.run(args, cwd=cwd, check=True, stdout=log, stderr=log,
                           env={'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'HOME': '/root', 'DEBIAN_FRONTEND': 'noninteractive'})
        def download(url, target, expected):
            run('curl', '-fL', '--retry', '3', '--connect-timeout', '15', '--max-time', '900', '-o', str(target), url)
            if hashlib.sha256(target.read_bytes()).hexdigest() != expected:
                raise ValueError('Download checksum mismatch')
        started = False
        try:
            print('Installing dependencies and downloading pinned artifacts...', flush=True)
            run('apt-get', 'update')
            run('apt-get', 'install', '-y', 'curl', 'ca-certificates', 'openssl', 'libc6', 'libsqlite3-0')
            download(BASE + '/3x-ui-tunnels-linux-amd64.tar.gz', stage/'fork.tgz', FORK_HASH)
            download(UPSTREAM, stage/'upstream.tgz', UPSTREAM_HASH)
            unpack(stage/'fork.tgz', stage/'fork')
            unpack(stage/'upstream.tgz', stage/'upstream')
            fork = stage/'fork/3x-ui-tunnels-release-3.8.5'
            upstream = stage/'upstream/x-ui'
            if hashlib.sha256((fork/'bin/x-ui').read_bytes()).hexdigest() != PANEL_HASH:
                raise ValueError('Panel checksum mismatch')
            run(str(fork/'bin/x-ui'), '-v')  # Verify local libc compatibility before creating destinations.
            if not (upstream/'bin/xray-linux-amd64').is_file():
                raise ValueError('Missing upstream Xray')
            preflight()  # Recheck before writing; never overwrite another installation.
            Path('/usr/local/x-ui').mkdir(mode=0o700)
            started = True
            shutil.copytree(upstream/'bin', '/usr/local/x-ui/bin')
            shutil.copy2(fork/'bin/x-ui', '/usr/local/x-ui/x-ui')
            shutil.copytree(fork/'bundle', '/usr/local/share/x-ui-tunnels')
            Path('/etc/x-ui').mkdir(mode=0o700)
            cert = Path('/etc/x-ui/panel.crt')
            key = Path('/etc/x-ui/panel.key')
            try:
                ipaddress.ip_address(a.host)
                san = 'IP:' + a.host
            except ValueError:
                san = 'DNS:' + a.host
            run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '365',
                '-subj', '/CN=' + a.host, '-addext', 'subjectAltName=' + san,
                '-keyout', str(key), '-out', str(cert))
            username, password, basepath = 'admin-' + secrets.token_hex(4), secrets.token_urlsafe(24), '/' + secrets.token_hex(12) + '/'
            run('/usr/local/x-ui/x-ui', 'setting', '-username', username, '-password', password,
                '-port', str(a.port), '-listenIP', '0.0.0.0', '-webBasePath', basepath,
                '-webCert', str(cert), '-webCertKey', str(key), cwd='/usr/local/x-ui')
            # Some upstream CLI setting errors do not produce a nonzero exit code.
            with sqlite3.connect('/etc/x-ui/x-ui.db') as db:
                settings = dict(db.execute('SELECT key,value FROM settings'))
                user = db.execute('SELECT username,password FROM users ORDER BY id LIMIT 1').fetchone()
                if not user or user[0] != username or not user[1] or user[1] == 'admin':
                    raise ValueError('Admin account verification failed')
                for k, v in {'webPort': str(a.port), 'webListen': '0.0.0.0', 'webBasePath': basepath,
                             'webCertFile': str(cert), 'webKeyFile': str(key)}.items():
                    if settings.get(k) != v:
                        raise ValueError('Panel settings verification failed: ' + k)
            host = '[' + a.host + ']' if ':' in a.host else a.host
            result = Path('/etc/x-ui/fork-install.txt')
            result.write_text('URL: https://' + host + ':' + str(a.port) + basepath + '\nUsername: ' + username + '\nPassword: ' + password + '\n')
            unit = Path('/etc/systemd/system/x-ui.service')
            unit.write_text('[Unit]\nDescription=3x-ui VK/Telemost fork\nAfter=network.target\n[Service]\nType=simple\nWorkingDirectory=/usr/local/x-ui\nExecStart=/usr/local/x-ui/x-ui\nRestart=on-failure\nRestartSec=5\n[Install]\nWantedBy=multi-user.target\n')
            unit.chmod(0o644)
            run('systemctl', 'daemon-reload')
            run('systemctl', 'enable', '--now', 'x-ui')
            for attempt in range(30):
                # Certificate is pinned locally; hostname override preserves validation.
                check = subprocess.run(['curl', '-fsS', '--noproxy', '*', '--cacert', str(cert),
                    '--connect-to', f'{host}:{a.port}:127.0.0.1:{a.port}', '--max-time', '2',
                    'https://' + host + ':' + str(a.port) + basepath], stdout=subprocess.DEVNULL, stderr=log)
                if check.returncode == 0:
                    run('systemctl', 'is-active', '--quiet', 'x-ui')
                    break
                time.sleep(1)
            else:
                raise ValueError('Panel health check failed')
            print('Installed. Credentials: /etc/x-ui/fork-install.txt (root only).')
            print('HTTPS uses a self-signed certificate. Configure a trusted certificate before connecting this panel as a node.')
            print('Allow TCP port ' + str(a.port) + ' in your firewall to access the panel. No VPN inbounds have been created.')
            print('Service: systemctl status x-ui. Do not use the upstream updater for this fork.')
        except Exception:
            if started:
                subprocess.run(['systemctl', 'disable', '--now', 'x-ui'], stdout=log, stderr=log)
            print('Installation failed. Private diagnostic log: ' + str(logpath))
            print('Partial files are preserved; the panel is stopped. Do not remove existing files blindly.')
            raise


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError:
        raise SystemExit('A setup command failed; see the private installation log.')
    except Exception as e:
        raise SystemExit(str(e))
