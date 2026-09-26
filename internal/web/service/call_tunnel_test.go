package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCallTunnelOutboundKeepsRoutingAndClients(t *testing.T) {
	original := `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"balancerTag":"working"}]},"inbounds":[{"settings":{"clients":[{"id":"unchanged"}]}}]}`
	updated, err := addTunnelOutbound(original, "vk", 19094)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	json.Unmarshal([]byte(original), &a)
	json.Unmarshal([]byte(updated), &b)
	for _, k := range []string{"routing", "inbounds"} {
		if !reflect.DeepEqual(a[k], b[k]) {
			t.Fatalf("%s changed", k)
		}
	}
	same, err := addTunnelOutbound(updated, "vk", 19094)
	if err != nil || same != updated {
		t.Fatal("same outbound is not idempotent")
	}
	if _, err = addTunnelOutbound(updated, "vk", 19095); err == nil {
		t.Fatal("silently overwrote conflicting outbound")
	}
}

func TestTunnelPairsKeepLegacyAndSelectExactInstance(t *testing.T) {
	setupBulkDB(t)
	svc := &CallTunnelService{}
	old := `[{'provider':'vk','nodeId':1,'outboundTag':'legacy'}]`
	old = strings.ReplaceAll(old, "'", "\"")
	if err := svc.saveSetting(pairsKey, old); err != nil {
		t.Fatal(err)
	}
	pairs, err := svc.pairs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].InstanceID != "" {
		t.Fatal("legacy pair not preserved")
	}
	pairs = append(pairs, TunnelPair{InstanceID: "first", Provider: "vk", NodeID: 2, OutboundTag: "one"}, TunnelPair{InstanceID: "second", Provider: "vk", NodeID: 3, OutboundTag: "two"})
	if err = svc.savePairs(pairs); err != nil {
		t.Fatal(err)
	}
	got, err := svc.pairs()
	if err != nil || !reflect.DeepEqual(got, pairs) {
		t.Fatal("multiple pairs were lost")
	}
	// An unknown instance must not fall back to the first provider match.
	if err = svc.Action(context.Background(), "vk", "check", "", "missing"); err == nil || err.Error() != "attach the tunnel first" {
		t.Fatalf("unknown instance selected a pair: %v", err)
	}
	if err = svc.Create(context.Background(), TunnelCreate{Provider: "vk", InstanceID: "second", NodeID: 3, OutboundTag: "another", Adopt: true}); err == nil || err.Error() != "tunnel instance already attached" {
		t.Fatalf("duplicate instance accepted: %v", err)
	}
}
