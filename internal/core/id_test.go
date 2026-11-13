package core_test

import (
	"testing"

	"github.com/ursakhiin/underdraw/internal/core"
)

func TestNewIDIsValidAndIncreasing(t *testing.T) {
	a, b := core.NewID(), core.NewID()
	if !core.ValidID(a) || !core.ValidID(b) {
		t.Fatalf("invalid IDs: %q %q", a, b)
	}
	if a >= b {
		t.Fatalf("IDs not increasing: %q >= %q", a, b)
	}
}

func TestValidID(t *testing.T) {
	if !core.ValidID("01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Error("rejected a canonical ULID")
	}
	for _, id := range []string{
		"", "../../etc/passwd", "01ARZ3NDEKTSV4RRFFQ69G5FA", // too short
		"01ARZ3NDEKTSV4RRFFQ69G5FAV/x", "01arz3ndektsv4rrffq69g5fav", // lowercase
		"81ARZ3NDEKTSV4RRFFQ69G5FAV", // overflows 128 bits
	} {
		if core.ValidID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}
