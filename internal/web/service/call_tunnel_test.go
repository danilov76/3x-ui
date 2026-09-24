package service

import (
	"encoding/json"
	"reflect"
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
