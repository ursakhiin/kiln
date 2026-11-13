package core

import (
	"fmt"
	"strings"
)

// Rank digits, in ASCII order, so ranks compare correctly as plain strings.
const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

// ValidRank reports whether r is a valid rank: non-empty, only the digits
// 0-9a-z, and not ending in 0. The last rule guarantees there is always room
// to insert another rank before r.
func ValidRank(r string) bool {
	if r == "" || r[len(r)-1] == '0' {
		return false
	}
	for i := 0; i < len(r); i++ {
		if strings.IndexByte(digits, r[i]) < 0 {
			return false
		}
	}
	return true
}

// RankBetween returns a rank that sorts strictly between before and after.
// An empty before or after means an open end, so RankBetween(last, "")
// appends and RankBetween("", first) prepends.
func RankBetween(before, after string) (string, error) {
	if before != "" && !ValidRank(before) {
		return "", fmt.Errorf("invalid rank %q: %w", before, ErrInvalidArgument)
	}
	if after != "" && !ValidRank(after) {
		return "", fmt.Errorf("invalid rank %q: %w", after, ErrInvalidArgument)
	}
	if before != "" && after != "" && before >= after {
		return "", fmt.Errorf("rank %q is not before %q: %w", before, after, ErrInvalidArgument)
	}
	return midpoint(before, after), nil
}

// midpoint is David Greenspan's fractional-indexing midpoint over base-36
// digits. An empty b means +infinity. It requires a < b and neither ending
// in 0, which RankBetween checks.
func midpoint(a, b string) string {
	if b != "" {
		// Keep the common prefix (a is treated as padded with 0s).
		n := 0
		for n < len(b) && digitAt(a, n) == b[n] {
			n++
		}
		if n > 0 {
			return b[:n] + midpoint(suffix(a, n), b[n:])
		}
	}
	// The first digits differ.
	da := 0
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	db := len(digits)
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		return string(digits[(da+db+1)/2])
	}
	// The first digits are consecutive.
	if len(b) > 1 {
		return b[:1]
	}
	return string(digits[da]) + midpoint(suffix(a, 1), "")
}

// digitAt returns a[i], or '0' past the end of a.
func digitAt(a string, i int) byte {
	if i < len(a) {
		return a[i]
	}
	return '0'
}

// suffix returns a[n:], or "" when n >= len(a).
func suffix(a string, n int) string {
	if n >= len(a) {
		return ""
	}
	return a[n:]
}
