# VK / Telemost tunnel management

This fork adds a **Tunnels** page to 3x-ui **v3.8.5**. It manages one VK pair
and one Telemost pair: the current panel is the client, and an existing node
is the remote server.

## Features

- Attach existing `turnrelay-vk` and `olcrtc-telemost` services.
- Install a new pair using a preinstalled, checksum-pinned tunnel binary bundle.
- Inspect service status on both endpoints and check HTTPS egress through SOCKS.
- Create a SOCKS outbound after a successful connection check.
- Replace the call link and restart the corresponding services.

The feature does not import clients, edit inbound profiles, or select routes or
fallbacks. Routing remains under the usual 3x-ui controls. An existing outbound
with a conflicting configuration is rejected instead of overwritten.

## Requirements

Install this panel build on both endpoints. Remote node API access must use
HTTPS and an **admin** token; monitor and node-sync scopes are insufficient.
The service manager targets Linux amd64, systemd 247+, and a panel running as
root. Containers and non-systemd platforms are not supported by this feature.

The external tunnel engines are not Go dependencies and are not included in a
normal panel build. New installation requires all applicable binaries under
`/usr/local/share/x-ui-tunnels/`, matching the SHA256 values in
`internal/calltunnel/install.go`. Existing services can be attached without
reinstalling their engines. Stock upstream install/update scripts do not
provision this bundle.

Supported layouts:

| Provider | Service | Configuration |
| --- | --- | --- |
| VK | `turnrelay-vk.service` | `/etc/turnrelay-vk/config.json` |
| Telemost | `olcrtc-telemost.service` | `/etc/olcrtc/telemost.yaml` (JSON syntax) |

VK uses a pinned server certificate and shared secret. Telemost uses a shared
secret and the same call URL on both endpoints. Secrets are not returned by the
panel API. Firewall rules are not modified automatically.

## Build

Follow the upstream prerequisites (Go 1.27, Node 24, a C compiler for SQLite):

```bash
cd frontend
npm ci
npm run gen
npm run build
cd ..
go build -trimpath -ldflags='-s -w' -o x-ui .
```

Back up the database and current panel binary before deploying. Preserve the
installed Xray binary and existing service configuration. A normal upstream
panel update replaces this fork build and removes its added UI/API.

## Limits and verification

Service `active` does not prove connectivity; the separate check makes an HTTPS
request to `api.ipify.org`. It does not benchmark throughput or verify UDP.
Call creation and CAPTCHA handling are manual. Multiple pairs per provider,
service deletion, and key rotation are not implemented. A distributed install
can leave one endpoint installed if the second endpoint fails; it does not
overwrite that installation on retry. Use an independent control path where
possible, since restarting the tunnel carrying the node API can interrupt it.

Validated against v3.8.5: 1237 frontend unit tests, 283 component tests, Go tests
for the changed packages, race checks for the tunnel manager and node RPC,
frontend type checking/lint/build, and Go lint. Existing-service attachment and
HTTPS egress were also checked on live SQLite and PostgreSQL deployments.
Fresh two-server provisioning and the complete Storybook/CI suite have not
been validated. This is a downstream extension, not an upstream release.
