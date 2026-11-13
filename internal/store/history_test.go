package store_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

func changeEntry(id, itemID string, rev int64) *underdrawv1.HistoryEntry {
	return &underdrawv1.HistoryEntry{
		Id: id, ItemId: itemID,
		Actor: &underdrawv1.Actor{Type: underdrawv1.ActorType_ACTOR_TYPE_USER},
		Kind: &underdrawv1.HistoryEntry_Change{Change: &underdrawv1.ChangeEvent{
			Type: underdrawv1.ChangeType_CHANGE_TYPE_UPDATED, Revision: rev, ChangedFields: []string{"title"},
			Snapshot: &underdrawv1.Item{Id: itemID, Revision: rev, Title: "Sync engine"},
		}},
	}
}

func noteEntry(id, itemID string) *underdrawv1.HistoryEntry {
	return &underdrawv1.HistoryEntry{
		Id: id, ItemId: itemID,
		Actor: &underdrawv1.Actor{Type: underdrawv1.ActorType_ACTOR_TYPE_AGENT, AgentId: "session-1"},
		Kind:  &underdrawv1.HistoryEntry_Note{Note: &underdrawv1.DecisionNote{Body: "Chose X because Y", ItemRevision: 2}},
	}
}

func TestHistoryAppendAndList(t *testing.T) {
	s, _ := openStore(t)
	itemID := core.NewID()
	first, second, third := changeEntry(core.NewID(), itemID, 1), noteEntry(core.NewID(), itemID), changeEntry(core.NewID(), itemID, 2)
	for _, e := range []*underdrawv1.HistoryEntry{third, first, second} {
		if err := s.AppendHistory(e); err != nil {
			t.Fatal(err)
		}
	}
	entries, fileErrs, err := s.ListHistory(itemID)
	if err != nil || len(fileErrs) != 0 {
		t.Fatalf("ListHistory: err=%v fileErrs=%v", err, fileErrs)
	}
	want := []*underdrawv1.HistoryEntry{first, second, third}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i := range want {
		if !proto.Equal(entries[i], want[i]) {
			t.Errorf("entry %d = %v, want %v", i, entries[i], want[i])
		}
	}
}

func TestHistoryIsAppendOnly(t *testing.T) {
	s, _ := openStore(t)
	e := changeEntry(core.NewID(), core.NewID(), 1)
	if err := s.AppendHistory(e); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendHistory(e); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second append: got %v, want fs.ErrExist", err)
	}
}

func TestHistoryInvalidIDs(t *testing.T) {
	s, _ := openStore(t)
	if err := s.AppendHistory(changeEntry(core.NewID(), "../x", 1)); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("AppendHistory with bad item_id: got %v, want ErrInvalidArgument", err)
	}
	if err := s.AppendHistory(changeEntry("../x", core.NewID(), 1)); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("AppendHistory with bad id: got %v, want ErrInvalidArgument", err)
	}
	if _, _, err := s.ListHistory(".."); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("ListHistory(..): got %v, want ErrInvalidArgument", err)
	}
}

func TestListHistoryEmpty(t *testing.T) {
	s, _ := openStore(t)
	entries, fileErrs, err := s.ListHistory(core.NewID())
	if err != nil || len(fileErrs) != 0 || len(entries) != 0 {
		t.Fatalf("got %v, %v, %v; want empty", entries, fileErrs, err)
	}
}

func TestListHistoryReportsBadFiles(t *testing.T) {
	s, root := openStore(t)
	itemA, itemB := core.NewID(), core.NewID()
	good := changeEntry(core.NewID(), itemA, 1)
	if err := s.AppendHistory(good); err != nil {
		t.Fatal(err)
	}
	// An entry for item B, placed in item A's directory.
	misplaced := changeEntry(core.NewID(), itemB, 1)
	if err := s.AppendHistory(misplaced); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".underdraw", "history", itemB, misplaced.Id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".underdraw", "history", itemA, misplaced.Id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	entries, fileErrs, err := s.ListHistory(itemA)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !proto.Equal(entries[0], good) {
		t.Errorf("entries = %v, want only the good entry", entries)
	}
	if len(fileErrs) != 1 {
		t.Errorf("got %d file errors, want 1: %v", len(fileErrs), fileErrs)
	}
}
