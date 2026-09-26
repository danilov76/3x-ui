package calltunnel

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type Status struct {
	AutoPorts   bool   `json:"autoPorts"`
	InstanceID  string `json:"instanceId,omitempty"`
	Provider    string `json:"provider" example:"vk"`
	Installed   bool   `json:"installed" example:"true"`
	State       string `json:"state" example:"active"`
	Role        string `json:"role" example:"client"`
	Room        string `json:"room" example:"https://vk.ru/call/join/example"`
	Port        int    `json:"port" example:"19094"`
	Fingerprint string `json:"fingerprint,omitempty"`
	ExitIP      string `json:"exitIp,omitempty" example:"203.0.113.1"`
	Error       string `json:"error,omitempty"`
}

type Request struct {
	InstanceID  string `json:"instanceId,omitempty"`
	Role        string `json:"role"`
	Room        string `json:"room"`
	Secret      string `json:"secret,omitempty"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	ServerPort  int    `json:"serverPort"`
	Fingerprint string `json:"fingerprint"`
}

type spec struct{ unit, config, binary, secret string }

var specs = map[string]spec{
	"vk":       {"turnrelay-vk.service", "/etc/turnrelay-vk/config.json", "/opt/turnrelay-vk/turnrelay-proxy", "/etc/turnrelay-vk/secret"},
	"telemost": {"olcrtc-telemost.service", "/etc/olcrtc/telemost.yaml", "/opt/olcrtc/olcrtc", "/etc/olcrtc/telemost.key"},
}

var (
	mutations    sync.Mutex
	roomPath     = regexp.MustCompile(`^/call/join/[A-Za-z0-9_-]+$`)
	telemostPath = regexp.MustCompile(`^/j/[0-9]+$`)
	keyPattern   = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type Manager struct {
	InstanceID string
	Root       string
	Run        func(context.Context, string, ...string) ([]byte, error)
}

func New() *Manager { return &Manager{Run: command} }

// Empty ID selects the legacy installation; existing services are never renamed.
func NewInstance(id string) *Manager { m := New(); m.InstanceID = id; return m }

var instancePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func ValidInstance(id string) bool { return id == "" || instancePattern.MatchString(id) }
func (m *Manager) spec(provider string) (spec, bool) {
	s, ok := specs[provider]
	if !ok || !ValidInstance(m.InstanceID) {
		return spec{}, false
	}
	if m.InstanceID == "" {
		return s, true
	}
	name := "xui-tunnel-" + provider + "-" + m.InstanceID
	return spec{name + ".service", "/etc/" + name + "/config.json", "/opt/" + name + "/" + filepath.Base(s.binary), "/etc/" + name + "/secret"}, true
}

func (m *Manager) stateName() string {
	if m.InstanceID == "" {
		return "turnrelay-vk"
	}
	return "xui-tunnel-vk-" + m.InstanceID
}

func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed; check service permissions and system journal", filepath.Base(name))
	}
	return b, nil
}

func (m *Manager) path(p string) string {
	if m.Root == "" {
		return p
	}
	return filepath.Join(m.Root, p)
}

func ValidateRoom(provider, room string) error {
	u, err := url.Parse(room)
	if err != nil || len(room) > 2048 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid call URL")
	}
	if provider == "vk" && (u.Host == "vk.ru" || u.Host == "vk.com") && roomPath.MatchString(u.Path) {
		return nil
	}
	if provider == "telemost" && (u.Host == "telemost.360.yandex.ru" || u.Host == "telemost.yandex.ru") && telemostPath.MatchString(u.Path) {
		return nil
	}
	return errors.New("call URL does not match provider")
}

func (m *Manager) read(path string) ([]byte, error) {
	p := m.path(path)
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return nil, errors.New("expected regular configuration file under 1 MiB")
	}
	return os.ReadFile(p)
}

func (m *Manager) write(path string, data []byte, mode os.FileMode) error {
	p := m.path(path)
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return errors.New("refusing non-regular destination")
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".call-tunnel-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

func (m *Manager) config(provider string) (map[string]any, error) {
	s, ok := m.spec(provider)
	if !ok {
		return nil, errors.New("unknown tunnel provider")
	}
	b, err := m.read(s.config)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if json.Unmarshal(b, &c) != nil {
		return nil, errors.New("unsupported configuration: expected installer JSON format")
	}
	return c, nil
}

func argsOf(c map[string]any) ([]string, error) {
	raw, ok := c["args"].([]any)
	if !ok {
		return nil, errors.New("invalid VK configuration")
	}
	a := make([]string, len(raw))
	for i, v := range raw {
		var yes bool
		a[i], yes = v.(string)
		if !yes {
			return nil, errors.New("invalid VK arguments")
		}
	}
	if len(a) == 0 {
		return nil, errors.New("empty VK command")
	}
	return a, nil
}

func flagValue(a []string, k string) string {
	for i := 1; i < len(a)-1; i++ {
		if a[i] == k {
			return a[i+1]
		}
	}
	return ""
}
func nested(c map[string]any, k string) map[string]any { v, _ := c[k].(map[string]any); return v }
func text(c map[string]any, k string) string           { v, _ := c[k].(string); return v }
func number(c map[string]any, k string) int            { v, _ := c[k].(float64); return int(v) }
func (m *Manager) Status(ctx context.Context, provider string) Status {
	out := Status{AutoPorts: true, InstanceID: m.InstanceID, Provider: provider, State: "not-installed"}
	s, ok := m.spec(provider)
	if !ok {
		out.Error = "unknown provider"
		return out
	}
	c, err := m.config(provider)
	if errors.Is(err, os.ErrNotExist) {
		return out
	}
	out.Installed = true
	if err != nil {
		out.State = "invalid"
		out.Error = "cannot read supported tunnel configuration"
		return out
	}
	if provider == "vk" {
		a, e := argsOf(c)
		if e != nil {
			out.Error = e.Error()
			return out
		}
		switch a[0] {
		case s.binary:
			out.Role = "client"
		case filepath.Join(filepath.Dir(s.binary), "turnrelay-server"):
			out.Role = "server"
		default:
			out.Error = "unsupported VK executable"
			return out
		}
		out.Room = flagValue(a, "-links")
		_, p, _ := net.SplitHostPort(flagValue(a, "-listen"))
		out.Port, _ = strconv.Atoi(p)
		if out.Role == "server" {
			out.Fingerprint, _ = m.fingerprint()
		} else {
			out.Fingerprint = flagValue(a, "-server-fingerprint")
		}
	} else {
		switch text(c, "mode") {
		case "cnc":
			out.Role = "client"
		case "srv":
			out.Role = "server"
		default:
			out.Error = "unsupported Telemost mode"
			return out
		}
		out.Room = text(nested(c, "room"), "id")
		out.Port = number(nested(c, "socks"), "port")
	}
	b, e := m.Run(ctx, "systemctl", "show", s.unit, "--property=ActiveState", "--value")
	if e != nil {
		out.State = "unknown"
		out.Error = e.Error()
	} else {
		out.State = strings.TrimSpace(string(b))
	}
	return out
}

func (m *Manager) fingerprint() (string, error) {
	b, err := os.ReadFile(m.path("/var/lib/" + m.stateName() + "/cert.pem"))
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("missing certificate")
	}
	if _, err = x509.ParseCertificate(block.Bytes); err != nil {
		return "", errors.New("invalid certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

func (m *Manager) Restart(ctx context.Context, p string) error {
	s, ok := m.spec(p)
	if !ok {
		return errors.New("unknown provider")
	}
	if st := m.Status(ctx, p); !st.Installed || st.Error != "" {
		return errors.New("tunnel is not configured")
	}
	_, err := m.Run(ctx, "systemctl", "restart", s.unit)
	return err
}

func (m *Manager) Room(ctx context.Context, p, room string) error {
	if err := ValidateRoom(p, room); err != nil {
		return err
	}
	c, err := m.config(p)
	if err != nil {
		return errors.New("cannot read tunnel configuration")
	}
	s, _ := m.spec(p)
	old, err := m.read(s.config)
	if err != nil {
		return err
	}
	if p == "vk" {
		a, e := argsOf(c)
		if e != nil {
			return e
		}
		if a[0] != s.binary {
			return errors.New("VK link belongs on the client only")
		}
		found := 0
		for i := 1; i < len(a)-1; i++ {
			if a[i] == "-links" {
				a[i+1] = room
				found++
			}
		}
		if found != 1 {
			return errors.New("ambiguous VK call link")
		}
		c["args"] = a
	} else {
		if nested(c, "room") == nil {
			return errors.New("missing room settings")
		}
		nested(c, "room")["id"] = room
	}
	if err = m.write(s.config+".backup-"+strconv.FormatInt(time.Now().UnixNano(), 10), old, 0o600); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err = m.write(s.config, b, 0o600); err != nil {
		return err
	}
	if _, err = m.Run(ctx, "systemctl", "restart", s.unit); err != nil {
		restore := m.write(s.config, old, 0o600)
		_, restart := m.Run(context.Background(), "systemctl", "restart", s.unit)
		if restore != nil || restart != nil {
			return errors.New("restart failed and rollback incomplete; inspect service")
		}
		return errors.New("restart failed; previous configuration restored")
	}
	return nil
}

func (m *Manager) Probe(ctx context.Context, p string) (Status, error) {
	st := m.Status(ctx, p)
	if !st.Installed || st.Role != "client" || st.Error != "" || st.Port < 1 || st.Port > 65535 {
		return st, errors.New("probe requires a configured client")
	}
	c, err := m.config(p)
	if err != nil {
		return st, err
	}
	host := ""
	if p == "vk" {
		a, e := argsOf(c)
		if e != nil {
			return st, e
		}
		host, _, _ = net.SplitHostPort(flagValue(a, "-listen"))
	} else {
		host = text(nested(c, "socks"), "host")
	}
	if host != "127.0.0.1" {
		return st, errors.New("probe requires a loopback SOCKS listener")
	}
	d, err := proxy.SOCKS5("tcp", net.JoinHostPort(host, strconv.Itoa(st.Port)), nil, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		return st, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return st, errors.New("SOCKS context dialer unavailable")
	}
	transport := &http.Transport{DialContext: cd.DialContext, TLSHandshakeTimeout: 8 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return st, err
	}
	response, err := client.Do(req)
	if err != nil {
		return st, errors.New("HTTPS probe through SOCKS failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil || response.StatusCode != http.StatusOK || net.ParseIP(strings.TrimSpace(string(body))) == nil {
		return st, errors.New("unexpected exit IP response")
	}
	st.ExitIP = strings.TrimSpace(string(body))
	return st, nil
}

func (m *Manager) Action(ctx context.Context, p, action string, r Request) (Status, error) {
	if _, ok := m.spec(p); !ok {
		return Status{}, errors.New("unknown provider")
	}
	if action == "status" {
		st := m.Status(ctx, p)
		return st, nil
	}
	if action == "probe" {
		return m.Probe(ctx, p)
	}
	mutations.Lock()
	defer mutations.Unlock()
	var err error
	switch action {
	case "restart":
		err = m.Restart(ctx, p)
	case "room":
		err = m.Room(ctx, p, r.Room)
	case "install":
		err = m.Install(ctx, p, r)
	default:
		err = errors.New("unknown tunnel action")
	}
	return m.Status(ctx, p), err
}

func validateInstall(p string, r Request) error {
	if _, ok := specs[p]; !ok {
		return errors.New("unknown provider")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("installer requires Linux amd64 with systemd")
	}
	if r.Role != "client" && r.Role != "server" {
		return errors.New("invalid role")
	}
	if !keyPattern.MatchString(r.Secret) {
		return errors.New("invalid shared secret")
	}
	if r.Port < 1 || r.Port > 65535 || r.ServerPort < 1 || r.ServerPort > 65535 {
		return errors.New("invalid port")
	}
	if p == "telemost" || r.Role == "client" {
		if err := ValidateRoom(p, r.Room); err != nil {
			return err
		}
	}
	if p == "vk" && r.Role == "client" && (net.ParseIP(r.Address) == nil || !keyPattern.MatchString(r.Fingerprint)) {
		return errors.New("VK requires server IP and certificate SHA256")
	}
	return nil
}
