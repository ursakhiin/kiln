package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/service"
	"github.com/ursakhiin/kiln/internal/store"
)

// clock is a manual clock for Options.Now, starting at 2026-11-13T10:00:00Z.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock {
	return &clock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// id returns a valid, deterministic ULID for test item n.
func id(n int) string { return fmt.Sprintf("01JD%022d", n) }

// seedTime is the create and update time of seeded items, an hour before the
// clock starts.
var seedTime = timestamppb.New(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC))

func proj(n int) *kilnv1.Item {
	return &kilnv1.Item{
		Id: id(n), Revision: 1, Title: fmt.Sprintf("Project %d", n),
		CreateTime: seedTime, UpdateTime: seedTime,
		Kind: &kilnv1.Item_Project{Project: &kilnv1.Project{}},
	}
}

func feat(n, project int, rank string, deps ...*kilnv1.Dependency) *kilnv1.Item {
	return &kilnv1.Item{
		Id: id(n), Revision: 1, Title: fmt.Sprintf("Feature %d", n),
		CreateTime: seedTime, UpdateTime: seedTime,
		Kind: &kilnv1.Item_Feature{Feature: &kilnv1.Feature{
			ProjectId: id(project), Draft: false, Rank: rank, DependsOn: deps,
		}},
	}
}

func task(n, feature int, rank string, st kilnv1.Status, deps ...*kilnv1.Dependency) *kilnv1.Item {
	return &kilnv1.Item{
		Id: id(n), Revision: 1, Title: fmt.Sprintf("Task %d", n),
		CreateTime: seedTime, UpdateTime: seedTime,
		Kind: &kilnv1.Item_Task{Task: &kilnv1.Task{
			FeatureId: id(feature), Status: st, Rank: rank, DependsOn: deps,
		}},
	}
}

func dep(n int, until kilnv1.BlockUntil) *kilnv1.Dependency {
	return &kilnv1.Dependency{ItemId: id(n), BlockUntil: until}
}

// newService seeds a fresh repo with items and opens a service on it.
func newService(t *testing.T, items ...*kilnv1.Item) (*service.Service, *clock, string) {
	t.Helper()
	repo := t.TempDir()
	st, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if err := st.PutItem(it); err != nil {
			t.Fatal(err)
		}
	}
	clk := newClock()
	svc, fileErrs, err := service.New(st, service.Options{Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if len(fileErrs) > 0 {
		t.Fatalf("unexpected file errors: %v", fileErrs)
	}
	return svc, clk, repo
}

var user = service.WithCaller(context.Background(), service.Caller{Actor: kilnv1.ActorType_ACTOR_TYPE_USER})

func agent(name string) context.Context {
	return service.WithCaller(context.Background(), service.Caller{
		Actor: kilnv1.ActorType_ACTOR_TYPE_AGENT, AgentID: name, Host: "test-host", HostVersion: "1.0",
	})
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %v", code)
	}
	if got := service.ErrorCode(err); got != code {
		t.Fatalf("got code %v (%v), want %v", got, err, code)
	}
}
