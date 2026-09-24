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
	Provider    string `json:"provider" example:"vk"`
	NodeID      int    `json:"nodeId" example:"2"`
	OutboundTag string `json:"outboundTag" example:"koara-vk"`
	LastCheck   string `json:"lastCheck,omitempty"`
	ExitIP      string `json:"exitIp,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}
type TunnelView struct {
	Provider string             `json:"provider" example:"vk"`
	Pair     *TunnelPair        `json:"pair,omitempty"`
	Local    calltunnel.Status  `json:"local"`
	Peer     *calltunnel.Status `json:"peer,omitempty"`
	Error    string             `json:"error,omitempty"`
}
type TunnelCreate struct {
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
	return remote.CallTunnel(ctx, p, action, r)
}

func (s *CallTunnelService) List(ctx context.Context) ([]TunnelView, error) {
	pairs, err := s.pairs()
	if err != nil {
		return nil, err
	}
	out := make([]TunnelView, 0, 2)
	for _, p := range []string{"vk", "telemost"} {
		view := TunnelView{Provider: p, Local: calltunnel.New().Status(ctx, p)}
		for _, pair := range pairs {
			if pair.Provider == p {
				v := pair
				view.Pair = &v
				peer, e := s.peer(ctx, pair.NodeID, p, "status", calltunnel.Request{})
				if e != nil {
					view.Error = e.Error()
				} else {
					view.Peer = &peer
				}
				break
			}
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *CallTunnelService) Create(ctx context.Context, r TunnelCreate) error {
	pairMutations.Lock()
	defer pairMutations.Unlock()
	if (r.Provider != "vk" && r.Provider != "telemost") || !outboundName.MatchString(r.OutboundTag) || r.NodeID <= 0 {
		return errors.New("invalid tunnel parameters")
	}
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if p.Provider == r.Provider {
			return errors.New("provider already attached; use its existing card")
		}
	}
	mgr := calltunnel.New()
	local := mgr.Status(ctx, r.Provider)
	peer, err := s.peer(ctx, r.NodeID, r.Provider, "status", calltunnel.Request{})
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
		request := calltunnel.Request{Role: "server", Room: r.Room, Secret: hex.EncodeToString(key), Port: r.Port, ServerPort: r.ServerPort, Address: r.Address}
		preflight := request
		preflight.Role = "client"
		preflight.Fingerprint = strings.Repeat("0", 64)
		if err = mgr.Preflight(ctx, r.Provider, preflight); err != nil {
			return err
		}
		peer, err = s.peer(ctx, r.NodeID, r.Provider, "install", request)
		if err != nil {
			return err
		}
		request.Role = "client"
		request.Fingerprint = peer.Fingerprint
		if _, err = mgr.Action(ctx, r.Provider, "install", request); err != nil {
			return fmt.Errorf("server installed; local installation needs attention: %w", err)
		}
	}
	pair := TunnelPair{Provider: r.Provider, NodeID: r.NodeID, OutboundTag: r.OutboundTag}
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
	st, err := calltunnel.New().Probe(ctx, pair.Provider)
	for i := 0; err != nil && i < retries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		st, err = calltunnel.New().Probe(ctx, pair.Provider)
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

func (s *CallTunnelService) Action(ctx context.Context, provider, action, room string) error {
	pairMutations.Lock()
	defer pairMutations.Unlock()
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	index := -1
	for i := range pairs {
		if pairs[i].Provider == provider {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("attach the tunnel first")
	}
	pair := pairs[index]
	mgr := calltunnel.New()
	switch action {
	case "check":
		return s.checkPublish(ctx, &pairs, index, 0)
	case "restart":
		if _, err = s.peer(ctx, pair.NodeID, provider, "restart", calltunnel.Request{}); err != nil {
			return err
		}
		_, err = mgr.Action(ctx, provider, "restart", calltunnel.Request{})
		return err
	case "room":
		if err = calltunnel.ValidateRoom(provider, room); err != nil {
			return err
		}
		oldLocal := mgr.Status(ctx, provider)
		if oldLocal.Error != "" || oldLocal.Role != "client" {
			return errors.New("local client configuration is invalid")
		}
		oldPeer, e := s.peer(ctx, pair.NodeID, provider, "status", calltunnel.Request{})
		if e != nil {
			return e
		}
		if provider == "telemost" {
			if _, err = s.peer(ctx, pair.NodeID, provider, "room", calltunnel.Request{Room: room}); err != nil {
				return err
			}
		}
		if _, err = mgr.Action(ctx, provider, "room", calltunnel.Request{Room: room}); err != nil {
			if provider == "telemost" {
				if _, rollback := s.peer(context.WithoutCancel(ctx), pair.NodeID, provider, "room", calltunnel.Request{Room: oldPeer.Room}); rollback != nil {
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
