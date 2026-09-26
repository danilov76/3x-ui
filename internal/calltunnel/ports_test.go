package calltunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticPortSkipsSocketsAndStoppedConfigurations(t *testing.T) {
	for _, network := range []string{"tcp", "udp"} {
		t.Run(network, func(t *testing.T) {
			m := NewInstance("auto")
			m.Root = t.TempDir()
			ctx := context.Background()
			first, err := m.AvailablePort(ctx, network)
			if err != nil {
				t.Fatal(err)
			}
			if network == "tcp" {
				listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", first))
				if e != nil {
					t.Fatal(e)
				}
				defer listener.Close()
			} else {
				listener, e := net.ListenPacket("udp", fmt.Sprintf(":%d", first))
				if e != nil {
					t.Fatal(e)
				}
				defer listener.Close()
			}
			second, err := m.AvailablePort(ctx, network)
			if err != nil {
				t.Fatal(err)
			}
			if first == second {
				t.Fatal("selected a bound socket")
			}
			// A stopped sibling must reserve its port even though it has no listener.
			sibling := m.path("/etc/xui-tunnel-vk-stopped/config.json")
			if err = os.MkdirAll(filepath.Dir(sibling), 0o700); err != nil {
				t.Fatal(err)
			}
			raw := fmt.Sprintf(`{"args":["/opt/example","-listen","127.0.0.1:%d"]}`, second)
			if err = os.WriteFile(sibling, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			third, err := m.AvailablePort(ctx, network)
			if err != nil {
				t.Fatal(err)
			}
			if third == first || third == second {
				t.Fatal("selected a reserved port")
			}
			got, _ := os.ReadFile(sibling)
			if string(got) != raw {
				t.Fatal("allocation changed sibling configuration")
			}
		})
	}
}

func TestAutomaticPortFailsWhenXrayReservesRange(t *testing.T) {
	m := New()
	m.Root = t.TempDir()
	p := m.path("/usr/local/x-ui/bin/config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"inbounds":[{"port":"19090-19999"},{"port":"56000-56999"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, network := range []string{"tcp", "udp"} {
		if _, err := m.AvailablePort(context.Background(), network); err == nil || !strings.Contains(err.Error(), "no free tunnel port") {
			t.Fatalf("expected exhausted range, got %v", err)
		}
	}
}

func TestAutomaticPortServerInstallReturnsSelectedPort(t *testing.T) {
	m := NewInstance("auto-install")
	m.Root = t.TempDir()
	payload := []byte("test server")
	hash := sha256.Sum256(payload)
	old := binaryHashes["turnrelay-server"]
	binaryHashes["turnrelay-server"] = hex.EncodeToString(hash[:])
	t.Cleanup(func() { binaryHashes["turnrelay-server"] = old })
	dest := m.path("/usr/local/share/x-ui-tunnels/turnrelay-server")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	m.Run = func(_ context.Context, _ string, a ...string) ([]byte, error) {
		if a[0] == "--version" {
			return []byte("systemd 255"), nil
		}
		if a[0] == "enable" {
			path := m.path("/var/lib/" + m.stateName() + "/cert.pem")
			if e := os.MkdirAll(filepath.Dir(path), 0o755); e != nil {
				return nil, e
			}
			if e := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); e != nil {
				return nil, e
			}
		}
		return []byte("active"), nil
	}
	st, err := m.Action(context.Background(), "vk", "install", Request{Role: "server", Room: "https://vk.ru/call/join/test", Secret: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if st.Port < 56000 || st.Port > 56999 || st.Fingerprint == "" || !st.AutoPorts {
		t.Fatalf("incomplete installed endpoint: %+v", st)
	}
	other := NewInstance("next-install")
	other.Root = m.Root
	next, err := other.AvailablePort(context.Background(), "udp")
	if err != nil || next == st.Port {
		t.Fatalf("installed port not reserved: next=%d err=%v", next, err)
	}
}
