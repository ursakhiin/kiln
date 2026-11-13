package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"google.golang.org/protobuf/proto"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
	"github.com/ursakhiin/underdraw/internal/store"
)

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func testItem(id string) *underdrawv1.Item {
	return &underdrawv1.Item{
		Id: id, Revision: 1, Title: "Sync engine",
		Kind: &underdrawv1.Item_Task{Task: &underdrawv1.Task{FeatureId: core.NewID(), Status: underdrawv1.Status_STATUS_READY, Rank: "i"}},
	}
}

func TestItemRoundTrip(t *testing.T) {
	s, root := openStore(t)
	item := testItem(core.NewID())
	if err := s.PutItem(item); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetItem(item.Id)
	if err != nil || !proto.Equal(got, item) {
		t.Fatalf("GetItem = %v, %v; want %v", got, err, item)
	}
	if _, err := os.Stat(filepath.Join(root, ".underdraw", "items", item.Id+".json")); err != nil {
		t.Fatalf("item file: %v", err)
	}
	if err := s.DeleteItem(item.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(item.Id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetItem after delete: got %v, want ErrNotFound", err)
	}
}

func TestInvalidIDs(t *testing.T) {
	s, root := openStore(t)
	if _, err := s.GetItem("../../etc/passwd"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("GetItem: got %v, want ErrInvalidArgument", err)
	}
	if err := s.DeleteItem("x"); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("DeleteItem: got %v, want ErrInvalidArgument", err)
	}
	if err := s.PutItem(&underdrawv1.Item{Id: "../x"}); !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("PutItem: got %v, want ErrInvalidArgument", err)
	}
	// Only the store's own directories exist; nothing was written elsewhere.
	var paths []string
	filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		rel, _ := filepath.Rel(root, p)
		paths = append(paths, rel)
		return nil
	})
	want := []string{".", ".underdraw", ".underdraw/history", ".underdraw/items"}
	sort.Strings(paths)
	if len(paths) != len(want) {
		t.Fatalf("files under root: %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("files under root: %v, want %v", paths, want)
		}
	}
}

func TestListItemsReportsBadFiles(t *testing.T) {
	s, root := openStore(t)
	a, b := testItem(core.NewID()), testItem(core.NewID())
	for _, it := range []*underdrawv1.Item{b, a} {
		if err := s.PutItem(it); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, ".underdraw", "items")
	mismatched := testItem(core.NewID()) // its id doesn't match the filename below
	mismatchedJSON := `{"id": "` + mismatched.Id + `", "title": "x"}`
	for name, content := range map[string]string{
		"not-an-id.json":       "{}",
		core.NewID() + ".json": "{",
		core.NewID() + ".json": mismatchedJSON,
		".tmp-123":             "partial",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	items, fileErrs, err := s.ListItems()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !proto.Equal(items[0], a) || !proto.Equal(items[1], b) {
		t.Errorf("items = %v, want [a, b] in ID order", items)
	}
	if len(fileErrs) != 3 {
		t.Errorf("got %d file errors, want 3: %v", len(fileErrs), fileErrs)
	}
}

func TestGetItemMissing(t *testing.T) {
	s, _ := openStore(t)
	if _, err := s.GetItem(core.NewID()); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
