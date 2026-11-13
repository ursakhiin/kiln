package mcp

import (
	"bytes"
	"encoding/json"
	"errors"

	connectrpc "connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/service"
)

// noTaskText is the text of an empty next_task result.
const noTaskText = "No task is available: nothing is ready, unblocked and unclaimed."

var marshalOptions = protojson.MarshalOptions{UseProtoNames: true, Multiline: true, Indent: "  "}

// marshal encodes m as indented protojson. protojson deliberately varies its
// whitespace between builds, so the output is re-indented to keep the text
// agents see stable.
func marshal(m proto.Message) ([]byte, error) {
	js, err := marshalOptions.Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, js, "", "  "); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// successResult returns resp as structured content, with its markdown context
// as the text when it has one.
func successResult(resp proto.Message) *sdk.CallToolResult {
	js, err := marshal(resp)
	if err != nil {
		return errorResult(connectrpc.CodeInternal, "encoding the response: "+err.Error())
	}
	text := string(js)
	if ctx := contextText(resp); ctx != "" {
		text = ctx
	} else if next, ok := resp.(*underdrawv1.NextTaskResponse); ok && next.GetTask() == nil {
		text = noTaskText
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: text}},
		StructuredContent: json.RawMessage(js),
	}
}

// contextText returns resp's top-level string field named context, if any.
func contextText(resp proto.Message) string {
	m := resp.ProtoReflect()
	fd := m.Descriptor().Fields().ByName("context")
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.IsList() {
		return ""
	}
	return m.Get(fd).String()
}

// serviceErrorResult turns a service error into a tool error. A revision
// conflict includes the current item, so the agent can retry without another
// read.
func serviceErrorResult(err error) *sdk.CallToolResult {
	msg := err.Error()
	var conflict *service.ConflictError
	if errors.As(err, &conflict) && conflict.Current != nil {
		if js, jerr := marshal(conflict.Current); jerr == nil {
			msg += "\n\nCurrent item:\n" + string(js)
		}
	}
	return errorResult(service.ErrorCode(err), msg)
}

// errorResult is a tool error with the text "<code>: <message>". Tool errors
// are visible to the model, unlike protocol errors.
func errorResult(code connectrpc.Code, msg string) *sdk.CallToolResult {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: code.String() + ": " + msg}},
		IsError: true,
	}
}
