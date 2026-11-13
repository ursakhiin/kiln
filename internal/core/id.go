package core

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

// NewID returns a new item or history entry ID: a canonical uppercase ULID.
// IDs from one process are strictly increasing.
func NewID() string {
	return ulid.Make().String()
}

// ValidID reports whether id is a canonical uppercase ULID. Store paths are
// built from IDs, so anything else (including path separators) is rejected.
func ValidID(id string) bool {
	if _, err := ulid.ParseStrict(id); err != nil {
		return false
	}
	return id == strings.ToUpper(id)
}
