package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/calltunnel"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

type TunnelPair struct {
	InstanceID  string `json:"instanceId,omitempty"`
	Provider    string `json:"provider" example:"vk"`
	NodeID      int    `json:"nodeId" example:"2"`
	OutboundTag string `json:"outboundTag" example:"koara-vk"`
	LastCheck   string `json:"lastCheck,omitempty"`
	ExitIP      string `json:"exitIp,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}
type TunnelView struct {
	InstanceID string             `json:"instanceId,omitempty"`
	Provider   string             `json:"provider" example:"vk"`
	Pair       *TunnelPair        `json:"pair,omitempty"`
	Local      calltunnel.Status  `json:"local"`
	Peer       *calltunnel.Status `json:"peer,omitempty"`
	Error      string             `json:"error,omitempty"`
}
type TunnelCreate struct {
	InstanceID  string `json:"instanceId,omitempty"`
	Provider    string `json:"provider" binding:"required"`
	NodeID      int    `json:"nodeId" binding:"required"`
	OutboundTag string `json:"outboundTag" binding:"required"`
	Adopt       bool   `json:"adopt"`
	Room        string `json:"room"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	ServerPort  int    `json:"serverPort"`
}
type CallTunnelService struct {
	SettingService
	NodeService
}

var (
	pairMutations sync.Mutex
	outboundName  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

const pairsKey = "managedCallTunnelPairs"

func (s *CallTunnelService) pairs() ([]TunnelPair, error) {
	st, err := s.getSetting(pairsKey)
	if database.IsNotFound(err) {
		return []TunnelPair{}, nil
	}
	if err != nil {
		return nil, err
	}
	var pairs []TunnelPair
	err = json.Unmarshal([]byte(st.Value), &pairs)
	return pairs, err
}

func (s *CallTunnelService) savePairs(p []TunnelPair) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.saveSetting(pairsKey, string(b))
}

func (s *CallTunnelService) peer(ctx context.Context, id int, p, action string, r calltunnel.Request) (calltunnel.Status, error) {
	n, err := s.GetById(id)
	if err != nil || n == nil || !n.Enable {
		return calltunnel.Status{}, errors.New("node not found or disabled")
	}
	if n.Scheme != "https" {
		return calltunnel.Status{}, errors.New("tunnel management requires HTTPS for the node API")
	}
	mgr := runtime.GetManager()
	if mgr == nil {
		return calltunnel.Status{}, errors.New("node runtime unavailable")
	}
	remote, err := mgr.RemoteFor(n)
	if err != nil {
		return calltunnel.Status{}, err
	}
	if r.InstanceID != "" && action != "status" {
		capability, e := remote.CallTunnel(ctx, p, "status", calltunnel.Request{InstanceID: r.InstanceID})
		if e != nil {
			return capability, e
		}
		if capability.InstanceID != r.InstanceID {
			return capability, errors.New("node does not support this tunnel instance; update its panel first")
		}
	}
	st, err := remote.CallTunnel(ctx, p, action, r)
	if err == nil && st.InstanceID != r.InstanceID {
		return st, errors.New("node does not support this tunnel instance; update its panel first")
	}
	return st, err
}

func (s *CallTunnelService) List(ctx context.Context) ([]TunnelView, error) {
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	out := make([]TunnelView, len(pairs))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, pair := range pairs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := pair
			view := TunnelView{InstanceID: pair.InstanceID, Provider: pair.Provider, Pair: &v}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				view.Error = "status request timed out"
				out[i] = view
				return
			}
			view.Local = calltunnel.NewInstance(pair.InstanceID).Status(ctx, pair.Provider)
			peer, e := s.peer(ctx, pair.NodeID, pair.Provider, "status", calltunnel.Request{InstanceID: pair.InstanceID})
			if e != nil {
				view.Error = e.Error()
			} else {
				view.Peer = &peer
			}
			out[i] = view
		}()
	}
	wg.Wait()
	for _, provider := range []string{"vk", "telemost"} {
		attached := false
		for _, pair := range pairs {
			if pair.Provider == provider && pair.InstanceID == "" {
				attached = true
			}
		}
		if !attached {
			out = append(out, TunnelView{Provider: provider, Local: calltunnel.New().Status(ctx, provider)})
		}
	}
	return out, nil
}

func (s *CallTunnelService) Create(ctx context.Context, r TunnelCreate) error {
	pairMutations.Lock()
	defer pairMutations.Unlock()
	if (r.Provider != "vk" && r.Provider != "telemost") || !outboundName.MatchString(r.OutboundTag) || r.NodeID <= 0 || !calltunnel.ValidInstance(r.InstanceID) {
		return errors.New("invalid tunnel parameters")
	}
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	if !r.Adopt && r.InstanceID == "" {
		id := make([]byte, 8)
		if _, err = rand.Read(id); err != nil {
			return err
		}
		r.InstanceID = hex.EncodeToString(id)
	}
	for _, p := range pairs {
		if p.Provider == r.Provider && p.InstanceID == r.InstanceID {
			return errors.New("tunnel instance already attached")
		}
		if p.OutboundTag == r.OutboundTag {
			return errors.New("outbound is already assigned to another tunnel")
		}
	}
	mgr := calltunnel.NewInstance(r.InstanceID)
	local := mgr.Status(ctx, r.Provider)
	peer, err := s.peer(ctx, r.NodeID, r.Provider, "status", calltunnel.Request{InstanceID: r.InstanceID})
	if err != nil {
		return err
	}
	if r.Adopt {
		if !local.Installed || local.Role != "client" || local.Error != "" || !peer.Installed || peer.Role != "server" || peer.Error != "" {
			return errors.New("attach requires an existing client here and server on the selected node")
		}
		if r.Provider == "telemost" && local.Room != peer.Room {
			return errors.New("Telemost call links differ; align them before attaching")
		}
		if r.Provider == "vk" && local.Fingerprint != peer.Fingerprint {
			return errors.New("VK server fingerprint differs from client configuration")
		}
	} else {
		if local.Installed || peer.Installed {
			return errors.New("existing tunnel found; use Attach existing")
		}
		if err = calltunnel.ValidateRoom(r.Provider, r.Room); err != nil {
			return err
		}
		// Validate the local install parameters before changing either endpoint.
		if r.Provider == "vk" && net.ParseIP(r.Address) == nil {
			return errors.New("VK requires the foreign server IP address")
		}
		if r.Port < 1 || r.Port > 65535 || r.ServerPort < 1 || r.ServerPort > 65535 {
			return errors.New("ports must be 1..65535")
		}
		if _, err = s.outbound(r.OutboundTag, r.Port, false); err != nil {
			return err
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		request := calltunnel.Request{InstanceID: r.InstanceID, Role: "server", Room: r.Room, Secret: hex.EncodeToString(key), Port: r.Port, ServerPort: r.ServerPort, Address: r.Address}
		preflight := request
		preflight.Role = "client"
		preflight.Fingerprint = strings.Repeat("0", 64)
		if err = mgr.Preflight(ctx, r.Provider, preflight); err != nil {
			return err
		}
		peer, err = s.peer(ctx, r.NodeID, r.Provider, "install", request)
		if err != nil {
			return fmt.Errorf("server installation for instance %s needs inspection before retry: %w", r.InstanceID, err)
		}
		request.Role = "client"
		request.Fingerprint = peer.Fingerprint
		if _, err = mgr.Action(ctx, r.Provider, "install", request); err != nil {
			return fmt.Errorf("server installed for instance %s; local installation needs attention: %w", r.InstanceID, err)
		}
	}
	pair := TunnelPair{InstanceID: r.InstanceID, Provider: r.Provider, NodeID: r.NodeID, OutboundTag: r.OutboundTag}
	pairs = append(pairs, pair)
	if err = s.savePairs(pairs); err != nil {
		return err
	}
	if err = s.checkPublish(ctx, &pairs, len(pairs)-1, 2); err != nil {
		return fmt.Errorf("tunnel saved; close setup and use Check to retry: %w", err)
	}
	return nil
}

func (s *CallTunnelService) checkPublish(ctx context.Context, pairs *[]TunnelPair, index, retries int) error {
	pair := &(*pairs)[index]
	st, err := calltunnel.NewInstance(pair.InstanceID).Probe(ctx, pair.Provider)
	for i := 0; err != nil && i < retries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		st, err = calltunnel.NewInstance(pair.InstanceID).Probe(ctx, pair.Provider)
	}
	pair.LastCheck = time.Now().UTC().Format(time.RFC3339)
	pair.ExitIP = st.ExitIP
	pair.LastError = ""
	if err != nil {
		pair.LastError = err.Error()
	} else {
		_, err = s.outbound(pair.OutboundTag, st.Port, true)
		if err != nil {
			pair.LastError = err.Error()
		}
	}
	if saveErr := s.savePairs(*pairs); saveErr != nil {
		return saveErr
	}
	return err
}

func (s *CallTunnelService) Action(ctx context.Context, provider, action, room string, instanceIDs ...string) error {
	instanceID := ""
	if len(instanceIDs) > 0 {
		instanceID = instanceIDs[0]
	}
	if !calltunnel.ValidInstance(instanceID) {
		return errors.New("invalid tunnel instance")
	}
	pairMutations.Lock()
	defer pairMutations.Unlock()
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	index := -1
	for i := range pairs {
		if pairs[i].Provider == provider && pairs[i].InstanceID == instanceID {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("attach the tunnel first")
	}
	pair := pairs[index]
	mgr := calltunnel.NewInstance(pair.InstanceID)
	switch action {
	case "check":
		return s.checkPublish(ctx, &pairs, index, 0)
	case "restart":
		if _, err = s.peer(ctx, pair.NodeID, provider, "restart", calltunnel.Request{InstanceID: pair.InstanceID}); err != nil {
			return err
		}
		_, err = mgr.Action(ctx, provider, "restart", calltunnel.Request{InstanceID: pair.InstanceID})
		return err
	case "room":
		if err = calltunnel.ValidateRoom(provider, room); err != nil {
			return err
		}
		oldLocal := mgr.Status(ctx, provider)
		if oldLocal.Error != "" || oldLocal.Role != "client" {
			return errors.New("local client configuration is invalid")
		}
		oldPeer, e := s.peer(ctx, pair.NodeID, provider, "status", calltunnel.Request{InstanceID: pair.InstanceID})
		if e != nil {
			return e
		}
		if provider == "telemost" {
			if _, err = s.peer(ctx, pair.NodeID, provider, "room", calltunnel.Request{InstanceID: pair.InstanceID, Room: room}); err != nil {
				return err
			}
		}
		if _, err = mgr.Action(ctx, provider, "room", calltunnel.Request{InstanceID: pair.InstanceID, Room: room}); err != nil {
			if provider == "telemost" {
				if _, rollback := s.peer(context.WithoutCancel(ctx), pair.NodeID, provider, "room", calltunnel.Request{InstanceID: pair.InstanceID, Room: oldPeer.Room}); rollback != nil {
					return errors.New("room update failed and peer rollback failed; align both call links manually")
				}
			}
			return err
		}
		pairs[index].LastCheck = ""
		pairs[index].ExitIP = ""
		pairs[index].LastError = ""
		return s.savePairs(pairs)
	default:
		return errors.New("unknown action")
	}
}

func addTunnelOutbound(raw, tag string, port int) (string, error) {
	var t map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &t) != nil {
		return "", errors.New("invalid Xray template")
	}
	var outs []map[string]any
	if json.Unmarshal(t["outbounds"], &outs) != nil {
		return "", errors.New("invalid outbound list")
	}
	out := map[string]any{"tag": tag, "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": float64(port)}}}}
	for _, v := range outs {
		if v["tag"] == tag {
			if reflect.DeepEqual(v, out) {
				return raw, nil
			}
			return "", errors.New("outbound name is already used by a different configuration")
		}
	}
	outs = append(outs, out)
	b, err := json.Marshal(outs)
	if err != nil {
		return "", err
	}
	t["outbounds"] = b
	b, err = json.Marshal(t)
	return string(b), err
}

func (s *CallTunnelService) outbound(tag string, port int, apply bool) (string, error) {
	old, err := s.GetXrayConfigTemplate()
	if err != nil {
		return "", err
	}
	next, err := addTunnelOutbound(old, tag, port)
	if err != nil || !apply || next == old {
		return next, err
	}
	validator := XraySettingService{}
	if err = validator.CheckXrayConfig(next); err != nil {
		return "", err
	}
	// Keep concurrent panel edits, client rows and routing intact.
	db := database.GetDB()
	res := db.Model(&model.Setting{}).Where("key = ? AND value = ?", "xrayTemplateConfig", old).Update("value", next)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected != 1 {
		return "", errors.New("Xray settings changed concurrently; retry")
	}
	x := XrayService{}
	if x.IsXrayRunning() {
		if err = x.RestartXray(false); err != nil {
			restore := db.Model(&model.Setting{}).Where("key = ? AND value = ?", "xrayTemplateConfig", next).Update("value", old)
			if restore.Error == nil && restore.RowsAffected == 1 {
				_ = x.RestartXray(false)
			}
			return "", errors.New("outbound apply failed; inspect Xray status")
		}
	}
	return next, nil
}
