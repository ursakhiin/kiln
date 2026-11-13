package core

import "fmt"

// CheckRevision returns ErrRevisionConflict if a write based on revision
// base would overwrite a newer current revision.
func CheckRevision(current, base int64) error {
	if current != base {
		return fmt.Errorf("item is at revision %d, write is based on revision %d: %w", current, base, ErrRevisionConflict)
	}
	return nil
}
