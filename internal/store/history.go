package store

import (
	"fmt"
	"os"
	"path/filepath"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

// AppendHistory writes entry to .kiln/history/<item_id>/<id>.json. History is
// append-only: it fails with an error matching fs.ErrExist if the entry
// already exists.
func (s *Store) AppendHistory(entry *kilnv1.HistoryEntry) error {
	if err := checkID(entry.GetId()); err != nil {
		return err
	}
	if err := checkID(entry.GetItemId()); err != nil {
		return err
	}
	dir := filepath.Join(s.historyDir, entry.GetItemId())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := marshal(entry)
	if err != nil {
		return err
	}
	return writeFileExclusive(filepath.Join(dir, entry.GetId()+".json"), data)
}

// ListHistory returns an item's history entries, oldest first. Files that
// can't be loaded are reported as FileErrors and skipped; the error is only
// for failures such as an unreadable directory.
func (s *Store) ListHistory(itemID string) ([]*kilnv1.HistoryEntry, []*FileError, error) {
	if err := checkID(itemID); err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(s.historyDir, itemID)
	ids, fileErrs, err := listJSON(dir)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]*kilnv1.HistoryEntry, 0, len(ids))
	for _, id := range ids {
		path := filepath.Join(dir, id+".json")
		entry, err := readEntry(path, id, itemID)
		if err != nil {
			fileErrs = append(fileErrs, &FileError{path, err})
			continue
		}
		entries = append(entries, entry)
	}
	return entries, fileErrs, nil
}

// readEntry reads and parses the history entry at path, checking that its id
// matches the file name and its item_id matches the directory.
func readEntry(path, id, itemID string) (*kilnv1.HistoryEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entry := &kilnv1.HistoryEntry{}
	if err := unmarshal(data, entry); err != nil {
		return nil, err
	}
	if entry.GetId() != id {
		return nil, fmt.Errorf("id %q doesn't match the file name", entry.GetId())
	}
	if entry.GetItemId() != itemID {
		return nil, fmt.Errorf("item_id %q doesn't match the directory", entry.GetItemId())
	}
	return entry, nil
}
