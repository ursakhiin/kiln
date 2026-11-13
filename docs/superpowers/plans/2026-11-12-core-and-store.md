# Core Rules and File Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement step 1 of Kiln's build order: the pure domain rules in `internal/core` and the `.kiln/` file storage in `internal/store`, so the service layer (step 2) can be built on them.

**Architecture:**
- `internal/core` holds pure functions over the generated protobuf types (`gen/go/kiln/v1`): IDs, revision checks, ranking, lifecycle and permission rules, the derived feature status, blocking, cycle detection and secret checks. It does no I/O.
- `internal/store` reads and writes items and history entries as deterministic protojson files, using atomic writes. It reports bad files without failing.
- Neither package increments revisions, picks tasks or checks that parents exist; that is the service layer's job in step 2.

**Tech Stack:**
- Go 1.27 (module floor `go 1.27.1`)
- `google.golang.org/protobuf` (`protojson`, `proto`)
- `github.com/oklog/ulid/v2` (new dependency)
- Standard `testing` package only

**Spec:** `Requirements.md` and `proto/kiln/v1/{item,service,history}.proto`. The proto comments are the authoritative rules.

## Global Constraints

- Import the generated types as `kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"`.
- Never hand-edit `gen/go/` or `web/src/gen/`. `make check-gen`, which runs as part of `make lint`, fails if they differ from freshly generated code (Task 10).
- `internal/core` is pure. Its non-test files must not import `os`, `io`, `syscall`, `net`, or anything under `os/`, `io/`, `path/` or `net/`; `TestCoreDoesNoIO` enforces this (Task 1).
- IDs are canonical uppercase ULIDs: 26 characters of Crockford base32.
- Storage paths:
  - `.kiln/items/<id>.json`
  - `.kiln/history/<item-id>/<entry-id>.json`
- JSON is protojson with proto field names (`UseProtoNames: true`), re-indented with two spaces, and ending in a newline. `int64` values appear as JSON strings (e.g. `"revision": "3"`), which is protojson's standard behavior.
- Error categories are sentinel errors in `core`, compared with `errors.Is`. The service maps them to Connect codes later:
  - `ErrNotFound` → `NOT_FOUND`
  - `ErrInvalidArgument` → `INVALID_ARGUMENT`
  - `ErrPermissionDenied` → `PERMISSION_DENIED`
  - `ErrFailedPrecondition` → `FAILED_PRECONDITION`
  - `ErrRevisionConflict` → `ABORTED`
- Error messages must never include a value rejected as a secret.
- Run everything from the repo root. `make test` runs `go test ./...`.

## Review Focus

1. **Path traversal through IDs.** An ID such as `../../etc/passwd` passed to any store method must fail with `ErrInvalidArgument` before any path is built. Owned by Task 1 (`ValidID`) and Task 8.
2. **Hand-edited or merge-damaged files.** Invalid JSON, a file whose `id` doesn't match its filename, or a non-ULID filename must be reported as a `FileError`, while every other item still loads. Owned by Tasks 8 and 9.
3. **A crash during a write.** A leftover `.tmp-*` file must be ignored by every list call, and the previous version of the file must be untouched. Owned by Tasks 7 and 8.
4. **Repeated drag-and-drop into the same spot.** Ranks must stay valid and strictly ordered after 1,000 inserts at the front and between neighbors. Owned by Task 2.
5. **Dependency deadlocks through parent links.** A feature that depends on one of its own tasks deadlocks, because a task is blocked whenever its feature is. This must be detected as a cycle, and a dependency on a missing item must count as blocking. Owned by Tasks 3 and 5.

---

### Task 1: Errors, IDs and revision checks

**Files:**
- Create: `internal/core/errors.go`, `internal/core/id.go`, `internal/core/revision.go`
- Test: `internal/core/id_test.go`, `internal/core/revision_test.go`, `internal/core/imports_test.go`

**Interfaces:**
- Produces:
  - `var ErrNotFound, ErrInvalidArgument, ErrPermissionDenied, ErrFailedPrecondition, ErrRevisionConflict error`
  - `func NewID() string`
  - `func ValidID(id string) bool`
  - `func CheckRevision(current, base int64) error`

- [ ] **Step 1: Add the ULID dependency**

Run: `go get github.com/oklog/ulid/v2@latest`
Expected: `go.mod` lists `github.com/oklog/ulid/v2`.

- [ ] **Step 2: Write the failing tests**

```go
func TestNewIDIsValidAndIncreasing(t *testing.T) {
	a, b := core.NewID(), core.NewID()
	if !core.ValidID(a) || !core.ValidID(b) {
		t.Fatalf("invalid IDs: %q %q", a, b)
	}
	if a >= b {
		t.Fatalf("IDs not increasing: %q >= %q", a, b)
	}
}

func TestValidID(t *testing.T) {
	if !core.ValidID("01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Error("rejected a canonical ULID")
	}
	for _, id := range []string{
		"", "../../etc/passwd", "01ARZ3NDEKTSV4RRFFQ69G5FA", // too short
		"01ARZ3NDEKTSV4RRFFQ69G5FAV/x", "01arz3ndektsv4rrffq69g5fav", // lowercase
		"81ARZ3NDEKTSV4RRFFQ69G5FAV", // overflows 128 bits
	} {
		if core.ValidID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}

func TestCheckRevision(t *testing.T) {
	if err := core.CheckRevision(3, 3); err != nil {
		t.Errorf("same revision: %v", err)
	}
	if err := core.CheckRevision(4, 3); !errors.Is(err, core.ErrRevisionConflict) {
		t.Errorf("stale revision: got %v", err)
	}
}
```

Put this test in `imports_test.go`, in `package core`. It enforces the "core is pure" constraint on every later task:

```go
func TestCoreDoesNoIO(t *testing.T) {
	files, err := filepath.Glob("*.go") // tests run in the package directory
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue // tests may do I/O
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isIOImport(path) {
				t.Errorf("%s imports %q; internal/core must not do I/O", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files checked")
	}
}

func isIOImport(path string) bool {
	for _, p := range []string{"os", "io", "syscall", "net"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return strings.HasPrefix(path, "path/")
}
```

Also check that the test is actually catching something. Temporarily add `import _ "os"` to `id.go`, run `go test ./internal/core/ -run CoreDoesNoIO`, see it FAIL naming `id.go`, then remove the import.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/core/`
Expected: FAIL (undefined: `core.NewID`).

- [ ] **Step 4: Implement**

- `errors.go`: define the five sentinels with `errors.New` (e.g. `"not found"`). Callers wrap them with `fmt.Errorf("...: %w", ErrX)`.
- `NewID`: `ulid.Make().String()`. It is monotonic within a process.
- `ValidID`: `ulid.ParseStrict` must succeed and `id == strings.ToUpper(id)`.
- `CheckRevision`: when the revisions differ, wrap `ErrRevisionConflict` with both numbers.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/core
git commit -m "Add core errors, ULID IDs and revision check"
```

### Task 2: Ranking

**Files:**
- Create: `internal/core/rank.go`
- Test: `internal/core/rank_test.go`

**Interfaces:**
- Consumes: `ErrInvalidArgument` (Task 1)
- Produces:
  - `func RankBetween(before, after string) (string, error)`. `""` means an open end, so `RankBetween(last, "")` appends.
  - `func ValidRank(r string) bool`

Ranks use the digits `0-9a-z` (base 36, in ASCII order). A rank is never empty and never ends in `0`; that guarantees there is always room to insert before it.

- [ ] **Step 1: Write the failing tests**

```go
func TestRankBetween(t *testing.T) {
	for _, c := range []struct{ before, after, want string }{
		{"", "", "i"},
		{"", "i", "9"},
		{"i", "", "r"},
		{"a", "b", "ai"},
		{"az", "b", "azi"},
		{"a1", "a2", "a1i"},
		{"", "1", "0i"},
	} {
		got, err := core.RankBetween(c.before, c.after)
		if err != nil || got != c.want {
			t.Errorf("RankBetween(%q, %q) = %q, %v; want %q", c.before, c.after, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"b", "a"}, {"a", "a"}, {"a0", ""}, {"", "A"}} {
		if _, err := core.RankBetween(c[0], c[1]); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("RankBetween(%q, %q): got %v, want ErrInvalidArgument", c[0], c[1], err)
		}
	}
}

func TestRankBetweenRepeatedInserts(t *testing.T) {
	ranks := []string{"i"}
	for i := 0; i < 1000; i++ {
		front, err := core.RankBetween("", ranks[0])
		if err != nil {
			t.Fatal(err)
		}
		mid, err := core.RankBetween(front, ranks[0])
		if err != nil {
			t.Fatal(err)
		}
		ranks = append([]string{front, mid}, ranks...)
	}
	for i, r := range ranks {
		if !core.ValidRank(r) {
			t.Fatalf("invalid rank %q", r)
		}
		if i > 0 && ranks[i-1] >= r {
			t.Fatalf("not ordered: %q >= %q", ranks[i-1], r)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/ -run RankBetween`
Expected: FAIL (undefined: `core.RankBetween`).

- [ ] **Step 3: Implement `RankBetween` and `ValidRank`**

`ValidRank`: non-empty, every byte in `0-9a-z`, last byte not `0`.

`RankBetween`:
- Return `ErrInvalidArgument` if a non-empty argument fails `ValidRank`, or if both are non-empty and `before >= after`.
- Otherwise return `midpoint(before, after)`. This is Greenspan's fractional-indexing midpoint, where an `after` of `""` means +infinity:

```go
const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

func midpoint(a, b string) string {
	if b != "" {
		n := 0
		for n < len(b) && digitAt(a, n) == b[n] { // digitAt returns '0' past the end
			n++
		}
		if n > 0 {
			return b[:n] + midpoint(suffix(a, n), b[n:])
		}
	}
	da := 0
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	db := len(digits)
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		return string(digits[(da+db+1)/2])
	}
	if len(b) > 1 {
		return b[:1]
	}
	return string(digits[da]) + midpoint(suffix(a, 1), "")
}
```

`suffix(a, n)` returns `a[n:]`, or `""` when `n >= len(a)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/ -run RankBetween`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/rank.go internal/core/rank_test.go
git commit -m "Add LexoRank-style rank generation"
```

### Task 3: Lifecycle, derived feature status and blocking

**Files:**
- Create: `internal/core/lifecycle.go`
- Test: `internal/core/lifecycle_test.go`

**Interfaces:**
- Consumes: the error sentinels (Task 1)
- Produces:
  - `func CheckStatusChange(actor kilnv1.ActorType, item *kilnv1.Item, to kilnv1.Status) error`
  - `func DeriveFeatureStatus(f *kilnv1.Feature, tasks []kilnv1.Status) kilnv1.Status`
  - `func IsBlocked(deps []*kilnv1.Dependency, statusOf func(id string) (kilnv1.Status, bool)) bool`

These are the rules, taken from the proto comments:

**`CheckStatusChange`:**
- Projects have no status: `ErrFailedPrecondition`.
- Only `ACTOR_TYPE_USER` and `ACTOR_TYPE_AGENT` may change status; anything else gets `ErrPermissionDenied`.
- **Tasks:**
  - `to` must not be `STATUS_UNSPECIFIED` (`ErrInvalidArgument`).
  - The user may set any status.
  - An agent may set only `READY`, `IN_PROGRESS` or `IN_REVIEW`, and only on a task that isn't `DONE`; anything else is `ErrPermissionDenied`.
- **Features** (this sets `status_override`, and `UNSPECIFIED` clears it): user only. An agent gets `ErrPermissionDenied`.

**`DeriveFeatureStatus`**, checked in this order:
1. `status_override`, if set
2. `DRAFT`, if `draft` is true
3. `READY`, if there are no tasks
4. `DONE`, if every task is `DONE`
5. `IN_REVIEW`, if every task is `IN_REVIEW` or `DONE`
6. `IN_PROGRESS`, if any task is `IN_PROGRESS`, `IN_REVIEW` or `DONE`
7. `READY` otherwise

**`IsBlocked`:**
- Returns true if any dependency is unsatisfied.
- A dependency with `BLOCK_UNTIL_IN_REVIEW` is satisfied once the item is `IN_REVIEW` or `DONE`.
- `BLOCK_UNTIL_DONE` and `BLOCK_UNTIL_UNSPECIFIED` are satisfied only by `DONE`.
- An ID that `statusOf` can't find counts as unsatisfied.
- Combining this with a task's parent feature (a task is blocked if its feature is draft or blocked) is the service's job in step 2.

- [ ] **Step 1: Write the failing tests**

Write a table test `TestCheckStatusChange` with at least these rows. Each row is (actor, item, to) → want error, using small helpers `task(status)`, `feature()` and `project()` that build `*kilnv1.Item`:

```go
{user, task(READY), DONE, nil},
{user, task(DONE), READY, nil},
{agent, task(READY), IN_PROGRESS, nil},
{agent, task(IN_PROGRESS), IN_REVIEW, nil},
{agent, task(IN_REVIEW), DONE, core.ErrPermissionDenied},
{agent, task(DONE), IN_PROGRESS, core.ErrPermissionDenied},
{agent, task(READY), DRAFT, core.ErrPermissionDenied},
{user, task(READY), UNSPECIFIED, core.ErrInvalidArgument},
{user, feature(), DONE, nil},
{user, feature(), UNSPECIFIED, nil},
{agent, feature(), IN_REVIEW, core.ErrPermissionDenied},
{user, project(), READY, core.ErrFailedPrecondition},
{external, task(READY), IN_PROGRESS, core.ErrPermissionDenied},
```

Write `TestDeriveFeatureStatus` with these rows: (feature, task statuses) → want.

```go
{&kilnv1.Feature{StatusOverride: DONE}, []kilnv1.Status{READY}, DONE},
{&kilnv1.Feature{Draft: true}, []kilnv1.Status{IN_PROGRESS}, DRAFT},
{&kilnv1.Feature{}, nil, READY},
{&kilnv1.Feature{}, []kilnv1.Status{READY, DRAFT}, READY},
{&kilnv1.Feature{}, []kilnv1.Status{READY, IN_PROGRESS}, IN_PROGRESS},
{&kilnv1.Feature{}, []kilnv1.Status{READY, DONE}, IN_PROGRESS},
{&kilnv1.Feature{}, []kilnv1.Status{IN_REVIEW, DONE}, IN_REVIEW},
{&kilnv1.Feature{}, []kilnv1.Status{DONE, DONE}, DONE},
```

Write `TestIsBlocked`. Use `statusOf` backed by `map[string]kilnv1.Status{"A": IN_REVIEW, "B": DONE}`:
- `[]` → false
- `[{A, IN_REVIEW}]` → false
- `[{A, DONE}]` → true
- `[{A, UNSPECIFIED}]` → true
- `[{B, DONE}]` → false
- `[{MISSING, IN_REVIEW}]` → true

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/ -run 'StatusChange|DeriveFeature|IsBlocked'`
Expected: FAIL (undefined functions).

- [ ] **Step 3: Implement the three functions in `lifecycle.go`, following the rules above**

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle.go internal/core/lifecycle_test.go
git commit -m "Add lifecycle, feature status rollup and blocking rules"
```

### Task 4: New-item and update validation

**Files:**
- Create: `internal/core/validate.go`
- Test: `internal/core/validate_test.go`

**Interfaces:**
- Consumes: the error sentinels (Task 1)
- Produces:
  - `func NormalizeNewItem(actor kilnv1.ActorType, item *kilnv1.Item) error`. It modifies `item` in place.
  - `func CheckUpdate(actor kilnv1.ActorType, current, updated *kilnv1.Item, paths []string) error`

**`NormalizeNewItem`** fails with `ErrInvalidArgument` unless all of these hold:
- `id`, `revision`, `create_time` and `update_time` are unset.
- The title isn't empty after `strings.TrimSpace`.
- Exactly one kind is set.
- A feature has a non-empty `project_id`, an empty `rank`, empty `depends_on` and an unset `status_override`.
- A task has a non-empty `feature_id`, an empty `rank`, empty `depends_on`, a nil `work_state`, and a status of `UNSPECIFIED`, `DRAFT` or `READY`.

If those hold, it then:
- sets `draft = true` on features created by an agent
- sets a task status of `UNSPECIFIED` to `READY`

It doesn't check that the parent exists.

**`CheckUpdate`:**
- `paths` must be non-empty, and each path must be allowed for the item's kind:
  - any kind: `title`, `description`
  - features: `feature.project_id`, `feature.draft`
  - tasks: `task.feature_id`

  Anything else is `ErrInvalidArgument`. That includes status, rank, dependencies, work state, `id` and `revision`, which have their own RPCs.
- If `title` is in `paths`, the updated title must be non-empty after trimming.
- An agent changing `feature.draft` from true to false gets `ErrPermissionDenied`.

- [ ] **Step 1: Write the failing tests**

Write `TestNormalizeNewItem` as a table. Cases:
- an agent-created feature comes out with `Draft == true`
- a user-created feature keeps `Draft == false`
- a task with no status comes out `READY`
- each invalid case returns `ErrInvalidArgument`: `id` set; title `"  "`; no kind; a feature without `project_id`; a task with status `IN_PROGRESS`; a task with `depends_on` set

Write `TestCheckUpdate` as a table. Cases:
- `{"title"}` with a new title → nil
- `{"task.status"}` → `ErrInvalidArgument`
- `{"feature.draft"}` on a task → `ErrInvalidArgument`
- `{}` → `ErrInvalidArgument`
- `{"title"}` with title `""` → `ErrInvalidArgument`
- an agent with `{"feature.draft"}` going true→false → `ErrPermissionDenied`
- the user doing the same → nil
- an agent with `{"feature.draft"}` going false→true → nil

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/ -run 'NormalizeNewItem|CheckUpdate'`
Expected: FAIL (undefined functions).

- [ ] **Step 3: Implement both functions in `validate.go`, following the rules above**

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/validate.go internal/core/validate_test.go
git commit -m "Add new-item and update validation rules"
```

### Task 5: Dependency cycle detection

**Files:**
- Create: `internal/core/graph.go`
- Test: `internal/core/graph_test.go`

**Interfaces:**
- Produces:
  - `func Edges(item *kilnv1.Item) []string`: the item's `depends_on` IDs, plus `feature_id` for a task, because a task is blocked whenever its feature is.
  - `func WouldCreateCycle(itemID, dependsOnID string, edges func(id string) []string) bool`: true if adding `itemID → dependsOnID` creates a cycle. That's the case when `itemID == dependsOnID`, or when `itemID` can be reached from `dependsOnID` by following `edges`.

- [ ] **Step 1: Write the failing tests**

```go
func TestWouldCreateCycle(t *testing.T) {
	// F1 is a feature; T1 is a task in F1; T2 depends on T1; F2 depends on T2.
	items := map[string]*kilnv1.Item{
		"F1": {Id: "F1", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{}}},
		"T1": {Id: "T1", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F1"}}},
		"T2": {Id: "T2", Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "F1",
			DependsOn: []*kilnv1.Dependency{{ItemId: "T1"}}}}},
		"F2": {Id: "F2", Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			DependsOn: []*kilnv1.Dependency{{ItemId: "T2"}}}}},
	}
	edges := func(id string) []string {
		if it, ok := items[id]; ok {
			return core.Edges(it)
		}
		return nil
	}
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"T1", "T1", true},  // self
		{"T1", "T2", true},  // direct back-edge
		{"F1", "T1", true},  // feature depends on its own task (via parent edge)
		{"T1", "F2", true},  // transitive: F2 → T2 → T1
		{"F2", "T1", false}, // already implied, no cycle
		{"T1", "MISSING", false},
	} {
		if got := core.WouldCreateCycle(c.from, c.to, edges); got != c.want {
			t.Errorf("WouldCreateCycle(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/ -run WouldCreateCycle`
Expected: FAIL (undefined: `core.Edges`).

- [ ] **Step 3: Implement `Edges` and `WouldCreateCycle`**

Use an iterative depth-first search from `dependsOnID` with a visited set, so cycles that already exist in hand-edited data can't make it loop forever.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/graph.go internal/core/graph_test.go
git commit -m "Add dependency cycle detection including parent edges"
```

### Task 6: Work-state secret checks

**Files:**
- Create: `internal/core/secrets.go`
- Test: `internal/core/secrets_test.go`

**Interfaces:**
- Produces: `func CheckWorkState(ws *kilnv1.WorkState) error`. It returns `ErrInvalidArgument`, naming the field (e.g. `extra["deploy"]`), never the value.

**What it checks:**
- Every string field is scanned, including `files`, `session.*`, and each `extra` key and value. The scan uses these patterns:
  - `AKIA[0-9A-Z]{16}`
  - `gh[pousr]_[A-Za-z0-9]{36,}`
  - `github_pat_[A-Za-z0-9_]{22,}`
  - `xox[abprs]-[A-Za-z0-9-]{10,}`
  - `sk-ant-[A-Za-z0-9_-]{20,}`
  - `sk-[A-Za-z0-9]{20,}`
  - `-----BEGIN [A-Z ]*PRIVATE KEY-----`
  - `eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}` (JWTs)
- Each `credential_refs` entry must match `^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`, so it can only be a name.
- A nil `ws` returns nil.

- [ ] **Step 1: Write the failing tests**

`TestCheckWorkState`:
- nil → nil
- `{Branch: "feat/x", CredentialRefs: ["GITHUB_TOKEN"], Extra: {"pr": "42"}}` → nil

`TestCheckWorkStateRejectsSecrets` is a table of `{name string; ws *kilnv1.WorkState; secret string}`. For every row, assert both of these:
- `errors.Is(err, core.ErrInvalidArgument)`
- `!strings.Contains(err.Error(), row.secret)`, so the secret never appears in the error

It needs one row per pattern, each in a different field, plus the credential-name rules:

| Field | Secret |
|---|---|
| `Progress` | `"AKIA" + strings.Repeat("A", 16)` |
| `Branch` | `"ghp_" + strings.Repeat("a", 36)` |
| `PullRequestUrl` | `"github_pat_" + strings.Repeat("a", 22)` |
| `Files[0]` | `"xoxb-" + strings.Repeat("1", 10)` |
| `Extra` value | `"sk-ant-" + strings.Repeat("a", 20)` |
| `Extra` key | `"sk-" + strings.Repeat("a", 20)` |
| `Session.TranscriptPath` | `"-----BEGIN RSA PRIVATE KEY-----"` |
| `Session.SessionId` | `"eyJ" + strings.Repeat("a", 10) + "." + strings.Repeat("b", 10) + "." + strings.Repeat("c", 10)` |
| `CredentialRefs[0]` | `"ghp_" + strings.Repeat("a", 36)` |
| `CredentialRefs[0]` | `"my token"` |

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core/ -run CheckWorkState`
Expected: FAIL (undefined: `core.CheckWorkState`).

- [ ] **Step 3: Implement `CheckWorkState`, with the patterns compiled once as package-level `regexp.MustCompile` values**

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/secrets.go internal/core/secrets_test.go
git commit -m "Reject credential values in task work state"
```

### Task 7: Deterministic encoding and atomic writes

**Files:**
- Create: `internal/store/codec.go`, `internal/store/fileio.go`
- Test: `internal/store/codec_test.go`, `internal/store/fileio_test.go`

**Interfaces:**
- Produces (unexported, used by Tasks 8 and 9):
  - `func marshal(m proto.Message) ([]byte, error)`
  - `func unmarshal(b []byte, m proto.Message) error`
  - `func writeFileAtomic(path string, data []byte) error`: replaces any existing file
  - `func writeFileExclusive(path string, data []byte) error`: fails with an error matching `fs.ErrExist` if the file exists

- [ ] **Step 1: Write the failing tests**

```go
func TestMarshalIsDeterministic(t *testing.T) {
	item := &kilnv1.Item{
		Id: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Revision: 3, Title: "Sync engine",
		Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{
			FeatureId: "01ARZ3NDEKTSV4RRFFQ69G5FAW", Status: kilnv1.Status_STATUS_READY, Rank: "i",
			WorkState: &kilnv1.WorkState{Extra: map[string]string{"b": "2", "a": "1"}},
		}},
	}
	want := `{
  "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "revision": "3",
  "title": "Sync engine",
  "task": {
    "feature_id": "01ARZ3NDEKTSV4RRFFQ69G5FAW",
    "status": "STATUS_READY",
    "rank": "i",
    "work_state": {
      "extra": {
        "a": "1",
        "b": "2"
      }
    }
  }
}
`
	// protojson inserts extra spaces depending on a hash of the binary, so
	// its raw output differs between builds. Comparing to an exact expected
	// file catches that in any build where the spaces appear.
	got, err := marshal(item)
	if err != nil || string(got) != want {
		t.Fatalf("marshal:\n%s\nerr=%v", got, err)
	}
	var back kilnv1.Item
	if err := unmarshal([]byte(want), &back); err != nil || !proto.Equal(&back, item) {
		t.Fatalf("round trip failed: %v", err)
	}
}
```

`TestWriteFileAtomic`:
- Write `"a"`, then `"b"`, to `dir/x.json`. The file reads `"b"`, and `os.ReadDir(dir)` lists only `x.json`, with no leftover temp files.

`TestWriteFileExclusive`:
- The first write succeeds.
- The second write to the same path returns an error where `errors.Is(err, fs.ErrExist)`, and the content is still the first write's.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL (undefined: `marshal`).

- [ ] **Step 3: Implement**

- **`marshal`:**
  1. `protojson.MarshalOptions{UseProtoNames: true}.Marshal`
  2. `json.Compact`, which strips the extra spaces protojson adds on purpose. These vary between builds of the binary, per the protojson docs ("Do not depend on the output being stable"), so without this step every rebuild or upgrade of Kiln could rewrite `.kiln/` files with whitespace-only changes.
  3. `json.Indent` with `""` and `"  "`
  4. append `"\n"`
- **`unmarshal`:** `protojson.Unmarshal`.
- **`writeFileAtomic`:**
  1. `os.CreateTemp(filepath.Dir(path), ".tmp-*")`
  2. write, `Sync`, `Close`
  3. `os.Rename` onto `path`
  4. remove the temp file on any error
- **`writeFileExclusive`:** the same temp-file steps, but finish with `os.Link(tmp, path)` (which fails if `path` exists) and then remove the temp file.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add deterministic protojson encoding and atomic file writes"
```

### Task 8: Item store

**Files:**
- Create: `internal/store/store.go`, `internal/store/items.go`
- Test: `internal/store/items_test.go`

**Interfaces:**
- Consumes:
  - `core.ValidID`, `core.ErrNotFound`, `core.ErrInvalidArgument` (Task 1)
  - `marshal`, `unmarshal`, `writeFileAtomic` (Task 7)
- Produces:
  - `type Store struct` (unexported fields)
  - `func Open(repoRoot string) (*Store, error)`: creates `.kiln/items/` and `.kiln/history/` if they're missing
  - `type FileError struct { Path string; Err error }`, which implements `error` as `Path + ": " + Err.Error()`
  - `func (s *Store) PutItem(item *kilnv1.Item) error`
  - `func (s *Store) GetItem(id string) (*kilnv1.Item, error)`
  - `func (s *Store) DeleteItem(id string) error`
  - `func (s *Store) ListItems() ([]*kilnv1.Item, []*FileError, error)`: sorted by ID. The last error is only for failures such as the directory being unreadable.

**Rules:**
- Every method that takes an ID returns `ErrInvalidArgument` if `!core.ValidID(id)` (or the item's `Id`), before it builds a path.
- `GetItem` and `DeleteItem` return `ErrNotFound` for a missing file.
- `ListItems` skips names that start with `.` or don't end in `.json`.
- It reports a `FileError` when a filename's stem is not a valid ID, when the JSON doesn't parse, or when the parsed `id` doesn't match the stem.
- `PutItem` doesn't check revisions; the service does.

- [ ] **Step 1: Write the failing tests**

`TestItemRoundTrip`:
- `Open(t.TempDir())`, then `PutItem` an item with ID `core.NewID()`.
- `GetItem` returns a `proto.Equal` item.
- The file exists at `<root>/.kiln/items/<id>.json`.
- After `DeleteItem`, `GetItem` returns `ErrNotFound`.

`TestInvalidIDs`:
- `GetItem("../../etc/passwd")`, `DeleteItem("x")` and `PutItem(&kilnv1.Item{Id: "../x"})` each return `ErrInvalidArgument`.
- No file is created outside `.kiln/items/`.

`TestListItemsReportsBadFiles`:
- Put two valid items.
- Write these straight into `.kiln/items/`:
  - `not-an-id.json`
  - `<validID>.json` containing `{`
  - `<validID2>.json` containing a valid item whose `id` is a different ULID
  - `.tmp-123` containing anything
- `ListItems` returns exactly the 2 valid items, in ID order, plus 3 `FileError`s, and a nil error.

`TestGetItemMissing`:
- `GetItem(core.NewID())` returns `ErrNotFound`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run 'Item|InvalidIDs'`
Expected: FAIL (undefined: `Open`).

- [ ] **Step 3: Implement `store.go` (`Open`, the path helpers, `FileError`) and `items.go`, following the rules above**

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add file-backed item store"
```

### Task 9: History store

**Files:**
- Create: `internal/store/history.go`
- Test: `internal/store/history_test.go`

**Interfaces:**
- Consumes: `Store`, `FileError` (Task 8); `writeFileExclusive`, `marshal`, `unmarshal` (Task 7); `core.ValidID` (Task 1)
- Produces:
  - `func (s *Store) AppendHistory(entry *kilnv1.HistoryEntry) error`: writes `.kiln/history/<item_id>/<id>.json`, creating the item's directory if needed. It fails if the entry already exists, because history is append-only.
  - `func (s *Store) ListHistory(itemID string) ([]*kilnv1.HistoryEntry, []*FileError, error)`: sorted by entry ID. An item with no history returns an empty slice and nil errors.

**Rules:**
- Both `entry.Id` and `entry.ItemId` must pass `core.ValidID`, otherwise `ErrInvalidArgument`.
- `ListHistory` applies the same skip and `FileError` rules as `ListItems`, matching each file's stem against the entry's `id`.
- It also reports a `FileError` for an entry whose `item_id` doesn't match its directory.

- [ ] **Step 1: Write the failing tests**

`TestHistoryAppendAndList`:
- Append three entries for one item: a change, a note and a change.
- Their IDs come from `core.NewID()`, but append them out of order.
- `ListHistory` returns them in ID order, each `proto.Equal` to the original.

`TestHistoryIsAppendOnly`:
- Appending the same entry twice returns an error where `errors.Is(err, fs.ErrExist)`.

`TestHistoryInvalidIDs`:
- An entry with `ItemId: "../x"` returns `ErrInvalidArgument`.
- So does `ListHistory("..")`.

`TestListHistoryEmpty`:
- `ListHistory(core.NewID())` returns a length-0 slice, no `FileError`s and a nil error.

`TestListHistoryReportsBadFiles`:
- An entry file in item A's directory whose `item_id` is item B is reported as a `FileError`, and the other entries still load.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run History`
Expected: FAIL (undefined: `AppendHistory`).

- [ ] **Step 3: Implement `history.go`, following the rules above**

- [ ] **Step 4: Run the full checks**

Run: `make test && make lint`
Expected: all packages PASS, and lint exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add append-only history store"
```

### Task 10: Check generated code in lint

**Files:**
- Modify: `Makefile` (add a `check-gen` target and make `lint` depend on it)
- Delete: `gen/go/.gitkeep`. `gen/go/` now holds generated code, and the placeholder would show up as a difference.

**Interfaces:**
- Produces: `make check-gen`, which fails if `gen/go/` or `web/src/gen/` differ from freshly generated code. `make lint` runs it.

This compares against the working tree, not the last commit. A correct `make gen` that you haven't committed yet still passes, while a hand edit or a stale `gen/` after a `.proto` change fails.

- [ ] **Step 1: Delete the placeholder and show the check is missing**

Run: `git rm gen/go/.gitkeep && make check-gen`
Expected: FAIL with `No rule to make target 'check-gen'`.

- [ ] **Step 2: Add the target to the `Makefile`, and add `check-gen` to `.PHONY`**

```make
# Fail if generated code differs from what buf would generate now.
check-gen: web/node_modules
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
	go tool buf generate -o "$$tmp" && \
	diff -r "$$tmp/gen/go" gen/go && \
	diff -r "$$tmp/web/src/gen" web/src/gen
```

Change the `lint` target's first line to `lint: check-gen`.

- [ ] **Step 3: Run it on clean generated code**

Run: `make check-gen`
Expected: exits 0 with no output.

- [ ] **Step 4: Show it catches a hand edit**

Run: `echo '// edited' >> gen/go/kiln/v1/item.pb.go && make check-gen; echo "exit=$?"`
Expected: a `diff` showing `// edited`, and `exit=2`.

Then run: `git checkout gen/go/kiln/v1/item.pb.go && make check-gen`
Expected: exits 0.

- [ ] **Step 5: Run the full checks**

Run: `make lint && make test`
Expected: both exit 0.

- [ ] **Step 6: Commit**

```bash
git add Makefile   # the .gitkeep deletion is already staged by git rm in Step 1
git commit -m "Fail lint when generated code is stale or hand-edited"
```
