package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Dependency is one dependency of a task or feature, resolved by the caller.
type Dependency struct {
	ID         string
	BlockUntil kilnv1.BlockUntil // UNSPECIFIED renders as "done"
	Item       *kilnv1.ItemInfo  // nil if the item no longer exists
}

// TaskContext is everything RenderTaskContext needs to describe a task.
type TaskContext struct {
	Task                *kilnv1.ItemInfo
	Feature             *kilnv1.ItemInfo   // nil if missing
	Project             *kilnv1.Item       // nil if missing
	Dependencies        []Dependency       // the task's
	FeatureDependencies []Dependency       // the feature's
	Siblings            []*kilnv1.ItemInfo // every task in the feature, rank order, including Task
	Resumed             bool
}

// StatusName returns the lowercase display name of a status.
func StatusName(s kilnv1.Status) string {
	switch s {
	case kilnv1.Status_STATUS_DRAFT:
		return "draft"
	case kilnv1.Status_STATUS_READY:
		return "ready"
	case kilnv1.Status_STATUS_IN_PROGRESS:
		return "in progress"
	case kilnv1.Status_STATUS_IN_REVIEW:
		return "in review"
	case kilnv1.Status_STATUS_DONE:
		return "done"
	}
	return "unknown"
}

const (
	noDescription = "_No description._"
	resumedNote   = "> **Resumed task:** another agent started this task and stopped before finishing. " +
		"Read the work state below and continue from it instead of starting over; the branch may hold partial work."
)

// RenderTaskContext renders the markdown context an agent receives for a task.
func RenderTaskContext(c TaskContext) string {
	task := c.Task.GetItem()
	var sections []string

	var head strings.Builder
	fmt.Fprintf(&head, "# Task: %s\n\n", task.GetTitle())
	fmt.Fprintf(&head, "- ID: `%s`\n", task.GetId())
	fmt.Fprintf(&head, "- Revision: %d\n", task.GetRevision())
	fmt.Fprintf(&head, "- Status: %s", StatusName(c.Task.GetStatus()))
	if cl := c.Task.GetClaim(); cl != nil {
		fmt.Fprintf(&head, "\n- Claimed by: agent `%s` until %s", cl.GetAgentId(), formatTime(cl.GetExpireTime()))
	}
	if c.Feature != nil {
		fmt.Fprintf(&head, "\n- Feature: %s (`%s`)", c.Feature.GetItem().GetTitle(), c.Feature.GetItem().GetId())
	} else {
		fmt.Fprintf(&head, "\n- Feature: missing (`%s`)", task.GetTask().GetFeatureId())
	}
	if c.Project != nil {
		fmt.Fprintf(&head, "\n- Project: %s (`%s`)", c.Project.GetTitle(), c.Project.GetId())
	}
	sections = append(sections, head.String())

	if c.Resumed {
		sections = append(sections, resumedNote)
	}

	sections = append(sections, "## Description\n\n"+description(task))
	sections = append(sections, "## Dependencies\n\n"+renderDependencies(c.Dependencies))
	sections = append(sections, "## Work state\n\n"+renderWorkState(task.GetTask().GetWorkState()))

	if c.Feature != nil {
		sections = append(sections, renderFeature(c)...)
	}

	return strings.Join(sections, "\n\n") + "\n"
}

func description(it *kilnv1.Item) string {
	if d := strings.TrimSpace(it.GetDescription()); d != "" {
		return d
	}
	return noDescription
}

func renderDependencies(deps []Dependency) string {
	if len(deps) == 0 {
		return "None."
	}
	lines := make([]string, 0, len(deps))
	for _, d := range deps {
		until := "done"
		if d.BlockUntil == kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW {
			until = "in review"
		}
		if d.Item == nil {
			lines = append(lines, fmt.Sprintf("- [missing] `%s`, blocks until %s", d.ID, until))
			continue
		}
		line := fmt.Sprintf("- [%s] %s (`%s`), blocks until %s",
			StatusName(d.Item.GetStatus()), d.Item.GetItem().GetTitle(), d.ID, until)
		if satisfied(d) {
			line += ", satisfied"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func satisfied(d Dependency) bool {
	if d.Item == nil {
		return false
	}
	switch d.Item.GetStatus() {
	case kilnv1.Status_STATUS_DONE:
		return true
	case kilnv1.Status_STATUS_IN_REVIEW:
		return d.BlockUntil == kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW
	}
	return false
}

func renderWorkState(ws *kilnv1.WorkState) string {
	if ws == nil {
		return "None recorded yet."
	}
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, "- "+label+": "+value)
		}
	}
	code := func(v string) string {
		if v == "" {
			return ""
		}
		return "`" + v + "`"
	}
	codes := func(vs []string) string {
		quoted := make([]string, 0, len(vs))
		for _, v := range vs {
			quoted = append(quoted, "`"+v+"`")
		}
		return strings.Join(quoted, ", ")
	}

	add("Branch", code(ws.GetBranch()))
	add("Worktree", code(ws.GetWorktreePath()))
	add("Base commit", code(ws.GetBaseCommit()))
	add("Head commit", code(ws.GetHeadCommit()))
	add("Pull request", ws.GetPullRequestUrl())
	if s := ws.GetSession(); s != nil {
		var parts []string
		if hv := strings.TrimSpace(s.GetHost() + " " + s.GetHostVersion()); hv != "" {
			parts = append(parts, hv)
		}
		if s.GetSessionId() != "" {
			parts = append(parts, "session "+code(s.GetSessionId()))
		}
		if s.GetModel() != "" {
			parts = append(parts, "model "+code(s.GetModel()))
		}
		if s.GetTranscriptPath() != "" {
			parts = append(parts, "transcript "+code(s.GetTranscriptPath()))
		}
		add("Session", strings.Join(parts, ", "))
	}
	add("Files", codes(ws.GetFiles()))
	add("Credentials needed (names only)", codes(ws.GetCredentialRefs()))
	keys := make([]string, 0, len(ws.GetExtra()))
	for k := range ws.GetExtra() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(k, ws.GetExtra()[k])
	}
	if ws.GetUpdateTime() != nil {
		add("Updated", formatTime(ws.GetUpdateTime()))
	}

	var blocks []string
	if len(lines) > 0 {
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if p := strings.TrimSpace(ws.GetProgress()); p != "" {
		blocks = append(blocks, "### Progress\n\n"+p)
	}
	if len(blocks) == 0 {
		return "None recorded yet."
	}
	return strings.Join(blocks, "\n\n")
}

func renderFeature(c TaskContext) []string {
	f := c.Feature.GetItem()
	out := []string{
		fmt.Sprintf("## Feature: %s\n\n- ID: `%s`\n- Revision: %d\n- Status: %s",
			f.GetTitle(), f.GetId(), f.GetRevision(), StatusName(c.Feature.GetStatus())),
		description(f),
	}
	if len(c.FeatureDependencies) > 0 {
		out = append(out, "### Feature dependencies\n\n"+renderDependencies(c.FeatureDependencies))
	}
	if len(c.Siblings) > 0 {
		lines := make([]string, 0, len(c.Siblings))
		for i, s := range c.Siblings {
			line := fmt.Sprintf("%d. [%s] %s (`%s`)", i+1, StatusName(s.GetStatus()), s.GetItem().GetTitle(), s.GetItem().GetId())
			if s.GetItem().GetId() == c.Task.GetItem().GetId() {
				line += " ← this task"
			}
			lines = append(lines, line)
		}
		out = append(out, "### Tasks in this feature\n\n"+strings.Join(lines, "\n"))
	}
	return out
}

func formatTime(t *timestamppb.Timestamp) string {
	return t.AsTime().UTC().Format(time.RFC3339)
}
