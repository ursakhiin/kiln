package service

import (
	"context"
	"fmt"
	"log/slog"
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
}

// New loads every item from st. Files that can't be loaded are logged at
// Warn and returned; the items that did load are kept.
func New(st *store.Store, opts Options) (*Service, []*store.FileError, error) {
	s := &Service{
		store:    st,
		items:    map[string]*kilnv1.Item{},
		now:      opts.Now,
		claimTTL: opts.ClaimTTL,
		log:      opts.Logger,
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
	return s, fileErrs, nil
}

// begin starts every RPC method: it locks the service and reads the caller.
// On success the method must call the returned unlock function, typically
// with defer; on error the service is already unlocked.
func (s *Service) begin(ctx context.Context) (Caller, func(), error) {
	s.mu.Lock()
	c, ok := CallerFrom(ctx)
	if !ok {
		s.mu.Unlock()
		return Caller{}, nil, fmt.Errorf("no caller: %w", core.ErrPermissionDenied)
	}
	return c, s.mu.Unlock, nil
}

// commit records a change to item, which must be a copy the service owns
// (not a cached pointer still in use elsewhere): it sets the revision (1 for
// CHANGE_TYPE_CREATED, else incremented) and update_time (and create_time for
// creation), writes the item file, caches the item, then appends one
// ChangeEvent history entry whose snapshot is the written item. fields are
// the changed field paths, empty for creation.
func (s *Service) commit(caller Caller, item *kilnv1.Item, typ kilnv1.ChangeType, fields []string) error {
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
	return s.store.AppendHistory(&kilnv1.HistoryEntry{
		Id:         core.NewID(),
		ItemId:     item.GetId(),
		CreateTime: now,
		Actor:      caller.actor(),
		Kind: &kilnv1.HistoryEntry_Change{Change: &kilnv1.ChangeEvent{
			Type:          typ,
			Revision:      item.GetRevision(),
			ChangedFields: fields,
			Snapshot:      proto.Clone(item).(*kilnv1.Item),
		}},
	})
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
