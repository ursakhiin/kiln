package views_test

import (
	"strings"
	"testing"
	"time"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/views"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ts(h int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 11, 13, h, 0, 0, 0, time.UTC))
}

func info(id, title string, st kilnv1.Status) *kilnv1.ItemInfo {
	return &kilnv1.ItemInfo{Item: &kilnv1.Item{Id: id, Title: title, Revision: 1}, Status: st}
}

func TestRenderTaskContextFull(t *testing.T) {
	task := info("t2", "Write the renderer", kilnv1.Status_STATUS_IN_PROGRESS)
	task.Item.Revision = 4
	task.Item.Description = "Render markdown."
	task.Claim = &kilnv1.Claim{AgentId: "agent-1", ExpireTime: ts(12)}
	task.Item.Kind = &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "f1", WorkState: &kilnv1.WorkState{
		Branch: "kiln/t2", WorktreePath: "/w/t2", BaseCommit: "aaa", HeadCommit: "bbb",
		PullRequestUrl: "https://example.com/pr/1",
		Session: &kilnv1.AgentSession{Host: "claude-code", HostVersion: "2.0", SessionId: "s1",
			TranscriptPath: "/tr.jsonl", Model: "opus"},
		Progress:       "Half done.",
		Files:          []string{"a.md", "b.md"},
		CredentialRefs: []string{"GH_TOKEN", "NPM_TOKEN"},
		Extra:          map[string]string{"note": "hi", "alpha": "first"},
		UpdateTime:     ts(10),
	}}}
	feature := info("f1", "Views", kilnv1.Status_STATUS_IN_PROGRESS)
	feature.Item.Revision = 2
	feature.Item.Description = "All views."
	c := views.TaskContext{
		Task:    task,
		Feature: feature,
		Project: &kilnv1.Item{Id: "p1", Title: "Kiln"},
		Dependencies: []views.Dependency{
			{ID: "t0", Item: info("t0", "Done one", kilnv1.Status_STATUS_DONE)},
			{ID: "t1", BlockUntil: kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW, Item: info("t1", "Blocking", kilnv1.Status_STATUS_IN_PROGRESS)},
			{ID: "t9"},
		},
		FeatureDependencies: []views.Dependency{
			{ID: "f0", BlockUntil: kilnv1.BlockUntil_BLOCK_UNTIL_IN_REVIEW, Item: info("f0", "Base", kilnv1.Status_STATUS_IN_REVIEW)},
		},
		Siblings: []*kilnv1.ItemInfo{
			info("t1", "Blocking", kilnv1.Status_STATUS_IN_PROGRESS), task,
			info("t3", "Later", kilnv1.Status_STATUS_DRAFT),
		},
		Resumed: true,
	}
	want := "# Task: Write the renderer\n\n" +
		"- ID: `t2`\n- Revision: 4\n- Status: in progress\n" +
		"- Claimed by: agent `agent-1` until 2026-11-13T12:00:00Z\n" +
		"- Feature: Views (`f1`)\n- Project: Kiln (`p1`)\n\n" +
		"> **Resumed task:** another agent started this task and stopped before finishing. Read the work state below and continue from it instead of starting over; the branch may hold partial work.\n\n" +
		"## Description\n\nRender markdown.\n\n" +
		"## Dependencies\n\n" +
		"- [done] Done one (`t0`), blocks until done, satisfied\n" +
		"- [in progress] Blocking (`t1`), blocks until in review\n" +
		"- [missing] `t9`, blocks until done\n\n" +
		"## Work state\n\n" +
		"- Branch: `kiln/t2`\n- Worktree: `/w/t2`\n- Base commit: `aaa`\n- Head commit: `bbb`\n" +
		"- Pull request: https://example.com/pr/1\n" +
		"- Session: claude-code 2.0, session `s1`, model `opus`, transcript `/tr.jsonl`\n" +
		"- Files: `a.md`, `b.md`\n- Credentials needed (names only): `GH_TOKEN`, `NPM_TOKEN`\n- alpha: first\n- note: hi\n- Updated: 2026-11-13T10:00:00Z\n\n" +
		"### Progress\n\nHalf done.\n\n" +
		"## Feature: Views\n\n- ID: `f1`\n- Revision: 2\n- Status: in progress\n\nAll views.\n\n" +
		"### Feature dependencies\n\n- [in review] Base (`f0`), blocks until in review, satisfied\n\n" +
		"### Tasks in this feature\n\n" +
		"1. [in progress] Blocking (`t1`)\n2. [in progress] Write the renderer (`t2`) ← this task\n3. [draft] Later (`t3`)\n"
	if got := views.RenderTaskContext(c); got != want {
		t.Errorf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderTaskContextMinimal(t *testing.T) {
	task := info("t1", "Bare", kilnv1.Status_STATUS_READY)
	task.Item.Kind = &kilnv1.Item_Task{Task: &kilnv1.Task{FeatureId: "f9"}}
	got := views.RenderTaskContext(views.TaskContext{Task: task})
	for _, s := range []string{"_No description._", "## Dependencies\n\nNone.\n", "## Work state\n\nNone recorded yet.\n", "- Feature: missing (`f9`)"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in\n%s", s, got)
		}
	}
	for _, s := range []string{"## Feature:", "Resumed", "Claimed by", "Project:"} {
		if strings.Contains(got, s) {
			t.Errorf("unexpected %q in\n%s", s, got)
		}
	}
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("must end with a single newline: %q", got)
	}
}

func TestStatusName(t *testing.T) {
	cases := map[kilnv1.Status]string{
		kilnv1.Status_STATUS_UNSPECIFIED: "unknown",
		kilnv1.Status_STATUS_DRAFT:       "draft",
		kilnv1.Status_STATUS_READY:       "ready",
		kilnv1.Status_STATUS_IN_PROGRESS: "in progress",
		kilnv1.Status_STATUS_IN_REVIEW:   "in review",
		kilnv1.Status_STATUS_DONE:        "done",
	}
	for s, want := range cases {
		if got := views.StatusName(s); got != want {
			t.Errorf("StatusName(%v) = %q, want %q", s, got, want)
		}
	}
}
