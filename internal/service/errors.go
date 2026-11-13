package service

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

// ConflictError is returned for a stale base_revision; it matches
// core.ErrRevisionConflict. Current is the item as it is now.
type ConflictError struct{ Current *underdrawv1.ItemInfo }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("revision conflict: item %s is at revision %d",
		e.Current.GetItem().GetId(), e.Current.GetItem().GetRevision())
}

func (e *ConflictError) Unwrap() error { return core.ErrRevisionConflict }

// ErrorCode maps an error to its Connect code: the five core sentinels to
// their codes, and anything else to CodeInternal.
func ErrorCode(err error) connect.Code {
	switch {
	case errors.Is(err, core.ErrNotFound):
		return connect.CodeNotFound
	case errors.Is(err, core.ErrInvalidArgument):
		return connect.CodeInvalidArgument
	case errors.Is(err, core.ErrPermissionDenied):
		return connect.CodePermissionDenied
	case errors.Is(err, core.ErrFailedPrecondition):
		return connect.CodeFailedPrecondition
	case errors.Is(err, core.ErrRevisionConflict):
		return connect.CodeAborted
	default:
		return connect.CodeInternal
	}
}
