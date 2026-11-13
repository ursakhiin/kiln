package core_test

import (
	"errors"
	"testing"

	"github.com/ursakhiin/underdraw/internal/core"
)

func TestCheckRevision(t *testing.T) {
	if err := core.CheckRevision(3, 3); err != nil {
		t.Errorf("same revision: %v", err)
	}
	if err := core.CheckRevision(4, 3); !errors.Is(err, core.ErrRevisionConflict) {
		t.Errorf("stale revision: got %v", err)
	}
}
