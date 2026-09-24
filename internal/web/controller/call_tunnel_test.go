package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestCallTunnelAdminScope(t *testing.T) {
	for _, scope := range []string{model.ApiScopeAdmin, model.ApiScopeMonitor, model.ApiScopeNodeSync} {
		t.Run(scope, func(t *testing.T) {
			engine := gin.New()
			engine.Use(func(c *gin.Context) { c.Set("api_token_scope", scope) })
			a := &APIController{}
			g := engine.Group("/panel/api", a.enforceTokenScope)
			g.POST("/tunnels/local/:provider/:action", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/panel/api/tunnels/local/vk/install", nil))
			want := http.StatusForbidden
			if scope == model.ApiScopeAdmin {
				want = http.StatusNoContent
			}
			if w.Code != want {
				t.Fatalf("status %d, want %d", w.Code, want)
			}
		})
	}
}
