#!/usr/bin/env bash
# Independent 3x-ui fork installer. Original project: MHSanaei/3x-ui (GPL-3.0).
set -Eeuo pipefail
umask 077
if [[ ${1:-} == --help || $# == 0 ]]; then
    echo 'Usage: sudo bash install-fork.sh --host SERVER_IP_OR_DOMAIN [--port 2053]'
    echo 'Fresh Ubuntu 24.04+ / Debian 12+ amd64 only. Existing installations are refused.'
    exit 0
fi
[[ $EUID == 0 ]] || { echo 'Run as root' >&2; exit 1; }
for path in /usr/local/x-ui /etc/x-ui /usr/local/share/x-ui-tunnels /etc/systemd/system/x-ui.service /etc/default/x-ui /usr/bin/x-ui /etc/turnrelay-vk /etc/olcrtc; do
    [[ ! -e "$path" && ! -L "$path" ]] || { echo "Existing installation: $path. Use the upgrade installer." >&2; exit 1; }
done
[[ $(uname -m) == x86_64 && -d /run/systemd/system ]] || { echo 'Linux amd64 with running systemd required' >&2; exit 1; }
# os-release contains OS metadata, not credentials.
. /etc/os-release
case "$ID:${VERSION_ID%%.*}" in
    ubuntu:24|ubuntu:25|ubuntu:26|debian:12|debian:13) ;;
    *) echo 'Supported: Ubuntu 24.04–26.x or Debian 12–13' >&2; exit 1 ;;
esac
if ! command -v python3 >/dev/null || ! command -v curl >/dev/null || [[ ! -s /etc/ssl/certs/ca-certificates.crt ]]; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y python3 curl ca-certificates
fi
task_tmp=$(mktemp -d)
trap 'rm -rf -- "$task_tmp"' EXIT
curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
    https://github.com/danilov76/3x-ui/releases/download/tunnels-3.8.5-patch2/install-fork.py \
    -o "$task_tmp/install-fork.py"
printf '%s  %s\n' '4fa166c89ace57754f944de18d110fec075005c2b86d0620a0c559d87bd36ae8' "$task_tmp/install-fork.py" | sha256sum -c -
python3 "$task_tmp/install-fork.py" "$@"
