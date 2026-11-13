package core

import "errors"

// Error categories. Callers wrap them with fmt.Errorf("...: %w", ErrX) and
// test them with errors.Is; the service maps them to Connect codes.
var (
	// ErrNotFound means an ID doesn't exist (NOT_FOUND).
	ErrNotFound = errors.New("not found")
	// ErrInvalidArgument means a request is malformed (INVALID_ARGUMENT).
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrPermissionDenied means the actor may not do this (PERMISSION_DENIED).
	ErrPermissionDenied = errors.New("permission denied")
	// ErrFailedPrecondition means the change would break a rule
	// (FAILED_PRECONDITION).
	ErrFailedPrecondition = errors.New("failed precondition")
	// ErrRevisionConflict means a write was based on an outdated revision
	// (ABORTED).
	ErrRevisionConflict = errors.New("revision conflict")
)
