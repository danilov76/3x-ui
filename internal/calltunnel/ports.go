package calltunnel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AvailablePort skips both bound sockets and configured (possibly stopped) services.
// Installation checks the socket again before starting the service. The caller's
// mutation lock serializes panel-managed installations on the same server.
func (m *Manager) AvailablePort(ctx context.Context, network string) (int, error) {
	first, last := 19090, 19999
	if network == "udp" {
		first, last = 56000, 56999
	} else if network != "tcp" {
		return 0, errors.New("unsupported port allocation network")
	}
	used := map[int]bool{}
	reserve := func(value any) {
		var raw string
		switch v := value.(type) {
		case float64:
			raw = strconv.Itoa(int(v))
		case string:
			raw = v
		}
		for _, item := range strings.Split(raw, ",") {
			bounds := strings.Split(strings.TrimSpace(item), "-")
			start, _ := strconv.Atoi(bounds[0])
			end := start
			if len(bounds) == 2 {
				end, _ = strconv.Atoi(bounds[1])
			}
			if start < first {
				start = first
			}
			if end > last {
				end = last
			}
			for p := start; p <= end; p++ {
				used[p] = true
			}
		}
	}
	configs, err := filepath.Glob(m.path("/etc/xui-tunnel-*/config.json"))
	if err != nil {
		return 0, err
	}
	for _, s := range specs {
		configs = append(configs, m.path(s.config))
	}
	configs = append(configs, m.path("/usr/local/x-ui/bin/config.json"))
	for _, path := range configs {
		b, e := os.ReadFile(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return 0, errors.New("cannot inspect configured ports")
		}
		var c map[string]any
		if json.Unmarshal(b, &c) != nil {
			return 0, errors.New("cannot inspect configured ports: invalid JSON")
		}
		if args, e := argsOf(c); e == nil {
			_, port, _ := net.SplitHostPort(flagValue(args, "-listen"))
			reserve(port)
		}
		if socks := nested(c, "socks"); socks != nil {
			reserve(socks["port"])
		}
		if inbounds, ok := c["inbounds"].([]any); ok {
			for _, item := range inbounds {
				if inbound, ok := item.(map[string]any); ok {
					reserve(inbound["port"])
				}
			}
		}
	}
	for port := first; port <= last; port++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if used[port] {
			continue
		}
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if network == "udp" {
			listener, e := new(net.ListenConfig).ListenPacket(ctx, "udp", ":"+strconv.Itoa(port))
			if e == nil {
				listener.Close()
				return port, nil
			}
		} else {
			listener, e := new(net.ListenConfig).Listen(ctx, "tcp", address)
			if e == nil {
				listener.Close()
				return port, nil
			}
		}
	}
	return 0, errors.New("no free tunnel port in automatic allocation range")
}
