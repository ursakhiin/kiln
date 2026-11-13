package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ursakhiin/kiln/internal/core"
)

// Store reads and writes Kiln's data files under <repo>/.kiln/:
//
//	items/<id>.json                    one file per item
//	history/<item-id>/<entry-id>.json  one file per history entry
//
// The daemon is the only writer, so Store does no locking. Every write is
// atomic, so readers never see a partial file.
type Store struct {
	itemsDir   string
	historyDir string
}

// Open opens the store for the repo at repoRoot, creating .kiln/items/ and
// .kiln/history/ if they're missing.
func Open(repoRoot string) (*Store, error) {
	kiln := filepath.Join(repoRoot, ".kiln")
	s := &Store{
		itemsDir:   filepath.Join(kiln, "items"),
		historyDir: filepath.Join(kiln, "history"),
	}
	for _, dir := range []string{s.itemsDir, s.historyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// FileError is a data file that couldn't be loaded, such as a hand edit with
// invalid JSON. Listing reports it and carries on with the other files.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// checkID rejects anything that isn't a canonical ULID before it is used to
// build a path.
func checkID(id string) error {
	if !core.ValidID(id) {
		return fmt.Errorf("invalid ID %q: %w", id, core.ErrInvalidArgument)
	}
	return nil
}

// listJSON returns the IDs of the <id>.json files in dir, sorted. It skips
// names starting with "." (including in-progress writes) and names without a
// .json suffix, and reports .json files whose name isn't a valid ID. A
// missing dir has no files.
func listJSON(dir string) ([]string, []*FileError, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	var fileErrs []*FileError
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || e.IsDir() {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !core.ValidID(id) {
			fileErrs = append(fileErrs, &FileError{filepath.Join(dir, name), errors.New("file name is not a valid ID")})
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, fileErrs, nil
}
