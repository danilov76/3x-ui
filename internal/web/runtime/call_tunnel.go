package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/calltunnel"
)

func (r *Remote) CallTunnel(ctx context.Context, provider, action string, req calltunnel.Request) (calltunnel.Status, error) {
	var result calltunnel.Status
	if provider != "vk" && provider != "telemost" {
		return result, errors.New("unknown provider")
	}
	switch action {
	case "status", "restart", "room", "install", "probe":
	default:
		return result, errors.New("unknown action")
	}
	env, err := r.doTimeout(ctx, http.MethodPost, "panel/api/tunnels/local/"+provider+"/"+action, req, 45*time.Second)
	if err != nil {
		return result, errors.New("node tunnel request failed; check connectivity, patch version and admin token scope")
	}
	err = json.Unmarshal(env.Obj, &result)
	return result, err
}
