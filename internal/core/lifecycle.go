package core

import (
	"fmt"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
)

// CheckStatusChange reports whether actor may set item's status to to. For a
// task this is its status; for a feature it is its status_override, where
// STATUS_UNSPECIFIED clears the override.
func CheckStatusChange(actor underdrawv1.ActorType, item *underdrawv1.Item, to underdrawv1.Status) error {
	if actor != underdrawv1.ActorType_ACTOR_TYPE_USER && actor != underdrawv1.ActorType_ACTOR_TYPE_AGENT {
		return fmt.Errorf("%v can't change status: %w", actor, ErrPermissionDenied)
	}
	switch k := item.GetKind().(type) {
	case *underdrawv1.Item_Project:
		return fmt.Errorf("projects have no status: %w", ErrFailedPrecondition)
	case *underdrawv1.Item_Feature:
		if actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
			return fmt.Errorf("only the user can override a feature's status: %w", ErrPermissionDenied)
		}
		return nil
	case *underdrawv1.Item_Task:
		if to == underdrawv1.Status_STATUS_UNSPECIFIED {
			return fmt.Errorf("a task needs a status: %w", ErrInvalidArgument)
		}
		if actor == underdrawv1.ActorType_ACTOR_TYPE_USER {
			return nil
		}
		switch k.Task.GetStatus() {
		case underdrawv1.Status_STATUS_DONE:
			return fmt.Errorf("only the user can reopen a done task: %w", ErrPermissionDenied)
		case underdrawv1.Status_STATUS_DRAFT:
			return fmt.Errorf("only the user can mark a draft task ready: %w", ErrPermissionDenied)
		}
		switch to {
		case underdrawv1.Status_STATUS_READY, underdrawv1.Status_STATUS_IN_PROGRESS, underdrawv1.Status_STATUS_IN_REVIEW:
			return nil
		}
		return fmt.Errorf("agents can't set %v: %w", to, ErrPermissionDenied)
	default:
		return fmt.Errorf("item has no kind: %w", ErrInvalidArgument)
	}
}

// DeriveFeatureStatus returns a feature's effective status from its override,
// its draft flag and the statuses of its tasks.
func DeriveFeatureStatus(f *underdrawv1.Feature, tasks []underdrawv1.Status) underdrawv1.Status {
	if o := f.GetStatusOverride(); o != underdrawv1.Status_STATUS_UNSPECIFIED {
		return o
	}
	if f.GetDraft() {
		return underdrawv1.Status_STATUS_DRAFT
	}
	if len(tasks) == 0 {
		return underdrawv1.Status_STATUS_READY
	}
	allDone, allReviewOrDone, anyStarted := true, true, false
	for _, s := range tasks {
		switch s {
		case underdrawv1.Status_STATUS_DONE:
			anyStarted = true
		case underdrawv1.Status_STATUS_IN_REVIEW:
			allDone = false
			anyStarted = true
		case underdrawv1.Status_STATUS_IN_PROGRESS:
			allDone, allReviewOrDone = false, false
			anyStarted = true
		default:
			allDone, allReviewOrDone = false, false
		}
	}
	switch {
	case allDone:
		return underdrawv1.Status_STATUS_DONE
	case allReviewOrDone:
		return underdrawv1.Status_STATUS_IN_REVIEW
	case anyStarted:
		return underdrawv1.Status_STATUS_IN_PROGRESS
	default:
		return underdrawv1.Status_STATUS_READY
	}
}

// IsBlocked reports whether any dependency is still blocking. statusOf returns
// a blocking item's effective status; an item it can't find counts as
// blocking.
func IsBlocked(deps []*underdrawv1.Dependency, statusOf func(id string) (underdrawv1.Status, bool)) bool {
	for _, d := range deps {
		s, ok := statusOf(d.GetItemId())
		if !ok {
			return true
		}
		satisfied := s == underdrawv1.Status_STATUS_DONE ||
			d.GetBlockUntil() == underdrawv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW && s == underdrawv1.Status_STATUS_IN_REVIEW
		if !satisfied {
			return true
		}
	}
	return false
}
