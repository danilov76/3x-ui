package calltunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var binaryHashes = map[string]string{
	"turnrelay-server": "7d97c845e73ac8cb125a919f3d6871a264be0bb87386e774f5b7383a8c9ed7e7",
	"turnrelay-proxy":  "fc55c5d67b424436ab105bd753764697d382e42c4650a7003a236d8d1ec58f11",
	"olcrtc":           "7b356a7bacd500f507bc027be62f258fd1fe14ddba78904bc0f0c4f3cddfd694",
}

const launcher = `import os,json
from pathlib import Path
c=Path(os.environ['CREDENTIALS_DIRECTORY'])
os.environ['TURNRELAY_PASSWORD']=(c/'secret').read_text().strip()
a=json.loads((c/'config').read_text())['args']
os.execv(a[0],a)
`

func (m *Manager) Install(ctx context.Context, p string, r Request) error {
	return m.install(ctx, p, r, false)
}

func (m *Manager) Preflight(ctx context.Context, p string, r Request) error {
	return m.install(ctx, p, r, true)
}

func (m *Manager) install(ctx context.Context, p string, r Request, validateOnly bool) error {
	if err := validateInstall(p, r); err != nil {
		return err
	}
	if os.Geteuid() != 0 && m.Root == "" {
		return errors.New("installation requires a root panel service")
	}
	s, ok := m.spec(p)
	if !ok {
		return errors.New("invalid tunnel instance")
	}
	for _, path := range []string{s.config, s.secret, "/etc/systemd/system/" + s.unit} {
		if _, err := os.Lstat(m.path(path)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing installation detected; use Attach existing")
		}
	}
	version, err := m.Run(ctx, "systemctl", "--version")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(version))
	if len(fields) < 2 {
		return errors.New("systemd unavailable")
	}
	v, _ := strconv.Atoi(fields[1])
	if v < 247 {
		return errors.New("systemd 247 or newer is required")
	}
	binary := filepath.Base(s.binary)
	if p == "vk" && r.Role == "server" {
		binary = "turnrelay-server"
	}
	src := m.path("/usr/local/share/x-ui-tunnels/" + binary)
	data, err := os.ReadFile(src)
	if err != nil {
		return errors.New("install the x-ui-tunnels binary bundle on this server first")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != binaryHashes[binary] {
		return errors.New("tunnel binary checksum mismatch")
	}
	if p == "vk" && r.Role == "server" {
		for _, dir := range []string{"/var/lib/" + m.stateName(), "/var/lib/private/" + m.stateName()} {
			if _, e := os.Lstat(m.path(dir)); !errors.Is(e, os.ErrNotExist) {
				return errors.New("existing VK certificate state detected")
			}
		}
	}
	if r.Role == "client" {
		listener, e := new(net.ListenConfig).Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port)))
		if e != nil {
			return errors.New("local SOCKS port is busy")
		}
		listener.Close()
	} else if p == "vk" {
		listener, e := new(net.ListenConfig).ListenPacket(ctx, "udp", fmt.Sprintf(":%d", r.ServerPort))
		if e != nil {
			return errors.New("server UDP port is busy")
		}
		listener.Close()
	}
	if validateOnly {
		return nil
	}
	for _, dir := range []string{filepath.Dir(s.config), filepath.Dir(s.binary), "/etc/systemd/system"} {
		target := m.path(dir)
		if fi, e := os.Lstat(target); e == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			return errors.New("unsafe installation directory")
		}
		if e := os.MkdirAll(target, 0o755); e != nil {
			return e
		}
	}
	if err = os.Chmod(m.path(filepath.Dir(s.binary)), 0o755); err != nil {
		return err
	}
	if err = os.Chmod(m.path(filepath.Dir(s.config)), 0o700); err != nil {
		return err
	}
	var config map[string]any
	unit := "[Unit]\nDescription=3x-ui managed call tunnel\nAfter=network-online.target\nWants=network-online.target\n[Service]\nType=simple\nDynamicUser=yes\nRestart=always\nRestartSec=10\nTimeoutStopSec=10\nNoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nMemoryMax=384M\nCPUWeight=10\nRestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK\n"
	if p == "vk" {
		a := []string{filepath.Join(filepath.Dir(s.binary), "turnrelay-server"), "-listen", fmt.Sprintf(":%d", r.ServerPort), "-mode", "srtp", "-cert", "/var/lib/" + m.stateName() + "/cert.pem", "-max-streams", "128"}
		if r.Role == "client" {
			a = []string{s.binary, "-server", net.JoinHostPort(r.Address, strconv.Itoa(r.ServerPort)), "-links", r.Room, "-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.Port)), "-connections", "2", "-server-fingerprint", r.Fingerprint, "-stats", "60s"}
		}
		config = map[string]any{"args": a}
		if err = m.write(filepath.Join(filepath.Dir(s.binary), "launch.py"), []byte(launcher), 0o644); err != nil {
			return err
		}
		unit += "StateDirectory=" + m.stateName() + "\nLoadCredential=secret:" + s.secret + "\nLoadCredential=config:" + s.config + "\nExecStart=/usr/bin/python3 " + filepath.Join(filepath.Dir(s.binary), "launch.py") + "\n"
	} else {
		mode := "srv"
		if r.Role == "client" {
			mode = "cnc"
		}
		config = map[string]any{"mode": mode, "auth": map[string]any{"provider": "telemost"}, "room": map[string]any{"id": r.Room}, "crypto": map[string]any{"key_file": "/run/credentials/" + s.unit + "/crypto.key"}, "net": map[string]any{"transport": "vp8channel", "dns": resolver()}, "vp8": map[string]any{"fps": 30, "batch_size": 64}, "debug": false}
		if r.Role == "client" {
			config["socks"] = map[string]any{"host": "127.0.0.1", "port": r.Port}
		}
		unit += "LoadCredential=crypto.key:" + s.secret + "\nLoadCredential=config:" + s.config + "\nExecStart=" + s.binary + " /run/credentials/" + s.unit + "/config\n"
	}
	unit += "[Install]\nWantedBy=multi-user.target\n"
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	for _, f := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{filepath.Join(filepath.Dir(s.binary), binary), data, 0o755}, {s.secret, []byte(r.Secret), 0o600}, {s.config, b, 0o600}, {"/etc/systemd/system/" + s.unit, []byte(unit), 0o644},
	} {
		if err = m.write(f.path, f.data, f.mode); err != nil {
			return errors.New("installation incomplete; inspect files before retry")
		}
	}
	if _, err = m.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err = m.Run(ctx, "systemctl", "enable", "--now", s.unit); err != nil {
		return errors.New("files installed but service failed to start; inspect system journal")
	}
	if p == "vk" && r.Role == "server" {
		for i := 0; i < 30; i++ {
			if _, e := m.fingerprint(); e == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return errors.New("VK service started but certificate is not ready")
	}
	return nil
}

func resolver() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "nameserver" && net.ParseIP(f[1]) != nil {
				return net.JoinHostPort(f[1], "53")
			}
		}
	}
	return "1.1.1.1:53"
}
