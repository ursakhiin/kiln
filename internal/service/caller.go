package service

import (
	"context"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
)

// Caller is who is calling the service. The transport sets it with
// WithCaller: Connect callers are the user, and each MCP session is an agent.
// Request fields never set it.
type Caller struct {
	Actor       underdrawv1.ActorType
	AgentID     string // MCP session ID; empty for the user
	Host        string // MCP clientInfo.name
	HostVersion string // MCP clientInfo.version
}

type callerKey struct{}

// WithCaller returns a context carrying c.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller set by WithCaller, if any.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// actor returns the history Actor for c.
func (c Caller) actor() *underdrawv1.Actor {
	a := &underdrawv1.Actor{Type: c.Actor}
	if c.Actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		a.AgentId = c.AgentID
		a.Session = &underdrawv1.AgentSession{Host: c.Host, HostVersion: c.HostVersion}
	}
	return a
}
