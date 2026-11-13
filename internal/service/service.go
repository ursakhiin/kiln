package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
	"github.com/ursakhiin/kiln/internal/store"
)

// DefaultClaimTTL is how long a claim lasts after its agent's last call.
const DefaultClaimTTL = 30 * time.Minute

// Options configures a Service.
type Options struct {
	Now      func() time.Time // nil → time.Now
	ClaimTTL time.Duration    // 0 → DefaultClaimTTL
	Logger   *slog.Logger     // nil → discard
}

// Service is Kiln's single write path. It keeps every item in memory, and
// every method holds mu for its whole body. It hands out clones, never its
// cached items.
type Service struct {
	mu       sync.Mutex
	store    *store.Store
	items    map[string]*kilnv1.Item
	now      func() time.Time
	claimTTL time.Duration
	log      *slog.Logger

	claims   map[string]*claim            // by task ID
	affinity map[string]string            // agent ID → feature of the task it most recently claimed
	pending  map[string]*pendingWorkState // by task ID: work state not yet in history
}

// New loads every item from st. Files that can't be loaded are logged at
// Warn and returned; the items that did load are kept. Work state that the
// previous daemon saved but didn't record in history is recorded now.
func New(st *store.Store, opts Options) (*Service, []*store.FileError, error) {
	s := &Service{
		store:    st,
		items:    map[string]*kilnv1.Item{},
		now:      opts.Now,
		claimTTL: opts.ClaimTTL,
		log:      opts.Logger,
		claims:   map[string]*claim{},
		affinity: map[string]string{},
		pending:  map[string]*pendingWorkState{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.claimTTL == 0 {
		s.claimTTL = DefaultClaimTTL
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	items, fileErrs, err := st.ListItems()
	if err != nil {
		return nil, nil, err
	}
	for _, fe := range fileErrs {
		s.log.Warn("skipping item file", "path", fe.Path, "err", fe.Err)
	}
	for _, it := range items {
		s.items[it.GetId()] = it
	}
	s.reconcileWorkState()
	return s, fileErrs, nil
}

// begin starts every RPC method: it locks the service, reads the caller,
// ends expired claims and, if the caller is an agent, renews its claims. On
// success the method must call the returned unlock function, typically
// with defer; on error the service is already unlocked.
func (s *Service) begin(ctx context.Context) (Caller, func(), error) {
	s.mu.Lock()
	c, ok := CallerFrom(ctx)
	if !ok {
		s.mu.Unlock()
		return Caller{}, nil, fmt.Errorf("no caller: %w", core.ErrPermissionDenied)
	}
	now := s.now()
	s.expireClaims(now)
	if c.Actor == kilnv1.ActorType_ACTOR_TYPE_AGENT {
		s.renewClaims(c.AgentID, now)
	}
	return c, s.mu.Unlock, nil
}

// commit records a change to item, which must be a copy the service owns
// (not a cached pointer still in use elsewhere): it sets the revision (1 for
// CHANGE_TYPE_CREATED, else incremented) and update_time (and create_time for
// creation), writes the item file, caches the item, then appends one
// ChangeEvent history entry whose snapshot is the written item. fields are
// the changed field paths, empty for creation; a task's pending work-state
// fields are appended to them, sorted, and stop being pending, but only if
// caller is the actor they're attributed to. Another actor's pending work
// state is first recorded in its own WORK_STATE_UPDATED entry, at the item's
// revision before this change, so the caller isn't credited with it.
//
// If writing the item file fails, commit returns the error and the cache is
// unchanged. If appending the history entry fails, the item file is already
// saved and is the source of truth, so commit logs the failure at Error and
// returns nil: reporting an error would tell the caller the change was lost.
func (s *Service) commit(caller Caller, item *kilnv1.Item, typ kilnv1.ChangeType, fields []string) error {
	if p := s.pending[item.GetId()]; p != nil && (p.actor != caller.Actor || p.agentID != caller.AgentID) {
		s.recordPendingWorkState(item.GetId())
	}
	now := timestamppb.New(s.now())
	if typ == kilnv1.ChangeType_CHANGE_TYPE_CREATED {
		item.Revision = 1
		item.CreateTime = now
	} else {
		item.Revision++
	}
	item.UpdateTime = now
	if err := s.store.PutItem(item); err != nil {
		return err
	}
	s.items[item.GetId()] = item
	if ws := s.takePending(item.GetId()); len(ws) > 0 {
		fields = append(slices.Clip(fields), ws...)
	}
	s.appendHistory(caller.actor(), item.GetId(), now, &kilnv1.ChangeEvent{
		Type:          typ,
		Revision:      item.GetRevision(),
		ChangedFields: fields,
		Snapshot:      proto.Clone(item).(*kilnv1.Item),
	})
	return nil
}

// appendHistory appends one history entry. A failure is logged at Error, not
// returned: callers use it after the change itself is saved.
func (s *Service) appendHistory(actor *kilnv1.Actor, itemID string, at *timestamppb.Timestamp, ev *kilnv1.ChangeEvent) {
	err := s.store.AppendHistory(&kilnv1.HistoryEntry{
		Id:         core.NewID(),
		ItemId:     itemID,
		CreateTime: at,
		Actor:      actor,
		Kind:       &kilnv1.HistoryEntry_Change{Change: ev},
	})
	if err != nil {
		s.log.Error("item saved but history entry not written",
			"item", itemID, "revision", ev.GetRevision(), "err", err)
	}
}

// checkBase returns a *ConflictError if a write based on revision base would
// overwrite a newer revision of item.
func (s *Service) checkBase(item *kilnv1.Item, base int64) error {
	if core.CheckRevision(item.GetRevision(), base) != nil {
		return &ConflictError{Current: s.info(item)}
	}
	return nil
}

// lookup returns the cached item with id, checking the ID first.
func (s *Service) lookup(id string) (*kilnv1.Item, error) {
	if !core.ValidID(id) {
		return nil, fmt.Errorf("invalid ID %q: %w", id, core.ErrInvalidArgument)
	}
	item, ok := s.items[id]
	if !ok {
		return nil, fmt.Errorf("item %s: %w", id, core.ErrNotFound)
	}
	return item, nil
}
