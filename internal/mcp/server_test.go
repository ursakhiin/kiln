package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/service"
	"github.com/ursakhiin/underdraw/internal/store"
)

type env struct {
	url string
	svc *service.Service
	st  *store.Store
}

// start opens a service on a fresh repo and serves it over MCP.
func start(t *testing.T) env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc, fileErrs, err := service.New(st, service.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fileErrs) > 0 {
		t.Fatalf("unexpected file errors: %v", fileErrs)
	}
	ts := httptest.NewServer(NewHandler(NewServer(svc, "test")))
	t.Cleanup(ts.Close)
	return env{url: ts.URL, svc: svc, st: st}
}

// connect opens an MCP session whose client is called name.
func connect(t *testing.T, url, name string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: name, Version: "9.9"}, nil)
	cs, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call calls a tool and returns its result, failing on protocol errors.
func call(t *testing.T, cs *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// text returns the result's single text content.
func text(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *TextContent", res.Content[0])
	}
	return tc.Text
}

// ok calls a tool, requires success and decodes its structured content.
func ok(t *testing.T, cs *sdk.ClientSession, name string, args any) map[string]any {
	t.Helper()
	res := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, text(t, res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// field walks m along keys.
func field(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, _ := v.(map[string]any)
		v = mm[k]
	}
	return v
}

func createProject(t *testing.T, cs *sdk.ClientSession) string {
	t.Helper()
	res := ok(t, cs, "create_item", map[string]any{"item": map[string]any{"title": "P", "project": map[string]any{}}})
	return field(res, "item", "item", "id").(string)
}

func TestToolsList(t *testing.T) {
	e := start(t)
	cs := connect(t, e.url, "claude-code")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 15 {
		t.Fatalf("got %d tools, want 15", len(res.Tools))
	}
	for _, tool := range res.Tools {
		if tool.Name != "create_item" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if field(schema, "properties", "item") == nil {
			t.Fatalf("create_item schema has no properties.item: %s", raw)
		}
		return
	}
	t.Fatal("no create_item tool")
}

func TestCreateItemAsAgent(t *testing.T) {
	e := start(t)
	cs := connect(t, e.url, "claude-code")
	project := createProject(t, cs)
	res := ok(t, cs, "create_item", map[string]any{"item": map[string]any{
		"title": "F", "feature": map[string]any{"project_id": project},
	}})
	if field(res, "item", "item", "feature", "draft") != true {
		t.Fatalf("feature is not a draft: %v", res)
	}
	id := field(res, "item", "item", "id").(string)

	hist, fileErrs, err := e.st.ListHistory(id)
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("ListHistory: %v %v", err, fileErrs)
	}
	if len(hist) != 1 {
		t.Fatalf("got %d history entries, want 1", len(hist))
	}
	actor := hist[0].GetActor()
	if actor.GetType() != underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		t.Errorf("actor type %v, want agent", actor.GetType())
	}
	if actor.GetAgentId() != cs.ID() || cs.ID() == "" {
		t.Errorf("agent_id %q, want session ID %q", actor.GetAgentId(), cs.ID())
	}
	if actor.GetSession().GetHost() != "claude-code" || actor.GetSession().GetHostVersion() != "9.9" {
		t.Errorf("session %v, want claude-code 9.9", actor.GetSession())
	}
}

func TestUpdateItemSnakeCaseMask(t *testing.T) {
	e := start(t)
	cs := connect(t, e.url, "claude-code")
	project := createProject(t, cs)
	res := ok(t, cs, "update_item", map[string]any{
		"item":          map[string]any{"id": project, "title": "Renamed"},
		"base_revision": 1,
		"update_mask":   []string{"title"},
	})
	if got := field(res, "item", "item", "title"); got != "Renamed" {
		t.Fatalf("title %v, want Renamed", got)
	}
}

func TestStaleWrite(t *testing.T) {
	e := start(t)
	cs := connect(t, e.url, "claude-code")
	project := createProject(t, cs)
	res := call(t, cs, "update_item", map[string]any{
		"item":          map[string]any{"id": project, "title": "Renamed"},
		"base_revision": 0,
		"update_mask":   []string{"title"},
	})
	got := text(t, res)
	if !res.IsError || !strings.HasPrefix(got, "aborted: ") || !strings.Contains(got, `"revision": "1"`) {
		t.Fatalf("IsError=%v, text:\n%s", res.IsError, got)
	}
}

func TestUnknownArgument(t *testing.T) {
	e := start(t)
	cs := connect(t, e.url, "claude-code")
	res := call(t, cs, "get_item", map[string]any{"bogus": 1})
	if got := text(t, res); !res.IsError || !strings.HasPrefix(got, "invalid_argument: ") {
		t.Fatalf("IsError=%v, text: %s", res.IsError, got)
	}
}

func TestNextTaskTwoAgents(t *testing.T) {
	e := start(t)
	user := service.WithCaller(context.Background(), service.Caller{Actor: underdrawv1.ActorType_ACTOR_TYPE_USER})
	create := func(item *underdrawv1.Item) string {
		t.Helper()
		resp, err := e.svc.CreateItem(user, &underdrawv1.CreateItemRequest{Item: item})
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetItem().GetItem().GetId()
	}
	project := create(&underdrawv1.Item{Title: "P", Kind: &underdrawv1.Item_Project{Project: &underdrawv1.Project{}}})
	feature := create(&underdrawv1.Item{Title: "F", Kind: &underdrawv1.Item_Feature{Feature: &underdrawv1.Feature{ProjectId: project}}})
	tasks := map[string]bool{}
	for _, title := range []string{"T1", "T2"} {
		tasks[create(&underdrawv1.Item{Title: title, Kind: &underdrawv1.Item_Task{Task: &underdrawv1.Task{FeatureId: feature}}})] = true
	}

	claimed := map[string]bool{}
	for _, name := range []string{"agent-a", "agent-b"} {
		cs := connect(t, e.url, name)
		res := call(t, cs, "next_task", nil)
		got := text(t, res)
		if res.IsError || !strings.HasPrefix(got, "# Task: ") {
			t.Fatalf("%s: IsError=%v, text:\n%s", name, res.IsError, got)
		}
		// The task's own ID is on its "- ID:" line; the feature section
		// lists every task.
		var found string
		for id := range tasks {
			if strings.Contains(got, "\n- ID: `"+id+"`\n") {
				found = id
			}
		}
		if found == "" || claimed[found] {
			t.Fatalf("%s got no new task (claimed %v):\n%s", name, claimed, got)
		}
		claimed[found] = true
	}

	cs := connect(t, e.url, "agent-c")
	res := call(t, cs, "next_task", nil)
	if got := text(t, res); res.IsError || got != noTaskText {
		t.Fatalf("IsError=%v, text: %s", res.IsError, got)
	}
}

// A session without an ID can't be told apart from other such sessions, so
// its tools refuse to run rather than act as a shared agent "".
func TestEmptySessionIDRejected(t *testing.T) {
	e := start(t)
	ctx := context.Background()
	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := NewServer(e.svc, "test").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "mem", Version: "1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	if ss.ID() != "" {
		t.Skipf("in-memory session has ID %q", ss.ID())
	}

	res := call(t, cs, "create_item", map[string]any{"item": map[string]any{"title": "P", "project": map[string]any{}}})
	if got := text(t, res); !res.IsError || !strings.HasPrefix(got, "failed_precondition: ") {
		t.Fatalf("IsError=%v, text: %s", res.IsError, got)
	}
	items, _, err := e.st.ListItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("%d items created, want 0", len(items))
	}
}
