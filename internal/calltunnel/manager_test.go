package calltunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, p string) (*Manager, []byte) {
	t.Helper()
	m := New()
	m.Root = t.TempDir()
	m.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("active\n"), nil }
	var raw string
	if p == "vk" {
		raw = `{"args":["/opt/turnrelay-vk/turnrelay-proxy","-links","https://vk.ru/call/join/old","-listen","127.0.0.1:19094","-server-fingerprint","public-pin"],"other":"keep"}`
	} else {
		raw = `{"mode":"cnc","room":{"id":"https://telemost.360.yandex.ru/j/123"},"crypto":{"key_file":"/secret"},"socks":{"host":"127.0.0.1","port":19090}}`
	}
	path := m.path(specs[p].config)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return m, []byte(raw)
}

func TestCallTunnelRoomPreservesUnrelatedConfiguration(t *testing.T) {
	for _, p := range []string{"vk", "telemost"} {
		t.Run(p, func(t *testing.T) {
			m, old := fixture(t, p)
			url := "https://vk.ru/call/join/new"
			if p == "telemost" {
				url = "https://telemost.360.yandex.ru/j/456"
			}
			_, err := m.Action(context.Background(), p, "room", Request{Room: url})
			if err != nil {
				t.Fatal(err)
			}
			b, err := m.read(specs[p].config)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), url) {
				t.Fatal("new link missing")
			}
			var expected, actual map[string]any
			json.Unmarshal(old, &expected)
			json.Unmarshal(b, &actual)
			if p == "vk" {
				actual["args"].([]any)[2] = expected["args"].([]any)[2]
			} else {
				actual["room"] = expected["room"]
			}
			a, _ := json.Marshal(actual)
			e, _ := json.Marshal(expected)
			if string(a) != string(e) {
				t.Fatal("unrelated settings changed")
			}
			backups, _ := filepath.Glob(m.path(specs[p].config) + ".backup-*")
			if len(backups) != 1 {
				t.Fatal("missing rollback backup")
			}
			fi, _ := os.Stat(backups[0])
			if fi.Mode().Perm() != 0o600 {
				t.Fatal("backup exposes private settings")
			}
		})
	}
}

func TestCallTunnelRollbackOnRestartFailure(t *testing.T) {
	m, old := fixture(t, "vk")
	calls := 0
	m.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "restart" {
			calls++
			if calls == 1 {
				return nil, errors.New("failed")
			}
		}
		return []byte("active"), nil
	}
	_, err := m.Action(context.Background(), "vk", "room", Request{Room: "https://vk.ru/call/join/new"})
	if err == nil || !strings.Contains(err.Error(), "previous configuration restored") {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := m.read(specs["vk"].config)
	if string(b) != string(old) || calls != 2 {
		t.Fatal("rollback did not restore old config and service")
	}
}

func TestCallTunnelRejectsInjectionAndServerRoom(t *testing.T) {
	for _, v := range []string{"http://vk.ru/call/join/x", "https://evil.test/call/join/x", "https://vk.ru/call/join/x?token=x", "https://vk.ru/call/join/x;reboot", "https://u:p@vk.ru/call/join/x"} {
		if ValidateRoom("vk", v) == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	m, old := fixture(t, "vk")
	_, err := m.Action(context.Background(), "vk/../../evil", "restart", Request{})
	if err == nil {
		t.Fatal("accepted provider path")
	}
	b := strings.Replace(string(old), "turnrelay-proxy", "turnrelay-server", 1)
	os.WriteFile(m.path(specs["vk"].config), []byte(b), 0o600)
	_, err = m.Action(context.Background(), "vk", "room", Request{Room: "https://vk.ru/call/join/new"})
	if err == nil {
		t.Fatal("accepted server-side VK room")
	}
}

func TestCallTunnelStatusDoesNotExposeSecrets(t *testing.T) {
	m, _ := fixture(t, "telemost")
	st := m.Status(context.Background(), "telemost")
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "crypto") || strings.Contains(string(b), "/secret") {
		t.Fatal("status leaked private config")
	}
	if st.Role != "client" || st.Port != 19090 || st.State != "active" {
		t.Fatalf("wrong status: %+v", st)
	}
}

func TestCallTunnelRejectsSymlinkConfig(t *testing.T) {
	m, _ := fixture(t, "vk")
	p := m.path(specs["vk"].config)
	os.Remove(p)
	os.Symlink(filepath.Join(m.Root, "unrelated"), p)
	if _, err := m.Action(context.Background(), "vk", "room", Request{Room: "https://vk.ru/call/join/new"}); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestCallTunnelInstallAndRefuseOverwrite(t *testing.T) {
	for _, p := range []string{"vk", "telemost"} {
		t.Run(p, func(t *testing.T) {
			m := New()
			m.Root = t.TempDir()
			m.Run = func(_ context.Context, _ string, a ...string) ([]byte, error) {
				if a[0] == "--version" {
					return []byte("systemd 255"), nil
				}
				return []byte("active"), nil
			}
			binary := "olcrtc"
			if p == "vk" {
				binary = "turnrelay-proxy"
			}
			payload := []byte("test binary")
			sum := sha256.Sum256(payload)
			oldHash := binaryHashes[binary]
			binaryHashes[binary] = hex.EncodeToString(sum[:])
			t.Cleanup(func() { binaryHashes[binary] = oldHash })
			dest := m.path("/usr/local/share/x-ui-tunnels/" + binary)
			os.MkdirAll(filepath.Dir(dest), 0o755)
			os.WriteFile(dest, payload, 0o755)
			r := Request{Role: "client", Room: "https://vk.ru/call/join/test", Secret: strings.Repeat("ab", 32), Address: "203.0.113.1", Port: 19397, ServerPort: 56014, Fingerprint: strings.Repeat("cd", 32)}
			if p == "telemost" {
				r.Room = "https://telemost.360.yandex.ru/j/123"
			}
			if _, err := m.Action(context.Background(), p, "install", r); err != nil {
				t.Fatal(err)
			}
			st := m.Status(context.Background(), p)
			if st.Role != "client" || st.Port != r.Port || st.Room != r.Room {
				t.Fatalf("wrong installed config: %+v", st)
			}
			fi, _ := os.Stat(m.path(specs[p].secret))
			if fi.Mode().Perm() != 0o600 {
				t.Fatal("secret is not private")
			}
			old, _ := m.read(specs[p].config)
			if _, err := m.Action(context.Background(), p, "install", r); err == nil {
				t.Fatal("installation overwrote existing config")
			}
			b, _ := m.read(specs[p].config)
			if string(b) != string(old) {
				t.Fatal("config changed on refused installation")
			}
			unit, _ := os.ReadFile(m.path("/etc/systemd/system/" + specs[p].unit))
			if strings.Contains(string(unit), r.Secret) {
				t.Fatal("secret embedded in unit")
			}
			if p == "telemost" && !strings.Contains(string(unit), "ExecStart=/opt/olcrtc/olcrtc /run/credentials/") {
				t.Fatal("invalid olcrtc CLI")
			}
		})
	}
}

func TestMultipleInstancesStayIsolated(t *testing.T) {
	for _, provider := range []string{"vk", "telemost"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			binary := "olcrtc"
			if provider == "vk" {
				binary = "turnrelay-proxy"
			}
			payload := []byte("test binary")
			sum := sha256.Sum256(payload)
			oldHash := binaryHashes[binary]
			binaryHashes[binary] = hex.EncodeToString(sum[:])
			t.Cleanup(func() { binaryHashes[binary] = oldHash })
			dest := filepath.Join(root, "usr/local/share/x-ui-tunnels", binary)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, payload, 0o755); err != nil {
				t.Fatal(err)
			}
			var restarted []string
			managers := []*Manager{New(), NewInstance("first"), NewInstance("second")}
			for i, m := range managers {
				m.Root = root
				m.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
					if args[0] == "--version" {
						return []byte("systemd 255"), nil
					}
					if args[0] == "restart" {
						restarted = append(restarted, args[1])
					}
					return []byte("active"), nil
				}
				room := "https://vk.ru/call/join/old"
				if provider == "telemost" {
					room = "https://telemost.360.yandex.ru/j/123"
				}
				r := Request{Role: "client", Room: room, Secret: strings.Repeat("ab", 32), Address: "203.0.113.1", Port: 19400 + i, ServerPort: 56100 + i, Fingerprint: strings.Repeat("cd", 32)}
				if _, err := m.Action(context.Background(), provider, "install", r); err != nil {
					t.Fatal(err)
				}
				st := m.Status(context.Background(), provider)
				if st.InstanceID != m.InstanceID || st.Port != r.Port || st.Role != "client" {
					t.Fatalf("bad instance status: %+v", st)
				}
			}
			legacySpec, _ := managers[0].spec(provider)
			secondSpec, _ := managers[2].spec(provider)
			legacy, _ := managers[0].read(legacySpec.config)
			second, _ := managers[2].read(secondSpec.config)
			room := "https://vk.ru/call/join/new"
			if provider == "telemost" {
				room = "https://telemost.360.yandex.ru/j/456"
			}
			if _, err := managers[1].Action(context.Background(), provider, "room", Request{Room: room}); err != nil {
				t.Fatal(err)
			}
			if st := managers[1].Status(context.Background(), provider); st.Room != room {
				t.Fatal("selected room unchanged")
			}
			for _, check := range []struct {
				m *Manager
				s spec
				b []byte
			}{{managers[0], legacySpec, legacy}, {managers[2], secondSpec, second}} {
				now, _ := check.m.read(check.s.config)
				if string(now) != string(check.b) {
					t.Fatal("other instance configuration changed")
				}
			}
			wanted, _ := managers[1].spec(provider)
			if len(restarted) != 1 || restarted[0] != wanted.unit {
				t.Fatalf("restarted wrong services: %v", restarted)
			}
			unit, err := os.ReadFile(managers[1].path("/etc/systemd/system/" + wanted.unit))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(unit), wanted.config) || !strings.Contains(string(unit), filepath.Dir(wanted.binary)) {
				t.Fatal("unit points outside instance")
			}
		})
	}
}

func TestInvalidInstanceCannotAccessLegacy(t *testing.T) {
	for _, id := range []string{"../vk", "a/b", "UPPER", "", "bad.service", strings.Repeat("x", 33)} {
		if id == "" {
			continue
		}
		m := NewInstance(id)
		m.Root = t.TempDir()
		m.Run = func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("invalid ID ran a command")
			return nil, nil
		}
		if _, err := m.Action(context.Background(), "vk", "restart", Request{}); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
	}
}
