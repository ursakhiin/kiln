package mcp

import (
	"context"
	"net/http"
	"time"

	connectrpc "connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1mcp"
	"github.com/ursakhiin/kiln/internal/service"
)

var _ kilnv1mcp.KilnServiceToolHandler = (*service.Service)(nil)

// sessionTimeout closes MCP sessions that have been idle this long.
const sessionTimeout = 2 * time.Hour

// NewServer returns an MCP server exposing every generated KilnService tool,
// backed by svc.
func NewServer(svc *service.Service, version string) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "kiln", Version: version}, nil)
	for _, tool := range kilnv1mcp.KilnServiceTools {
		srv.AddTool(&sdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}, toolHandler(svc, tool))
	}
	return srv
}

// NewHandler serves srv over stateful streamable HTTP.
func NewHandler(srv *sdk.Server) http.Handler {
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return srv },
		&sdk.StreamableHTTPOptions{SessionTimeout: sessionTimeout},
	)
}

// toolHandler decodes the arguments into the tool's request and calls the
// service as the agent of the calling session. It never returns a Go error:
// that would become a protocol error the model can't see.
func toolHandler(svc *service.Service, tool kilnv1mcp.KilnServiceTool) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		in := tool.NewRequest()
		args, err := normalizeArgs(req.Params.Arguments, in.ProtoReflect().Descriptor())
		if err == nil {
			err = protojson.Unmarshal(args, in)
		}
		if err != nil {
			return errorResult(connectrpc.CodeInvalidArgument, err.Error()), nil
		}
		resp, err := tool.Call(service.WithCaller(ctx, callerOf(req)), svc, in)
		if err != nil {
			return serviceErrorResult(err), nil
		}
		return successResult(resp), nil
	}
}

// callerOf identifies the session as an agent: its ID is the MCP session ID,
// and its host is the client's name and version.
func callerOf(req *sdk.CallToolRequest) service.Caller {
	c := service.Caller{Actor: kilnv1.ActorType_ACTOR_TYPE_AGENT, AgentID: req.Session.ID()}
	if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
		c.Host = p.ClientInfo.Name
		c.HostVersion = p.ClientInfo.Version
	}
	return c
}
