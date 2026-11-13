package core

import (
	"fmt"
	"strings"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
)

// NormalizeNewItem validates an item for CreateItem and fills in defaults:
// features created by agents become drafts, and tasks without a status
// become ready. Underdraw assigns the ID, revision, timestamps and rank, so the
// caller must leave them unset. It doesn't check that the parent exists.
func NormalizeNewItem(actor underdrawv1.ActorType, item *underdrawv1.Item) error {
	switch {
	case item.GetId() != "":
		return invalid("id is assigned by Underdraw")
	case item.GetRevision() != 0:
		return invalid("revision is assigned by Underdraw")
	case item.GetCreateTime() != nil || item.GetUpdateTime() != nil:
		return invalid("timestamps are assigned by Underdraw")
	case strings.TrimSpace(item.GetTitle()) == "":
		return invalid("title is required")
	}
	switch k := item.GetKind().(type) {
	case *underdrawv1.Item_Project:
		return nil
	case *underdrawv1.Item_Feature:
		f := k.Feature
		switch {
		case f.GetProjectId() == "":
			return invalid("feature needs a project_id")
		case f.GetRank() != "":
			return invalid("rank is assigned by Underdraw")
		case len(f.GetDependsOn()) > 0:
			return invalid("add dependencies with AddDependency")
		case f.GetStatusOverride() != underdrawv1.Status_STATUS_UNSPECIFIED:
			return invalid("set a status override with SetStatus")
		}
		if actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
			f.Draft = true
		}
		return nil
	case *underdrawv1.Item_Task:
		tk := k.Task
		switch {
		case tk.GetFeatureId() == "":
			return invalid("task needs a feature_id")
		case tk.GetRank() != "":
			return invalid("rank is assigned by Underdraw")
		case len(tk.GetDependsOn()) > 0:
			return invalid("add dependencies with AddDependency")
		case tk.GetWorkState() != nil:
			return invalid("record work state with UpdateWorkState")
		}
		switch tk.GetStatus() {
		case underdrawv1.Status_STATUS_UNSPECIFIED:
			tk.Status = underdrawv1.Status_STATUS_READY
		case underdrawv1.Status_STATUS_DRAFT, underdrawv1.Status_STATUS_READY:
		default:
			return invalid("a new task must be draft or ready")
		}
		return nil
	default:
		return invalid("set exactly one of project, feature or task")
	}
}

// updatablePaths lists the UpdateItem field paths allowed for each kind.
// Everything else has its own RPC.
var updatablePaths = map[string]map[string]bool{
	"project": {"title": true, "description": true},
	"feature": {"title": true, "description": true, "feature.project_id": true, "feature.draft": true},
	"task":    {"title": true, "description": true, "task.feature_id": true},
}

// CheckUpdate validates an UpdateItem request: which fields it may change,
// and who may change them.
func CheckUpdate(actor underdrawv1.ActorType, current, updated *underdrawv1.Item, paths []string) error {
	if len(paths) == 0 {
		return invalid("update_mask must name at least one field")
	}
	allowed := updatablePaths[kindName(current)]
	for _, p := range paths {
		if !allowed[p] {
			return invalid(fmt.Sprintf("field %q can't be changed with UpdateItem", p))
		}
		switch p {
		case "title":
			if strings.TrimSpace(updated.GetTitle()) == "" {
				return invalid("title is required")
			}
		case "feature.project_id":
			if updated.GetFeature().GetProjectId() == "" {
				return invalid("feature needs a project_id")
			}
		case "task.feature_id":
			if updated.GetTask().GetFeatureId() == "" {
				return invalid("task needs a feature_id")
			}
		case "feature.draft":
			promoting := current.GetFeature().GetDraft() && !updated.GetFeature().GetDraft()
			if promoting && actor == underdrawv1.ActorType_ACTOR_TYPE_AGENT {
				return fmt.Errorf("only the user can mark an idea ready: %w", ErrPermissionDenied)
			}
		}
	}
	return nil
}

func kindName(item *underdrawv1.Item) string {
	switch item.GetKind().(type) {
	case *underdrawv1.Item_Project:
		return "project"
	case *underdrawv1.Item_Feature:
		return "feature"
	case *underdrawv1.Item_Task:
		return "task"
	}
	return ""
}

func invalid(msg string) error {
	return fmt.Errorf("%s: %w", msg, ErrInvalidArgument)
}
