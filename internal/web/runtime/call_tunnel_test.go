package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/calltunnel"
)

func TestCallTunnelRemoteRoundTrip(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/panel/api/tunnels/local/vk/install" {
			t.Errorf("wrong RPC: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing node authorization")
		}
		var req calltunnel.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Secret != "test-shared-secret" || req.Role != "server" || req.InstanceID != "second" {
			t.Error("wrong install payload")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": calltunnel.Status{InstanceID: "second", Provider: "vk", Installed: true, Role: "server", State: "active", Fingerprint: "public-fingerprint"}})
	}))
	defer server.Close()
	remote := NewRemote(nodeForServer(t, server, "skip", ""), nil)
	st, err := remote.CallTunnel(context.Background(), "vk", "install", calltunnel.Request{InstanceID: "second", Role: "server", Secret: "test-shared-secret"})
	if err != nil || st.Fingerprint != "public-fingerprint" || st.InstanceID != "second" {
		t.Fatalf("bad node result: %+v, %v", st, err)
	}
	if _, err = remote.CallTunnel(context.Background(), "vk/../../server", "install", calltunnel.Request{}); err == nil {
		t.Error("accepted arbitrary path")
	}
	if calls != 1 {
		t.Fatal("invalid request reached node")
	}
}

func TestCallTunnelRemoteErrorDoesNotEchoBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"secret":"sensitive-value"}`))
	}))
	defer server.Close()
	remote := NewRemote(nodeForServer(t, server, "skip", ""), nil)
	_, err := remote.CallTunnel(context.Background(), "vk", "status", calltunnel.Request{})
	if err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatal("unsafe remote error")
	}
}
