package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

func (s *Store) itemPath(id string) string {
	return filepath.Join(s.itemsDir, id+".json")
}

// PutItem writes item to .kiln/items/<id>.json, replacing any earlier
// version. It doesn't check revisions; the service does.
func (s *Store) PutItem(item *kilnv1.Item) error {
	if err := checkID(item.GetId()); err != nil {
		return err
	}
	data, err := marshal(item)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.itemPath(item.GetId()), data)
}

// GetItem reads one item. It returns core.ErrNotFound if there is no file for
// id.
func (s *Store) GetItem(id string) (*kilnv1.Item, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	item, err := readItem(s.itemPath(id), id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("item %s: %w", id, core.ErrNotFound)
	}
	return item, err
}

// DeleteItem removes an item's file. Its history is kept.
func (s *Store) DeleteItem(id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	err := os.Remove(s.itemPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("item %s: %w", id, core.ErrNotFound)
	}
	return err
}

// ListItems returns every item, sorted by ID. Files that can't be loaded are
// reported as FileErrors and skipped; the error is only for failures such as
// an unreadable directory.
func (s *Store) ListItems() ([]*kilnv1.Item, []*FileError, error) {
	ids, fileErrs, err := listJSON(s.itemsDir)
	if err != nil {
		return nil, nil, err
	}
	items := make([]*kilnv1.Item, 0, len(ids))
	for _, id := range ids {
		path := s.itemPath(id)
		item, err := readItem(path, id)
		if err != nil {
			fileErrs = append(fileErrs, &FileError{path, err})
			continue
		}
		items = append(items, item)
	}
	return items, fileErrs, nil
}

// readItem reads and parses the item file at path, checking that its id
// matches the file name.
func readItem(path, id string) (*kilnv1.Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	item := &kilnv1.Item{}
	if err := unmarshal(data, item); err != nil {
		return nil, err
	}
	if item.GetId() != id {
		return nil, fmt.Errorf("id %q doesn't match the file name", item.GetId())
	}
	return item, nil
}
