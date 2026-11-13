package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// GetItemChanges returns the change entries of an item after
// since_revision, oldest first. It also works for deleted items, whose
// history stays on disk.
func (s *Service) GetItemChanges(ctx context.Context, req *underdrawv1.GetItemChangesRequest) (*underdrawv1.GetItemChangesResponse, error) {
	_, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if !core.ValidID(req.GetId()) {
		return nil, fmt.Errorf("invalid ID %q: %w", req.GetId(), core.ErrInvalidArgument)
	}
	entries, err := s.itemHistory(req.GetId())
	if err != nil {
		return nil, err
	}
	_, exists := s.items[req.GetId()]
	if !exists && len(entries) == 0 {
		return nil, fmt.Errorf("item %s: %w", req.GetId(), core.ErrNotFound)
	}
	resp := &underdrawv1.GetItemChangesResponse{}
	for _, e := range entries {
		if ch := e.GetChange(); ch != nil && ch.GetRevision() > req.GetSinceRevision() {
			resp.Changes = append(resp.Changes, e)
		}
	}
	return resp, nil
}

// ListHistory returns an item's history, oldest first, optionally with the
// history of its current children and only decision notes.
func (s *Service) ListHistory(ctx context.Context, req *underdrawv1.ListHistoryRequest) (*underdrawv1.ListHistoryResponse, error) {
	_, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item, err := s.lookup(req.GetId())
	if err != nil {
		return nil, err
	}
	ids := []string{item.GetId()}
	if req.GetIncludeChildren() {
		ids = append(ids, s.childIDs(item)...)
	}
	resp := &underdrawv1.ListHistoryResponse{}
	for _, id := range ids {
		entries, err := s.itemHistory(id)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if req.GetNotesOnly() && e.GetNote() == nil {
				continue
			}
			resp.Entries = append(resp.Entries, e)
		}
	}
	slices.SortFunc(resp.Entries, func(a, b *underdrawv1.HistoryEntry) int {
		return strings.Compare(a.GetId(), b.GetId())
	})
	return resp, nil
}

// AddDecisionNote appends a decision note to an item's history. It doesn't
// change the item.
func (s *Service) AddDecisionNote(ctx context.Context, req *underdrawv1.AddDecisionNoteRequest) (*underdrawv1.AddDecisionNoteResponse, error) {
	caller, unlock, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	item, err := s.lookup(req.GetItemId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetBody()) == "" {
		return nil, fmt.Errorf("decision note body is empty: %w", core.ErrInvalidArgument)
	}
	entry := &underdrawv1.HistoryEntry{
		Id:         core.NewID(),
		ItemId:     item.GetId(),
		CreateTime: timestamppb.New(s.now()),
		Actor:      caller.actor(),
		Kind: &underdrawv1.HistoryEntry_Note{Note: &underdrawv1.DecisionNote{
			Body:         req.GetBody(),
			ItemRevision: item.GetRevision(),
		}},
	}
	if err := s.store.AppendHistory(entry); err != nil {
		return nil, fmt.Errorf("save decision note: %w", err)
	}
	return &underdrawv1.AddDecisionNoteResponse{Entry: entry}, nil
}

// itemHistory reads an item's history from disk, oldest first. Entry files
// that can't be loaded are logged at Warn and skipped.
func (s *Service) itemHistory(id string) ([]*underdrawv1.HistoryEntry, error) {
	entries, fileErrs, err := s.store.ListHistory(id)
	if err != nil {
		return nil, fmt.Errorf("read history of %s: %w", id, err)
	}
	for _, fe := range fileErrs {
		s.log.Warn("skipping history file", "path", fe.Path, "err", fe.Err)
	}
	slices.SortFunc(entries, func(a, b *underdrawv1.HistoryEntry) int {
		return strings.Compare(a.GetId(), b.GetId())
	})
	return entries, nil
}

// childIDs returns the IDs of a feature's tasks, or of a project's features
// and their tasks. Tasks have no children.
func (s *Service) childIDs(item *underdrawv1.Item) []string {
	var ids []string
	switch {
	case item.GetFeature() != nil:
		for _, t := range s.sortedTasks(item.GetId()) {
			ids = append(ids, t.GetId())
		}
	case item.GetProject() != nil:
		for _, f := range s.sortedFeatures() {
			if f.GetFeature().GetProjectId() != item.GetId() {
				continue
			}
			ids = append(ids, f.GetId())
			for _, t := range s.sortedTasks(f.GetId()) {
				ids = append(ids, t.GetId())
			}
		}
	}
	return ids
}
