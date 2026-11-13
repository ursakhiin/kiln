# Service Layer and MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement step 2 of Kiln's build order, so that agents can connect to `kiln serve` over MCP and run the whole agent loop:
- create and read items
- `next_task` and claiming
- status updates
- work state
- history
- markdown views

The user's actions go through the Connect API, which is what lets dogfooding begin.

**Architecture:**
- `internal/service` is the single write path. It loads every item into memory at startup, applies the `internal/core` rules, and writes through `internal/store`. One mutex serializes every operation, and claims live only in memory.
- `protoc-gen-kiln-mcp` generates one MCP tool per annotated RPC, with a JSON Schema built from the request message and its proto comments. `internal/mcp` registers those tools on the official Go SDK's streamable HTTP server.
- `internal/api` serves the same service over Connect as the user.
- `internal/daemon` runs both on one loopback port, under a per-repo lock.

**Tech Stack:**
- Go 1.27
- `github.com/modelcontextprotocol/go-sdk` v1.8.0 (new)
- `connectrpc.com/connect`
- `google.golang.org/protobuf/compiler/protogen`
- `github.com/gofrs/flock` (already in `go.sum` as indirect; becomes direct)
- Standard `testing` and `net/http/httptest`

**Spec:**
- `Requirements.md` and `proto/kiln/v1/{item,service,history}.proto`. The proto comments are the authoritative rules.
- Step 1 is in `internal/core` and `internal/store`; see `docs/superpowers/plans/2026-11-12-core-and-store.md`.

**User decisions for this step (2026-11-13):**

| Topic | Decision |
|---|---|
| MCP tool schemas | Build `protoc-gen-kiln-mcp` now |
| User actions while dogfooding | Serve the Connect API now; call it with `buf curl` until the CLI exists |
| Claim TTL | 30 minutes |
| Daemon address | `--addr`, defaulting to `127.0.0.1:7477`; the actual address is written to `.kiln/run/daemon.json` |
| Item lookups | In memory; SQLite comes later |
| File watching | Deferred. Files load at startup, so restart the daemon after a `git pull` |

## Global Constraints

**Carried over from step 1:**
- Import the generated types as `kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"`.
- Never hand-edit `gen/go/` or `web/src/gen/`, and never add hand-written files there: `make check-gen` diffs the whole directory.
- `internal/core` stays pure (`TestCoreDoesNoIO`).
- Map errors with `errors.Is` on the `core` sentinels. Error messages never include a value rejected as a secret.

**Callers and permissions:**
- Callers are identified by transport only, through `service.WithCaller`:
  - Connect callers are the user (`ACTOR_TYPE_USER`).
  - Each MCP session is an agent (`ACTOR_TYPE_AGENT`), whose `agent_id` is the MCP session ID.
  - Request fields never set the actor.
- The user-only RPCs are DeleteItem and MoveItem; SetStatus is user-only for features and for `STATUS_DONE`. An agent calling them gets `PERMISSION_DENIED`.

**Service state:**
- The service keeps every item in memory and returns clones (`proto.Clone`), never its cached pointers.
- Every service method takes the service mutex for its whole body.
- Time always comes from `Options.Now`; nothing calls `time.Now` directly except the default.
- On every write that changes content:
  1. increment `revision`
  2. set `update_time`
  3. write the item file
  4. append one `ChangeEvent` history entry whose `snapshot` is the written item

  Writing the item before history means a crash between the two can leave a revision without its history entry, but never a history entry for content that wasn't saved.
- Work-state updates write the task file without changing `revision` or `update_time`, and add no history entry (see Task 10).
- Claims expire `30 * time.Minute` after their agent's last call: `service.DefaultClaimTTL`.

**Network and MCP:**
- The daemon listens on loopback by default, at `127.0.0.1:7477` (`daemon.DefaultAddr`). It serves MCP at `/mcp`, using the SDK's stateful streamable HTTP with its default DNS-rebinding protection, and Connect at the generated path prefix (`/kiln.v1.KilnService/`) wrapped in `http.NewCrossOriginProtection()`.
- MCP tool schemas describe `google.protobuf.FieldMask` as an array of snake_case paths, matching the proto comments (e.g. `["title", "task.feature_id"]`). protojson accepts only a comma-separated lowerCamel string (`"title,task.featureId"`, verified 2026-11-13), so `internal/mcp` converts the paths before unmarshaling.
- MCP arguments are parsed with `protojson.Unmarshal` (unknown fields rejected). Responses use `protojson.MarshalOptions{UseProtoNames: true, Multiline: true, Indent: "  "}`.

**Runtime files:** these live in `.kiln/run/`, which the daemon gitignores by writing `.kiln/.gitignore` containing `run/`.

**Workflow:** run everything from the repo root. `make test` runs `go test ./...`, and the service and mcp tests must also pass `go test -race`.

## Review Focus

1. **Two agents call `next_task` at the same moment with one task available.** Exactly one gets it, and the other gets no task. Owned by Task 9 (`TestConcurrentNextTask`).
2. **An agent writes from a stale revision after the user changed the item.** It gets `ABORTED`, and the MCP tool error text includes the current item with its new revision, so the agent can recover without another call. Owned by Tasks 6 and 11.
3. **The daemon restarts mid-task.**
   - The claims vanish, so the in-progress task is offered first, as resumed, with its work state.
   - Work state that was updated but not yet recorded gets exactly one `CHANGE_TYPE_WORK_STATE_UPDATED` entry on startup, not one per restart.

   Owned by Task 10.
4. **A task moves into a feature that depends on it.** This is a deadlock through the parent link, rejected with `FAILED_PRECONDITION`, and the file is left unchanged. Owned by Tasks 3 and 6.
5. **An agent sends arguments the way an LLM writes them.** All of these must work: snake_case mask paths, `base_revision` as a number or a string, or the arguments omitted entirely when a tool needs none. An unknown field gets a clear `invalid_argument` tool error, not a protocol error. Owned by Task 11.

---

### Task 1: JSON Schemas from proto messages

**Files:**
- Create: `cmd/protoc-gen-kiln-mcp/schema.go`
- Test: `cmd/protoc-gen-kiln-mcp/schema_test.go`

**Interfaces:**
- Produces: `func inputSchema(m *protogen.Message) (map[string]any, error)`, in package `main`.

**Schema rules** (JSON Schema 2020-12; property names are proto field names):

| Proto | Schema |
|---|---|
| message | `{"type":"object","properties":{…},"additionalProperties":false}` plus `"description"` if non-empty |
| `string`, `bytes` | `{"type":"string"}` |
| `bool` | `{"type":"boolean"}` |
| all integer kinds (incl. 64-bit) | `{"type":"integer"}` |
| `float`, `double` | `{"type":"number"}` |
| enum | `{"type":"string","enum":[<all value names in declaration order>]}` |
| `repeated T` | `{"type":"array","items":<T>}` |
| `map<string, V>` | `{"type":"object","additionalProperties":<V>}` |
| `google.protobuf.Timestamp` | `{"type":"string","format":"date-time"}` |
| `google.protobuf.FieldMask` | `{"type":"array","items":{"type":"string"}}` |
| any other `google.protobuf.*` message | error `unsupported well-known type <full name>` |
| a message that contains itself (directly or indirectly) | error `recursive message <full name>` |

**Descriptions:**
- A field's description is its leading comment, trimmed, with the `//` prefixes and single leading spaces removed (protogen's `Comments.String()` minus the comment markers).
- A message's description is its leading comment.
- For each real (non-synthetic) oneof, append `Set at most one of: a, b, c.` to the containing message's description, separated by a blank line if the description is non-empty.
- Omit empty descriptions.

- [ ] **Step 1: Write the failing tests.** `testPlugin(t)` builds a `*protogen.Plugin` from the compiled-in descriptors. These carry no comments, so descriptions come only from oneofs.

```go
func testPlugin(t *testing.T) *protogen.Plugin {
	t.Helper()
	files := []protoreflect.FileDescriptor{
		descriptorpb.File_google_protobuf_descriptor_proto,
		timestamppb.File_google_protobuf_timestamp_proto,
		fieldmaskpb.File_google_protobuf_field_mask_proto,
		optionsv1.File_kiln_options_v1_mcp_proto,
		kilnv1.File_kiln_v1_item_proto,
		kilnv1.File_kiln_v1_history_proto,
		kilnv1.File_kiln_v1_service_proto,
	}
	// The compiled-in descriptors keep their go_package option (checked
	// 2026-11-13), so no M parameters are needed.
	req := &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{"kiln/v1/service.proto"}}
	for _, fd := range files {
		req.ProtoFile = append(req.ProtoFile, protodesc.ToFileDescriptorProto(fd))
	}
	p, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// schemaOf finds message name in kiln/v1/service.proto and returns its schema
// re-encoded as JSON for comparison.
func schemaOf(t *testing.T, name string) string

func TestSchemaScalars(t *testing.T) {
	// GetItemRequest
	want := `{"additionalProperties":false,"properties":{"id":{"type":"string"}},"type":"object"}`
	if got := schemaOf(t, "GetItemRequest"); got != want { t.Fatalf("got %s", got) }
}

func TestSchemaEnumsAndIntegers(t *testing.T) {
	// SetStatusRequest: base_revision → integer; status → enum of the six STATUS_* names in order.
}

func TestSchemaNestedTypes(t *testing.T) {
	// UpdateItemRequest: update_mask → array of strings;
	// item.create_time → {"format":"date-time","type":"string"};
	// item.task.work_state.extra → {"additionalProperties":{"type":"string"},"type":"object"};
	// item.task.work_state.files → array of strings;
	// item.description (the Item message's description) == "Set at most one of: project, feature, task."
}

func TestSchemaRejectsRecursion(t *testing.T) {
	// A hand-built FileDescriptorProto "test/rec.proto" with message Node { Node child = 1; },
	// with go_package "example.com/rec;rec", passed through protogen.Options{}.New with FileToGenerate ["test/rec.proto"];
	// inputSchema(Node) returns an error containing "recursive message".
}
```

- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./cmd/protoc-gen-kiln-mcp/`
Expected: FAIL to compile, with `undefined: inputSchema`.

- [ ] **Step 3: Implement `inputSchema` in `schema.go`.** Recurse with a stack of message full names to detect recursion. Identify map fields with `field.Desc.IsMap()`, taking the value from `field.Message.Fields[1]`.

- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test ./cmd/protoc-gen-kiln-mcp/`
Expected: PASS.

- [ ] **Step 5: Commit.**
```bash
git add cmd/protoc-gen-kiln-mcp/schema.go cmd/protoc-gen-kiln-mcp/schema_test.go
git commit -m "Generate JSON Schemas from proto request messages"
```

---

### Task 2: Generate the MCP tool table

**Files:**
- Modify: `cmd/protoc-gen-kiln-mcp/main.go` (replace the stub), `buf.gen.yaml` (enable the plugin)
- Create (generated by `make gen`): `gen/go/kiln/v1/kilnv1mcp/service.mcp.go`
- Test: `cmd/protoc-gen-kiln-mcp/generated_test.go`

**Interfaces:**
- Consumes: `inputSchema` (Task 1).
- Produces package `kilnv1mcp` at import path `github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1mcp`:

```go
// KilnServiceToolHandler has one method per KilnService RPC with a
// kiln.options.v1.mcp_tool option, in declaration order.
type KilnServiceToolHandler interface {
	CreateItem(context.Context, *v1.CreateItemRequest) (*v1.CreateItemResponse, error)
	// … GetItem, ListItems, UpdateItem, AddDependency, RemoveDependency, SetStatus,
	// NextTask, ClaimTask, UpdateWorkState, ReleaseTask, GetTaskContext,
	// GetItemChanges, ListHistory, AddDecisionNote
}

type KilnServiceTool struct {
	Name        string          // the mcp_tool name, e.g. "create_item"
	Description string          // the RPC's leading comment
	InputSchema json.RawMessage // compact JSON from inputSchema(request message)
	NewRequest  func() proto.Message
	Call        func(ctx context.Context, h KilnServiceToolHandler, req proto.Message) (proto.Message, error)
}

var KilnServiceTools = []KilnServiceTool{ /* one per annotated RPC, declaration order */ }
```

**Generator rules:**
- Generate a file only for proto files with at least one annotated method. Read the option with `proto.GetExtension(m.Desc.Options(), optionsv1.E_McpTool).(*optionsv1.McpTool)` and skip methods where it is nil or its name is empty.
- File name: `path.Dir(f.GeneratedFilenamePrefix) + "/" + pkg + "/" + path.Base(f.GeneratedFilenamePrefix) + ".mcp.go"`, where `pkg = string(f.GoPackageName) + "mcp"`. The import path is `f.GoImportPath + "/" + pkg`.
- The header is `// Code generated by protoc-gen-kiln-mcp. DO NOT EDIT.` plus `// source: <proto path>`.
- Embed each schema as a Go string literal of its `json.Marshal` output; `encoding/json` sorts map keys, so the output is deterministic.
- `Call` must not return a typed nil. The generated body is:
  ```go
  resp, err := h.X(ctx, req.(*v1.XRequest))
  if err != nil {
      return nil, err
  }
  return resp, nil
  ```
- Streaming RPCs are never annotated; the generator returns an error if one is.

- [ ] **Step 1: Write the failing test** in `generated_test.go` (package `main`; it imports `kilnv1mcp`):

```go
func TestGeneratedTools(t *testing.T) {
	var names []string
	for _, tool := range kilnv1mcp.KilnServiceTools {
		names = append(names, tool.Name)
		var s map[string]any
		if err := json.Unmarshal(tool.InputSchema, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: bad schema %s (%v)", tool.Name, tool.InputSchema, err)
		}
	}
	want := []string{"create_item", "get_item", "list_items", "update_item", "add_dependency",
		"remove_dependency", "set_status", "next_task", "claim_task", "update_work_state",
		"release_task", "get_task_context", "get_item_changes", "list_history", "add_decision_note"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}
	next := kilnv1mcp.KilnServiceTools[7]
	if !strings.HasPrefix(next.Description, "Claim the next task to work on") {
		t.Errorf("next_task description = %q", next.Description)
	}
	if !strings.Contains(string(kilnv1mcp.KilnServiceTools[3].InputSchema), "feature.draft") {
		t.Error("update_item schema lacks the update_mask field comment")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails.**
Run: `go test ./cmd/protoc-gen-kiln-mcp/`
Expected: FAIL, because package `github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1mcp` is not found.

- [ ] **Step 3: Implement `main.go`** with `protogen.Options{}.Run`, using `GeneratedFile.QualifiedGoIdent` for every imported identifier.

- [ ] **Step 4: Enable the plugin in `buf.gen.yaml`.** Replace the commented-out block with:
```yaml
  - local: ["go", "run", "./cmd/protoc-gen-kiln-mcp"]
    out: gen/go
    opt: paths=source_relative
```

- [ ] **Step 5: Generate the code and run the tests.**
Run: `make gen && go build ./... && go test ./cmd/protoc-gen-kiln-mcp/ && make check-gen`
Expected:
- `gen/go/kiln/v1/kilnv1mcp/service.mcp.go` exists and the build succeeds.
- The tests PASS.
- `check-gen` exits 0.

- [ ] **Step 6: Commit.**
```bash
git add cmd/protoc-gen-kiln-mcp/main.go cmd/protoc-gen-kiln-mcp/generated_test.go buf.gen.yaml gen/go/kiln/v1/kilnv1mcp
git commit -m "Generate MCP tool definitions from annotated RPCs"
```

---

### Task 3: Core rules carried over from step 1

**Files:**
- Modify: `internal/core/validate.go`, `internal/core/graph.go`
- Test: `internal/core/validate_test.go`, `internal/core/graph_test.go`

**Interfaces:**
- Produces: `func HasCycle(ids []string, g Graph) bool`. It is true if any dependency held by any of the items in `ids` lies on a cycle in `g`.

  Every cycle passes through at least one dependency edge, because the parent edges alone are acyclic. So `HasCycle` can call `WouldCreateCycle(id, dep.item_id, g)` for each dependency of each id.
- Changes `CheckUpdate`: a mask that names `feature.project_id` or `task.feature_id` with an empty value fails with `ErrInvalidArgument`.

- [ ] **Step 1: Write the failing tests.**
  - Add to `TestCheckUpdate`'s table: masks of `feature.project_id` with an empty `project_id`, and `task.feature_id` with an empty `feature_id`. Both expect `ErrInvalidArgument`.
  - `TestHasCycle`, using the existing `mapGraph`:
    - **No cycle:** F1 has T1, F2 has no tasks, and F2 depends on T1. `HasCycle([F2], g)` is false.
    - **Cycle:** in the same graph, change T1's `feature_id` to F2 (`mapGraph.Tasks` derives a feature's tasks from its items). `HasCycle([F2, T1], g)` is true.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/core/`
Expected: FAIL, with `undefined: core.HasCycle` (and the new table rows failing once that compiles).
- [ ] **Step 3: Implement both changes.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test ./internal/core/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/core
git commit -m "Reject empty parent IDs and detect cycles after reparenting"
```

---

### Task 4: Markdown task context view

**Files:**
- Create: `internal/views/task.go`
- Test: `internal/views/task_test.go`

**Interfaces:**
- Produces:
```go
type Dependency struct {
	ID         string
	BlockUntil kilnv1.BlockUntil // UNSPECIFIED renders as "done"
	Item       *kilnv1.ItemInfo  // nil if the item no longer exists
}

type TaskContext struct {
	Task                *kilnv1.ItemInfo
	Feature             *kilnv1.ItemInfo // nil if missing
	Project             *kilnv1.Item     // nil if missing
	Dependencies        []Dependency     // the task's
	FeatureDependencies []Dependency     // the feature's
	Siblings            []*kilnv1.ItemInfo // every task in the feature, rank order, including Task
	Resumed             bool
}

func RenderTaskContext(c TaskContext) string
func StatusName(s kilnv1.Status) string // "draft", "ready", "in progress", "in review", "done"; UNSPECIFIED → "unknown"
```

**Exact format.** Sections are separated by one blank line. Timestamps are RFC 3339 in UTC, and the output ends with a single `\n`.

```markdown
# Task: <task title>

- ID: `<id>`
- Revision: <revision>
- Status: <status>
- Claimed by: agent `<agent_id>` until <expire_time>      ← only if claimed
- Feature: <feature title> (`<feature id>`)               ← "Feature: missing (`<feature_id>`)" if nil
- Project: <project title> (`<project id>`)               ← omitted if Project is nil

> **Resumed task:** another agent started this task and stopped before finishing. Read the work state below and continue from it instead of starting over; the branch may hold partial work.
                                                            ← only if Resumed
## Description

<task description, or "_No description._">

## Dependencies

- [<status>] <title> (`<id>`), blocks until <done|in review>[, satisfied]
- [missing] `<id>`, blocks until <done|in review>
                                                            ← or "None." ; ", satisfied" when it no longer blocks
## Work state

- Branch: `<branch>`
- Worktree: `<worktree_path>`
- Base commit: `<base_commit>`
- Head commit: `<head_commit>`
- Pull request: <pull_request_url>
- Session: <host> <host_version>, session `<session_id>`, model `<model>`, transcript `<transcript_path>`
- Files: `<f1>`, `<f2>`
- Credentials needed (names only): `<c1>`, `<c2>`
- <extra key>: <extra value>                              ← one line per key, keys sorted
- Updated: <update_time>
                                                            ← omit any line whose value is empty; Session omits
                                                              empty parts; "None recorded yet." if work_state is nil
### Progress                                               ← only if progress is non-empty

<progress>

## Feature: <feature title>                                ← whole section omitted if Feature is nil

- ID: `<id>`
- Revision: <revision>
- Status: <status>

<feature description, or "_No description._">

### Feature dependencies                                   ← only if FeatureDependencies is non-empty

<same line format as Dependencies>

### Tasks in this feature

1. [<status>] <title> (`<id>`)
2. [<status>] <title> (`<id>`) ← this task
```

A dependency is satisfied when its item exists and either its status is `done`, or `block_until` is IN_REVIEW and its status is `in review`.

- [ ] **Step 1: Write the failing tests.**
  - `TestRenderTaskContextFull` builds a fixture with:
    - a claimed, resumed task with every work-state field set, one `extra` key and two files
    - one satisfied dependency, one blocking dependency and one missing dependency
    - a feature with one dependency
    - three siblings

    It compares the output to an exact expected string written out in the test.
  - `TestRenderTaskContextMinimal` uses a task with no description, no dependencies, no work state, no claim and no project, and a nil Feature. It asserts that:
    - the output contains `"_No description._"`, `"## Dependencies\n\nNone.\n"` and `"## Work state\n\nNone recorded yet.\n"`
    - the output does not contain `"## Feature:"`, `"Resumed"`, `"Claimed by"` or `"Project:"`
  - `TestStatusName` covers all six values.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/views/`
Expected: FAIL, with `undefined: views.RenderTaskContext`.
- [ ] **Step 3: Implement `task.go`** using `strings.Builder`.
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test ./internal/views/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/views
git commit -m "Render the markdown task context view"
```

---

### Task 5: Service foundation: callers, loading, item info, reads

**Files:**
- Create:
  - `internal/service/caller.go`
  - `internal/service/service.go` (struct, `New`, `begin`, `commit`)
  - `internal/service/info.go` (effective status, blocked, ItemInfo, sorting)
  - `internal/service/errors.go`
  - `internal/service/read.go` (GetItem, ListItems)
- Test: `internal/service/helpers_test.go`, `internal/service/read_test.go`. Tests use package `service_test`.

**Interfaces:**
- Consumes: `store.Open`, `(*store.Store).ListItems/PutItem/AppendHistory`, `core.*`.
- Produces:
```go
type Caller struct {
	Actor       kilnv1.ActorType
	AgentID     string // MCP session ID; empty for the user
	Host        string // MCP clientInfo.name
	HostVersion string // MCP clientInfo.version
}
func WithCaller(ctx context.Context, c Caller) context.Context
func CallerFrom(ctx context.Context) (Caller, bool)

const DefaultClaimTTL = 30 * time.Minute
type Options struct {
	Now      func() time.Time // nil → time.Now
	ClaimTTL time.Duration    // 0 → DefaultClaimTTL
	Logger   *slog.Logger     // nil → discard
}
type Service struct{ /* mu sync.Mutex; store; items map[string]*kilnv1.Item; … */ }
func New(st *store.Store, opts Options) (*Service, []*store.FileError, error)

// ConflictError is returned for a stale base_revision; it matches core.ErrRevisionConflict.
type ConflictError struct{ Current *kilnv1.ItemInfo }
func (e *ConflictError) Error() string // "revision conflict: item <id> is at revision <n>"
func (e *ConflictError) Unwrap() error // core.ErrRevisionConflict

// ErrorCode maps an error to its Connect code: the five core sentinels as in step 1, else CodeInternal.
func ErrorCode(err error) connect.Code

func (s *Service) GetItem(context.Context, *kilnv1.GetItemRequest) (*kilnv1.GetItemResponse, error)
func (s *Service) ListItems(context.Context, *kilnv1.ListItemsRequest) (*kilnv1.ListItemsResponse, error)
```
- Internal helpers that later tasks use:
  - `begin(ctx) (Caller, func(), error)`: locks, reads the caller (`PERMISSION_DENIED` "no caller" if absent), and returns the unlock function.
  - `commit(caller, item, typ kilnv1.ChangeType, fields []string) error`: implements the write sequence from Global Constraints. The revision is set to 1 for CREATED and incremented otherwise. For agents, the actor is `{AGENT, agent_id, session{host, host_version}}`.
  - `info(item) *kilnv1.ItemInfo`: a clone, with status, blocked and claim filled in.
  - `checkBase(item, base int64) error`: returns a `*ConflictError` on mismatch.
  - `sortedFeatures()` and `sortedTasks(featureID)`: by rank, ties broken by ID.
- Test helpers in `helpers_test.go`:
```go
type clock struct{ t time.Time }        // starts 2026-11-13T10:00:00Z; Now(); Advance(d)
func id(n int) string                   // fmt.Sprintf("01JD%022d", n), a valid ULID
func proj(n int) *kilnv1.Item
func feat(n, project int, rank string, deps ...*kilnv1.Dependency) *kilnv1.Item   // draft false, revision 1
func task(n, feature int, rank string, st kilnv1.Status, deps ...*kilnv1.Dependency) *kilnv1.Item
func dep(n int, until kilnv1.BlockUntil) *kilnv1.Dependency
func newService(t *testing.T, items ...*kilnv1.Item) (*service.Service, *clock, string /*repo*/) // seeds via store.PutItem, then service.New
var user = service.WithCaller(context.Background(), service.Caller{Actor: kilnv1.ActorType_ACTOR_TYPE_USER})
func agent(name string) context.Context // Actor AGENT, AgentID name, Host "test-host", HostVersion "1.0"
func wantCode(t *testing.T, err error, code connect.Code)
```

**Rules:**
- **Effective status:**
  - task: its `status`
  - feature: `core.DeriveFeatureStatus` over its tasks' statuses
  - project: UNSPECIFIED
- **Blocked:**
  - A feature is blocked when `core.IsBlocked(feature.depends_on, effectiveStatus)` is true.
  - A task is blocked when its own dependencies block it or its feature is blocked; a task waits for its feature to start.
  - Projects are never blocked.
- **`ListItems` order:**
  - Projects come first, by ID.
  - Then features, in feature-rank order.
  - Then tasks, grouped by feature in feature-rank order and by task rank within each feature.
  - A task whose feature is missing comes last, ordered by ID.
- **`ListItems` filters** are ANDed:
  - `kind`
  - `project_id`: features with that project, and tasks whose feature has it
  - `feature_id`: tasks only
  - `statuses`: the effective status

  A non-empty `project_id` or `feature_id` that isn't a valid ID is `INVALID_ARGUMENT`.
- **Invalid IDs:** `GetItem` with an invalid ID is `INVALID_ARGUMENT`; with an unknown ID it is `NOT_FOUND`.
- **Loading:** `New` loads all items from the store. It returns the store's `FileError`s, which it also logs at Warn, and keeps the items that did load.

- [ ] **Step 1: Write the helpers and the failing tests in `read_test.go`.**
  - `TestCallerRequired`: `GetItem` with `context.Background()` gives `PERMISSION_DENIED`.
  - `TestGetItem`:
    - A seeded task comes back with `item.revision == 1` and `status == READY`.
    - An unknown ID gives `NOT_FOUND`.
    - `"../x"` gives `INVALID_ARGUMENT`.
  - `TestNewReportsBadFiles`: write `.kiln/items/<id(99)>.json` containing `{` beside one valid project. `New` returns exactly 1 FileError, and `GetItem` of the project works.
  - `TestResponsesAreCopies`: change `resp.Item.Item.Title` and call `GetItem` again; the title is unchanged.
  - `TestItemInfoDerived`: F1 has T1 (IN_PROGRESS) and T2 (READY); F2 depends on T2 (BLOCK_UNTIL_DONE) and has T3 (READY). Then:
    - F1's status is IN_PROGRESS.
    - F2's status is READY with `blocked == true`.
    - T3 is blocked through F2.
    - T1 is not blocked.
  - `TestListItemsOrderAndFilters`:
    - Seed P1 and P2; features A (P1, rank "m"), B (P2, rank "c") and C (P1, rank "x"); tasks A2 ("t"), A1 ("g") and B1 ("k").
    - With no filter, the IDs come back as `P1 P2 B A C B1 A1 A2`.
    - With `project_id=P1`, they are `A C A1 A2`.
    - With `kind=TASK` and `statuses=[READY]`, they are the ready tasks in that order.
    - A `feature_id` of `"bad"` gives `INVALID_ARGUMENT`.
  - `TestErrorCode`: each sentinel maps to its code, and a wrapped `&ConflictError{}` maps to `CodeAborted`.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/`
Expected: FAIL, with `undefined: service.New` (and the others).
- [ ] **Step 3: Implement the files listed above.** Add `connectrpc.com/connect` imports only in `errors.go`.
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Add service foundation: callers, loading, item info and reads"
```

---

### Task 6: Create, update, delete and move items

**Files:**
- Create: `internal/service/items.go`
- Test: `internal/service/items_test.go`

**Interfaces:**
- Consumes: `begin`, `commit`, `info` and `checkBase` (Task 5); `core.NormalizeNewItem`, `core.CheckUpdate`, `core.HasCycle` and `core.RankBetween`.
- Produces `CreateItem`, `UpdateItem`, `DeleteItem` and `MoveItem` on `*Service`, with the generated request and response types, plus the graph adapter that Tasks 7 and 9 use:
  ```go
  type graph struct{ s *Service } // implements core.Graph; Tasks() returns IDs in sortedTasks order
  ```

**Rules:**
- **CreateItem**
  1. Run `core.NormalizeNewItem(caller.Actor, item)`.
  2. Check the parent. A feature's `project_id` and a task's `feature_id` must be valid IDs (`INVALID_ARGUMENT` if not) of existing items (`NOT_FOUND`) of the right kind (`INVALID_ARGUMENT`).
  3. Assign the ID with `core.NewID()` and `create_time = update_time = now`.
  4. Rank: a feature's rank is `RankBetween(<last feature's rank>, "")` across all features; a task's is the same within its feature.
  5. `commit` with CREATED and no fields.
- **UpdateItem**
  1. The item must exist, then `checkBase`, then `core.CheckUpdate`.
  2. Apply only the masked paths, to a clone.
  3. Check a new parent the same way CreateItem does.
  4. Moving a task to another feature gives it the bottom rank there, and adds `task.rank` to the changed fields.
  5. After reparenting a task, build the hypothetical graph from the clone and call `core.HasCycle` over the task and every item that depends on it or on its new feature. A cycle is `FAILED_PRECONDITION`, and nothing is written.
  6. `commit` with UPDATED, and the changed fields set to the mask paths in mask order (plus `task.rank`).
- **DeleteItem** (user only; agents get `PERMISSION_DENIED`)
  1. The item must exist, then `checkBase`.
  2. It fails with `FAILED_PRECONDITION` if the item has children or any item depends on it.
  3. Delete the file, then append a `DELETED` entry with `revision = current + 1` and no snapshot.
  4. Remove the item from memory.
- **MoveItem** (user only)
  1. Only features and tasks can move; a project is `INVALID_ARGUMENT`.
  2. Set exactly one of `before_id` or `after_id` (`INVALID_ARGUMENT` otherwise). It must name an existing (`NOT_FOUND`) sibling other than the item itself (`INVALID_ARGUMENT` otherwise). A sibling is any feature, or a task in the same feature.
  3. The new rank lies between the neighbors in sibling order with the moved item excluded:
     - `before_id=X`: between X's predecessor and X
     - `after_id=X`: between X and its successor
  4. If the neighbors' ranks are equal (possible after a merge), fail with `FAILED_PRECONDITION` "ranks tied; move a neighbor first".
  5. `commit` with MOVED and `["feature.rank"]` or `["task.rank"]`.

- [ ] **Step 1: Write the failing tests.**
  - `TestCreateItem`: as the user, create P, then F1 and F2 in P, then T1 in F1. Check:
    - Every revision is 1 and every ID is valid.
    - `F1.rank < F2.rank`.
    - T1's status is READY.
    - `store.ListHistory(F1)` has one CREATED entry whose snapshot equals the F1 in the response.
  - `TestCreateItemParents`:
    - A feature with a valid but unknown `project_id` gives `NOT_FOUND`.
    - A task whose `feature_id` is a project gives `INVALID_ARGUMENT`.
    - A task whose `feature_id` is `"x"` gives `INVALID_ARGUMENT`.
  - `TestAgentCreatesDraftFeature`: as the agent, the created feature has `draft == true` and status DRAFT, and the history actor is `{AGENT, "a1", session.host "test-host"}`.
  - `TestUpdateItem`: change the title with base 1. The result is revision 2, and the history entry is UPDATED with `changed_fields ["title"]`.
  - `TestUpdateItemStale`: `base_revision 0` gives `ABORTED`, `errors.As` yields a `*ConflictError` with `Current.Item.Revision == 1`, and the file still has revision 1.
  - `TestAgentCannotPromoteDraft`: an agent updates with mask `feature.draft` and `draft=false`, and gets `PERMISSION_DENIED`.
  - `TestReparentTask`: moving T1 from F1 to F2 puts it at the bottom of F2's ranks, with changed fields `["task.feature_id","task.rank"]`.
  - `TestReparentTaskCycle`: F1 has T1, and F2 depends on T1. Moving T1 into F2 gives `FAILED_PRECONDITION`, and T1's file still has F1.
  - `TestDeleteItem`:
    - The user deletes a leaf task; the file is gone and a DELETED entry is appended.
    - An agent gets `PERMISSION_DENIED`.
    - A feature with tasks gives `FAILED_PRECONDITION`.
    - An item another item depends on gives `FAILED_PRECONDITION`.
  - `TestMoveItem`:
    - With features A, B and C in order, moving C before A makes `ListItems` features C, A, B.
    - Moving A after B gives C, B, A.
    - Moving a task with `after_id` set to a task of another feature gives `INVALID_ARGUMENT`.
    - An agent gets `PERMISSION_DENIED`.
    - Two seeded features with rank "m", moving a third between them, gives `FAILED_PRECONDITION`.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/ -run 'Create|Update|Reparent|Delete|Move|Draft'`
Expected: FAIL, with undefined methods.
- [ ] **Step 3: Implement `items.go`.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Create, update, delete and move items in the service"
```

---

### Task 7: Dependencies and status

**Files:**
- Create: `internal/service/deps.go`, `internal/service/status.go`
- Test: `internal/service/deps_test.go`, `internal/service/status_test.go`

**Interfaces:**
- Produces `AddDependency`, `RemoveDependency` and `SetStatus` on `*Service`. It also produces `setStatusLocked(caller, item, to) error`, which Task 9 extends with claim rules.

**Rules:**
- **AddDependency**
  1. The blocked item must exist (`NOT_FOUND`), then `checkBase`.
  2. Both the blocked item and the blocker must be features or tasks; anything else is `INVALID_ARGUMENT`. A missing blocker is `NOT_FOUND`.
  3. A duplicate dependency is `FAILED_PRECONDITION` "already depends on <id>; remove it first".
  4. If `core.WouldCreateCycle(item, dep, graph{s})`, fail with `FAILED_PRECONDITION`.
  5. Store `block_until` UNSPECIFIED as `BLOCK_UNTIL_DONE`.
  6. `commit` with DEPENDENCY_ADDED, and fields `["feature.depends_on"]` or `["task.depends_on"]`.
- **RemoveDependency:** check the base revision first. If the dependency isn't present, fail with `NOT_FOUND`. Otherwise `commit` with DEPENDENCY_REMOVED and the same fields.
- **SetStatus**
  1. The item must exist, then `checkBase`, then `core.CheckStatusChange`.
  2. Setting the status the item already has (a feature's current `status_override`, or a task's current `status`) writes nothing and returns the current info.
  3. A feature sets `status_override`, with field `feature.status_override`. A task sets `status`, with field `task.status`.
  4. `commit` with STATUS_CHANGED.

- [ ] **Step 1: Write the failing tests.**
  - `TestAddDependency`: T2 depends on T1 with `block_until` UNSPECIFIED. The stored value is DONE, and T2 is blocked.
  - `TestAddDependencyRules`:
    - Adding T1→T2 after T2→T1 gives `FAILED_PRECONDITION` (cycle).
    - A dependency on a project gives `INVALID_ARGUMENT`.
    - A duplicate gives `FAILED_PRECONDITION`.
    - An unknown blocker gives `NOT_FOUND`.
    - A stale base gives `ABORTED`.
  - `TestBlockUntilInReview`: T2 depends on T1 with IN_REVIEW. The user sets T1 to IN_REVIEW, and T2 becomes unblocked.
  - `TestRemoveDependency`: a removed dependency unblocks the item; removing it again gives `NOT_FOUND`.
  - `TestSetStatusPermissions`:
    - An agent setting a task to DONE gets `PERMISSION_DENIED`.
    - The user setting it to DONE gets revision +1 and a STATUS_CHANGED entry with `["task.status"]`.
    - An agent setting a feature override gets `PERMISSION_DENIED`.
    - The user sets the feature override to IN_REVIEW, and then UNSPECIFIED clears it back to the derived status.
  - `TestSetStatusNoop`: setting the current status leaves the revision unchanged and adds no history entry.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/ -run 'Depend|BlockUntil|SetStatus'`
Expected: FAIL, with undefined methods.
- [ ] **Step 3: Implement `deps.go` and `status.go`.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Add dependencies and status changes to the service"
```

---

### Task 8: History RPCs

**Files:**
- Create: `internal/service/history.go`
- Test: `internal/service/history_test.go`

**Interfaces:**
- Produces `GetItemChanges`, `ListHistory` and `AddDecisionNote` on `*Service`.

**Rules:**
- History `FileError`s are logged at Warn and skipped.
- **GetItemChanges:** returns the change entries with `revision > since_revision`, ordered by entry ID. It works for deleted items that still have history. If the item doesn't exist and has no history, it is `NOT_FOUND`.
- **ListHistory:**
  - The item must exist.
  - `include_children` adds the history of a feature's tasks, or a project's features and their tasks, using the current items.
  - The entries are merged and sorted by entry ID.
  - `notes_only` keeps only entries that have a `note`.
- **AddDecisionNote:**
  - The item must exist.
  - A blank body (after `TrimSpace`) is `INVALID_ARGUMENT`.
  - The entry's `item_revision` is the item's current revision, and its actor comes from the caller.
  - It doesn't change the item.

- [ ] **Step 1: Write the failing tests.**
  - `TestGetItemChanges`: create a task, then change its title twice. Since revision 1, the result is two entries with revisions 2 and 3, and the last snapshot's title is current. Since revision 3, the result is empty.
  - `TestGetItemChangesDeleted`: delete a task. Its changes since 0 end with a DELETED entry.
  - `TestGetItemChangesUnknown`: an unknown valid ID gives `NOT_FOUND`.
  - `TestListHistoryChildren`: for a feature with two tasks, `include_children` returns the CREATED entries of all three, in ID order; without it, only the feature's.
  - `TestDecisionNotes`:
    - Add a note at revision 1. The revision is unchanged, and the entry's `item_revision` is 1.
    - `notes_only` returns just the note.
    - A blank body gives `INVALID_ARGUMENT`.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/ -run 'History|Changes|Decision'`
Expected: FAIL.
- [ ] **Step 3: Implement `history.go`.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Add history, item changes and decision notes to the service"
```

---

### Task 9: Claims, next_task and task context

**Files:**
- Create: `internal/service/claims.go` (claim map, expiry, renewal, affinity), `internal/service/next.go` (NextTask, ClaimTask, ReleaseTask), `internal/service/context.go` (GetTaskContext and building `views.TaskContext`)
- Modify: `internal/service/service.go` (`begin` sweeps and renews), `internal/service/status.go`, `internal/service/items.go` (DeleteItem), `internal/service/info.go` (claim in ItemInfo)
- Test: `internal/service/claims_test.go`

**Interfaces:**
- Consumes: `views.RenderTaskContext` (Task 4), `setStatusLocked` (Task 7).
- Produces:
  - `NextTask`, `ClaimTask`, `ReleaseTask` and `GetTaskContext` on `*Service`.
  - `endClaim(taskID string)`: the single place a claim ends. Task 10 hooks work-state recording into it.

**State:**
- `claims map[taskID]{agentID string; expire time.Time}`
- `affinity map[agentID]featureID`: the feature of the task the agent most recently claimed. It is kept after the task leaves IN_PROGRESS.

**Rules:**
- **`begin`**, on every call:
  1. End every claim whose `expire <= now`. Expiry is checked lazily, with no goroutine.
  2. If the caller is an agent, set each of its claims to expire at `now + ClaimTTL`.
- **ItemInfo:** `ItemInfo.claim` is `{agent_id, expire_time}` for claimed tasks.
- **Candidates for NextTask:**
  - A task is a candidate when it is unclaimed, its feature exists and is not a draft, it matches the `project_id` filter if set, and either:
    - it is READY and not blocked, or
    - it is IN_PROGRESS. These are *abandoned* tasks, and their blocking isn't checked.
- **Candidate order** for agent A:
  1. Abandoned tasks, by feature rank and then task rank.
  2. Ready tasks in A's affinity feature, by task rank.
  3. Ready tasks in features where no other agent holds a claim, by feature rank and then task rank.
  4. All other ready tasks, in the same order.
- **Claiming** a task for agent A:
  - Create the claim (`expire = now + TTL`) and set `affinity[A]` to the task's feature.
  - A READY task moves to IN_PROGRESS through `setStatusLocked`, which bumps the revision and adds a STATUS_CHANGED entry.
  - An abandoned task keeps its revision.
  - Return the info and the context from `RenderTaskContext`, with `Resumed` true when the task was abandoned.
- **NextTask:**
  - Agents only; the user gets `FAILED_PRECONDITION` "only agents claim tasks".
  - If there's no candidate, return an empty response.
  - An invalid `project_id` is `INVALID_ARGUMENT`.
- **ClaimTask:**
  - Agents only. The item must exist and be a task (`INVALID_ARGUMENT` otherwise).
  - Re-claiming your own claim renews it and returns the context.
  - A task claimed by another agent is `FAILED_PRECONDITION`.
  - Otherwise the task must satisfy the candidate rules apart from the `project_id` filter; if not, fail with `FAILED_PRECONDITION` and name the reason (draft feature, blocked, or status).
- **ReleaseTask:**
  - The caller must be the claim holder or the user; otherwise it is `FAILED_PRECONDITION`. An unclaimed task is `FAILED_PRECONDITION` "not claimed".
  - An IN_PROGRESS task goes back to READY through `setStatusLocked`; a task in any other status only loses its claim.
  - Ends the claim with `endClaim`.
- **SetStatus additions** (in `setStatusLocked`):
  - An agent changing a task claimed by another agent gets `FAILED_PRECONDITION`.
  - An agent setting IN_PROGRESS without holding the claim gets `FAILED_PRECONDITION` "claim the task with claim_task".
  - Any change that moves a claimed task out of IN_PROGRESS ends its claim with `endClaim`.
- **DeleteItem:** a claimed task is `FAILED_PRECONDITION`.
- **GetTaskContext:**
  - The item must be a task.
  - `Resumed` is true when the task is IN_PROGRESS and unclaimed.
  - The response carries the context and the revision.

- [ ] **Step 1: Write the failing tests.** All of them use `clock`.
  - `TestNextTaskSpreadsAgents`: features A ("a") and B ("b"); tasks A1, A2 and B1, all READY.
    - Agent a1 gets A1.
    - a2 gets B1, because A has a claim by a1.
    - a3 gets A2.
    - a4 gets an empty response.
    - A1's status is IN_PROGRESS at revision 2, and its claim's `agent_id` is "a1".
  - `TestNextTaskAffinity`: A ("a") has A1; B ("b") has B1 and B2.
    1. a1 calls `ClaimTask(B1)`, then sets it to IN_REVIEW.
    2. a1's `NextTask` gives B2, not A1.
  - `TestNextTaskSkips`: seed six READY tasks, each with one reason to be skipped:
    - in a draft feature
    - blocked by an unfinished task
    - in a blocked feature
    - claimed by another agent
    - DRAFT itself
    - in another project, when `project_id` is set

    The response has no task.
  - `TestClaimExpiresAndResumes`:
    1. a1 takes T1.
    2. Advance the clock 31 minutes.
    3. a2's `NextTask` returns T1 at the same revision, with the claim held by a2 and the context containing `"**Resumed task:**"`.
  - `TestClaimRenews`:
    1. a1 takes T1.
    2. Advance 20 minutes, and a1 calls `GetItem`.
    3. Advance 20 more minutes. a2's `NextTask` returns no task.
  - `TestClaimTask`:
    - Claiming a task held by another agent gives `FAILED_PRECONDITION`.
    - Claiming a blocked task gives `FAILED_PRECONDITION`.
    - The user calling `NextTask` gets `FAILED_PRECONDITION`.
    - Re-claiming your own claim succeeds.
  - `TestReleaseTask`:
    - The task returns to READY with no claim.
    - a2 releasing a1's claim gets `FAILED_PRECONDITION`.
    - The user can release a1's claim.
  - `TestSetStatusClaimRules`:
    - a2 sets IN_REVIEW on a1's task: `FAILED_PRECONDITION`.
    - a2 sets IN_PROGRESS on an unclaimed READY task: `FAILED_PRECONDITION`.
    - a1 sets its own task to IN_REVIEW; the claim is gone.
  - `TestConcurrentNextTask`: one READY task and 10 goroutines, each a distinct agent calling `NextTask`. Exactly one response has a task.
  - `TestGetTaskContext`: the context starts with `"# Task: "`, the revision matches, and a1's claim is in the text.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/ -run 'Next|Claim|Release|TaskContext'`
Expected: FAIL.
- [ ] **Step 3: Implement the code.**
- [ ] **Step 4: Run the tests to verify they pass, including earlier tasks' tests.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Add claims, next_task with affinity, and task context"
```

---

### Task 10: Work state

**Files:**
- Create: `internal/service/workstate.go`
- Modify: `internal/service/service.go` (`commit` merges pending fields; `New` reconciles), `internal/service/claims.go` (`endClaim` records pending)
- Test: `internal/service/workstate_test.go`

**Interfaces:**
- Produces `UpdateWorkState` on `*Service`.

**State:** `pending map[taskID]{agentID string; fields map[string]bool}`, holding the work-state paths changed since the task's last history entry.

**Rules:**
- **UpdateWorkState**
  1. The item must be a task (`NOT_FOUND` or `INVALID_ARGUMENT` otherwise). The caller must hold the claim or be the user; otherwise it is `FAILED_PRECONDITION`.
  2. Mask paths are top-level WorkState field names: `branch`, `worktree_path`, `base_commit`, `head_commit`, `pull_request_url`, `session`, `progress`, `files`, `credential_refs` or `extra`. Any other path is `INVALID_ARGUMENT`, and an empty mask means all of them.
  3. Copy the masked fields onto a clone of the current work state; a nil work state starts empty.
  4. If the caller is an agent, set `session.host` and `session.host_version` from the caller, overriding the request.
  5. Set `update_time = now`.
  6. Run `core.CheckWorkState`; a failure is `INVALID_ARGUMENT`.
  7. `PutItem` without changing the revision or `update_time`.
  8. Add `task.work_state.<path>` for each masked path to `pending[task]`, with the caller's agent ID.
- **`commit`** for a task with pending fields appends them, sorted, after the change's own fields, then clears the pending entry.
- **`endClaim`**: if the task has pending fields, append a `WORK_STATE_UPDATED` entry with the current revision, the snapshot, the pending fields, and actor `{AGENT, pending.agentID, work_state.session}`; then clear the pending entry.
- **Reconciliation in `New`**: for each task with a non-nil work state, compare it with the `task.work_state` of the snapshot in its latest ChangeEvent (by entry ID).
  - If the two aren't `proto.Equal`, append a `WORK_STATE_UPDATED` entry: actor `{AGENT, "", work_state.session}`, with the fields being the top-level WorkState fields that differ.
  - A task with no ChangeEvent counts as differing from an empty work state.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpdateWorkState`: a1 claims T1 (revision 2), then records `branch` and `progress`. Then:
    - The revision is still 2, and the history entry count is unchanged.
    - `store.GetItem` has the branch.
    - `session.host` is "test-host" even though the request set "evil".
  - `TestUpdateWorkStateMask`: a second update with mask `["progress"]` keeps the branch.
  - `TestUpdateWorkStatePermission`: a2 gets `FAILED_PRECONDITION`, and the user succeeds.
  - `TestUpdateWorkStateRejectsSecret`: progress set to `"token ghp_" + strings.Repeat("a", 36)` gives `INVALID_ARGUMENT`, and `err.Error()` doesn't contain `"ghp_"`.
  - `TestWorkStateRecordedInNextEntry`: after the branch update, a1 sets IN_REVIEW. The entry's `changed_fields` is `["task.status","task.work_state.branch"]`, and its snapshot has the branch.
  - `TestWorkStateRecordedOnRelease`: an update followed by `ReleaseTask` records it in the release's STATUS_CHANGED entry. With the task already IN_REVIEW, the update gets its own WORK_STATE_UPDATED entry with `agent_id "a1"`.
  - `TestWorkStateRecordedOnExpiry`: update, advance 31 minutes, then any call. There is one WORK_STATE_UPDATED entry by a1.
  - `TestRestartRecordsPendingOnce`:
    1. Update the work state, then build a new `service.New` over the same repo. There is one WORK_STATE_UPDATED entry, with `changed_fields` containing `"branch"`.
    2. Build a third service. There is still one.
  - `TestResumedContextHasWorkState`: after the expiry, a2's `NextTask` context contains "- Branch: `kiln/t1`".
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/service/ -run 'WorkState|Restart|Resumed'`
Expected: FAIL.
- [ ] **Step 3: Implement the code.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/service/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/service
git commit -m "Record agent work state, once per session in history"
```

---

### Task 11: MCP server

**Files:**
- Create:
  - `internal/mcp/server.go` (`NewServer`, `NewHandler`, the tool handler)
  - `internal/mcp/args.go` (argument normalization)
  - `internal/mcp/result.go` (results and tool errors)
- Test: `internal/mcp/args_test.go`, `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `kilnv1mcp.KilnServiceTools` and `KilnServiceToolHandler` (Task 2), which `*service.Service` satisfies; `service.WithCaller`, `service.ErrorCode` and `*service.ConflictError`.
- Produces (import the SDK as `sdk "github.com/modelcontextprotocol/go-sdk/mcp"`):
```go
func NewServer(svc *service.Service, version string) *sdk.Server // Implementation{Name: "kiln", Version: version}
func NewHandler(srv *sdk.Server) http.Handler                    // sdk.NewStreamableHTTPHandler, stateful, SessionTimeout 2h
func normalizeArgs(raw json.RawMessage, md protoreflect.MessageDescriptor) ([]byte, error)
```

**Rules:**
- **Add the dependency:** `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`.
- **Tools:** register every `KilnServiceTools` entry with `srv.AddTool`, using `InputSchema` as a `json.RawMessage`.
- **Caller:** `{AGENT, req.Session.ID(), clientInfo.Name, clientInfo.Version}` from `req.Session.InitializeParams()`, which is nil-safe.
- **`normalizeArgs`:**
  - Empty or `null` arguments become `{}`.
  - Decode with `UseNumber`, then walk the object against `md`:
    - For a FieldMask field holding an array of strings, or a string, convert each path's dot-separated segments from snake_case to lowerCamel (`feature_id` → `featureId`) and join them with `,`.
    - Recurse into message fields and into arrays of messages.
  - Leave unknown keys alone; protojson rejects them next.
  - Re-encode the result.
- **Success:**
  - `StructuredContent` is `json.RawMessage` of the protojson response.
  - `Content` is one `TextContent` whose text is chosen in this order:
    1. the response's top-level `context` string field, if it has one and it's non-empty
    2. `"No task is available: nothing is ready, unblocked and unclaimed."` for an empty `NextTaskResponse`
    3. otherwise, the protojson.
- **Failure** (a decode error or a service error): `IsError: true`, with text `"<code>: <message>"`. The code is `service.ErrorCode(err).String()` (e.g. `aborted`); decode errors use `invalid_argument`.
  - For a `*ConflictError`, append `"\n\nCurrent item:\n"` and the protojson of `Current`.
  - Never return a Go error from the handler; that would become a protocol error the model can't see.

- [ ] **Step 1: Write the failing tests.**
  - `TestNormalizeArgs`, table-driven over `UpdateItemRequest`'s descriptor:
    - `{"update_mask":["title","task.feature_id"]}` → `{"update_mask":"title,task.featureId"}`
    - `{"update_mask":"task.feature_id"}` → `{"update_mask":"task.featureId"}`
    - `null` → `{}`
    - `{"base_revision":"3"}` stays as it is.
  - `server_test.go`: `start(t)` creates a temp repo, `service.New`, and an `httptest.NewServer(NewHandler(NewServer(svc, "test")))`. `connect(t, url, name)` returns a `*sdk.ClientSession` using `&sdk.StreamableClientTransport{Endpoint: url}` and `Implementation{Name: name, Version: "9.9"}`. (In-memory transports have no session ID, so tests must use HTTP.)
    - `TestToolsList`: there are 15 tools; `create_item`'s schema has `properties.item`.
    - `TestCreateItemAsAgent`:
      - Create a project, then a feature, through `CallTool`.
      - The feature's structured content has `draft: true`.
      - The feature's history actor has `agent_id == session.ID()` and `session.host == "claude-code"` (the client name).
    - `TestUpdateItemSnakeCaseMask`: `update_mask: ["title"]` with `base_revision: 1` succeeds, and the title changes.
    - `TestStaleWrite`: `base_revision: 0` gives `IsError`, and the text starts with `"aborted: "` and contains `"revision": "1"`.
    - `TestUnknownArgument`: `{"bogus": 1}` on `get_item` gives `IsError`, with text starting `"invalid_argument: "`.
    - `TestNextTaskTwoAgents`:
      1. The user, through the service directly, seeds a ready feature with two tasks.
      2. Two client sessions each call `next_task` with no arguments. Their texts start with `"# Task: "` and name different task IDs.
      3. A third session's text is the no-task message.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/mcp/`
Expected: FAIL, with undefined `NewServer` and `normalizeArgs`.
- [ ] **Step 3: Implement the code.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/mcp/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add go.mod go.sum internal/mcp
git commit -m "Serve KilnService tools over MCP"
```

---

### Task 12: Connect API for the user

**Files:**
- Create: `internal/api/handler.go`
- Test: `internal/api/handler_test.go`

**Interfaces:**
- Produces: `func NewHandler(svc *service.Service) (string, http.Handler)`. It returns the path from `kilnv1connect.NewKilnServiceHandler`, wrapped in `http.NewCrossOriginProtection().Handler`.

**Rules:**
- The `handler` embeds `kilnv1connect.UnimplementedKilnServiceHandler`, so RestoreVersion and WatchItems return `CodeUnimplemented` until steps 6 and 4.
- Every other method calls the service with `service.WithCaller(ctx, Caller{Actor: USER})` and `req.Msg`.
- Errors become `connect.NewError(service.ErrorCode(err), err)`. A `*ConflictError` also gets `connect.NewErrorDetail(&kilnv1.RevisionConflict{Current: e.Current})`.

- [ ] **Step 1: Write the failing tests** against `httptest.NewServer` with `kilnv1connect.NewKilnServiceClient(srv.Client(), srv.URL)`:
  - `TestUserCreatesAndCompletes`:
    1. Create a project, a feature and a task. The feature is not a draft (created by the user).
    2. `SetStatus` DONE on the task succeeds.
  - `TestConflictDetail`: an `UpdateItem` with a stale base gives `connect.CodeOf(err) == CodeAborted`, and its one detail decodes to a `RevisionConflict` whose `current.item.revision` is 1.
  - `TestUnimplemented`: `RestoreVersion` gives `CodeUnimplemented`.
  - `TestCrossOriginRejected`: a raw POST to `/kiln.v1.KilnService/GetItem` with header `Sec-Fetch-Site: cross-site` gets status 403.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/api/`
Expected: FAIL, with `undefined: api.NewHandler`.
- [ ] **Step 3: Implement `handler.go`.**
- [ ] **Step 4: Run the tests to verify they pass.**
Run: `go test -race ./internal/api/`
Expected: PASS.
- [ ] **Step 5: Commit.**
```bash
git add internal/api
git commit -m "Serve KilnService over Connect as the user"
```

---

### Task 13: Daemon and `kiln serve`

**Files:**
- Create: `internal/daemon/daemon.go`
- Modify: `cmd/kiln/cmd/serve.go`, `.gitignore`, `CLAUDE.md`
- Test: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `store.Open`, `service.New`, `mcp.NewServer` and `mcp.NewHandler`, `api.NewHandler`.
- Produces:
```go
const DefaultAddr = "127.0.0.1:7477"
var ErrAlreadyRunning = errors.New("kiln serve is already running for this repo")

type Config struct {
	RepoRoot string
	Addr     string       // "" → DefaultAddr
	Version  string
	Logger   *slog.Logger // nil → slog.Default()
}
// FindRepoRoot walks up from start to the first directory containing .git (dir or file).
func FindRepoRoot(start string) (string, error)
// Run serves until ctx is done, then shuts down (5s grace) and returns nil.
// ready, if non-nil, is called with the bound address once listening.
func Run(ctx context.Context, cfg Config, ready func(addr string)) error
```

**`Run` steps:**
1. Open the store.
2. Create `.kiln/run/`. Write `.kiln/.gitignore` containing `run/\n` only if the file doesn't exist.
3. Take `flock.New(".kiln/run/daemon.lock").TryLock()`. If the lock is held, return `ErrAlreadyRunning`.
4. Call `service.New` with the logger, and log each FileError.
5. Listen on `cfg.Addr`.
6. Write `.kiln/run/daemon.json` as `{"addr":"<bound addr>","pid":<pid>}` via temp file and rename.
7. Serve a mux with `/mcp` going to the MCP handler and the Connect path going to the API handler.
8. On shutdown, remove `daemon.json` and unlock.

**`kiln serve`:**
- Flags: `--addr` (default `daemon.DefaultAddr`) and `--repo` (default `FindRepoRoot(cwd)`).
- Run under `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`.
- Once ready, print `Kiln is serving <repo>` and `  MCP:  http://<addr>/mcp`, on two lines.
- `version` is a package variable in `cmd`, defaulting to `"dev"`.

**Other files:**
- **`.gitignore`:** replace the two `.kiln/...` lines with a comment saying that `.kiln/.gitignore` (written by the daemon) ignores `.kiln/run/`.
- **`CLAUDE.md`:** add a "Running Kiln" section with:
  - `go run ./cmd/kiln serve`
  - connecting Claude Code: `claude mcp add --transport http kiln http://127.0.0.1:7477/mcp`
  - user-only actions until the CLI exists: `go tool buf curl --schema proto --data '{"id":"<id>","base_revision":1,"status":"STATUS_DONE"}' http://127.0.0.1:7477/kiln.v1.KilnService/SetStatus`
  - the note that the daemon doesn't watch files yet, so restart it after a `git pull` or branch switch

  Also update "Planned architecture" to say that the index is in-memory for now.

- [ ] **Step 1: Write the failing tests.**
  - `TestFindRepoRoot`: in a temp dir with `.git/` and a nested `a/b`, `FindRepoRoot("a/b")` returns the temp dir. A dir with no `.git` anywhere up to `/` is an error (use `t.TempDir()` with no `.git`, and expect a non-nil error only if none of its ancestors has one; otherwise skip the test).
  - `TestRun`:
    1. Start `Run` in a goroutine with `Addr "127.0.0.1:0"`; `ready` sends the address on a channel.
    2. Check that:
       - `.kiln/run/daemon.json` has that address
       - `.kiln/.gitignore` is `"run/\n"`
       - an MCP client at `http://<addr>/mcp` lists 15 tools
       - a Connect client's `CreateItem` of a project succeeds
       - a second `Run` on the same repo returns `ErrAlreadyRunning`
    3. Cancel the context. `Run` returns nil and `daemon.json` is gone.
- [ ] **Step 2: Run the tests to verify they fail.**
Run: `go test ./internal/daemon/`
Expected: FAIL, with `undefined: daemon.Run`.
- [ ] **Step 3: Implement `daemon.go`, then update `serve.go`.**
- [ ] **Step 4: Run every check.**
Run: `go mod tidy && make test && make lint && go build -o bin/kiln ./cmd/kiln`
Expected:
- `go mod tidy` leaves `gofrs/flock` and the MCP SDK as direct requirements.
- Every package passes.
- Lint exits 0.
- The binary builds.
- [ ] **Step 5: Smoke-test by hand.**
Run: `./bin/kiln serve --addr 127.0.0.1:0` from the repo root, then stop it with Ctrl-C.
Expected: it prints the MCP URL, exits cleanly on Ctrl-C, and leaves no `.kiln/run/daemon.json` behind.
- [ ] **Step 6: Commit.**
```bash
git add internal/daemon cmd/kiln/cmd/serve.go .gitignore CLAUDE.md go.mod go.sum .kiln/.gitignore
git commit -m "Run the service, MCP and Connect from kiln serve"
```
