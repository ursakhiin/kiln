package core_test

import (
	"errors"
	"testing"

	"github.com/ursakhiin/kiln/internal/core"
)

func TestRankBetween(t *testing.T) {
	for _, c := range []struct{ before, after, want string }{
		{"", "", "i"},
		{"", "i", "9"},
		{"i", "", "r"},
		{"a", "b", "ai"},
		{"az", "b", "azi"},
		{"a1", "a2", "a1i"},
		{"", "1", "0i"},
	} {
		got, err := core.RankBetween(c.before, c.after)
		if err != nil || got != c.want {
			t.Errorf("RankBetween(%q, %q) = %q, %v; want %q", c.before, c.after, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"b", "a"}, {"a", "a"}, {"a0", ""}, {"", "A"}} {
		if _, err := core.RankBetween(c[0], c[1]); !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("RankBetween(%q, %q): got %v, want ErrInvalidArgument", c[0], c[1], err)
		}
	}
}

func TestRankBetweenRepeatedInserts(t *testing.T) {
	ranks := []string{"i"}
	for i := 0; i < 1000; i++ {
		front, err := core.RankBetween("", ranks[0])
		if err != nil {
			t.Fatal(err)
		}
		mid, err := core.RankBetween(front, ranks[0])
		if err != nil {
			t.Fatal(err)
		}
		ranks = append([]string{front, mid}, ranks...)
	}
	for i, r := range ranks {
		if !core.ValidRank(r) {
			t.Fatalf("invalid rank %q", r)
		}
		if i > 0 && ranks[i-1] >= r {
			t.Fatalf("not ordered: %q >= %q", ranks[i-1], r)
		}
	}
}
