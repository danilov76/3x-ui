package controller

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/calltunnel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type CallTunnelController struct{ service service.CallTunnelService }

func NewCallTunnelController(g *gin.RouterGroup) {
	a := &CallTunnelController{}
	g.GET("/tunnels/list", a.list)
	g.POST("/tunnels/create", a.create)
	g.POST("/tunnels/:provider/:action", a.action)
	g.POST("/tunnels/local/:provider/:action", a.local)
}

func (a *CallTunnelController) list(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 95*time.Second)
	defer cancel()
	obj, err := a.service.List(ctx)
	jsonObj(c, obj, err)
}

func (a *CallTunnelController) create(c *gin.Context) {
	var r service.TunnelCreate
	if err := c.ShouldBindJSON(&r); err != nil {
		jsonMsg(c, "invalid tunnel request", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 120*time.Second)
	defer cancel()
	jsonMsg(c, "", a.service.Create(ctx, r))
}

func (a *CallTunnelController) action(c *gin.Context) {
	var r struct {
		Room string `json:"room"`
	}
	if err := c.ShouldBindJSON(&r); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 120*time.Second)
	defer cancel()
	jsonMsg(c, "", a.service.Action(ctx, c.Param("provider"), c.Param("action"), r.Room))
}

func (a *CallTunnelController) local(c *gin.Context) {
	var r calltunnel.Request
	if err := c.ShouldBindJSON(&r); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 40*time.Second)
	defer cancel()
	st, err := calltunnel.New().Action(ctx, c.Param("provider"), c.Param("action"), r)
	jsonObj(c, st, err)
}
