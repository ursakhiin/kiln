package core_test

import (
	"errors"
	"strings"
	"testing"

	underdrawv1 "github.com/ursakhiin/underdraw/gen/go/underdraw/v1"
	"github.com/ursakhiin/underdraw/internal/core"
)

func TestCheckWorkState(t *testing.T) {
	if err := core.CheckWorkState(nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	ok := &underdrawv1.WorkState{
		Branch:         "feat/x",
		CredentialRefs: []string{"GITHUB_TOKEN"},
		Extra:          map[string]string{"pr": "42"},
	}
	if err := core.CheckWorkState(ok); err != nil {
		t.Errorf("valid work state: %v", err)
	}
	// Agents will name branches and worktrees after task IDs; "task-<ULID>"
	// contains "sk-" followed by 26 alphanumerics.
	named := &underdrawv1.WorkState{
		Branch:       "task-" + core.NewID(),
		WorktreePath: ".claude/worktrees/task-" + core.NewID(),
		Progress:     "continues task-" + strings.ToLower(core.NewID()),
	}
	if err := core.CheckWorkState(named); err != nil {
		t.Errorf("work state naming a task ID: %v", err)
	}
}

func TestCheckWorkStateRejectsSecrets(t *testing.T) {
	aws := "AKIA" + strings.Repeat("A", 16)
	ghp := "ghp_" + strings.Repeat("a", 36)
	pat := "github_pat_" + strings.Repeat("a", 22)
	slack := "xoxb-" + strings.Repeat("1", 10)
	anthropic := "sk-ant-" + strings.Repeat("a", 20)
	sk := "sk-" + strings.Repeat("a", 20)
	key := "-----BEGIN RSA PRIVATE KEY-----"
	jwt := "eyJ" + strings.Repeat("a", 10) + "." + strings.Repeat("b", 10) + "." + strings.Repeat("c", 10)
	openaiProject := "sk-proj-" + strings.Repeat("aB3_x-", 8)

	for _, row := range []struct {
		name   string
		ws     *underdrawv1.WorkState
		secret string
	}{
		{"progress", &underdrawv1.WorkState{Progress: "used " + aws}, aws},
		{"branch", &underdrawv1.WorkState{Branch: ghp}, ghp},
		{"pull_request_url", &underdrawv1.WorkState{PullRequestUrl: "https://x/" + pat}, pat},
		{"files", &underdrawv1.WorkState{Files: []string{slack}}, slack},
		{"extra value", &underdrawv1.WorkState{Extra: map[string]string{"k": anthropic}}, anthropic},
		{"extra key", &underdrawv1.WorkState{Extra: map[string]string{sk: "v"}}, sk},
		{"transcript_path", &underdrawv1.WorkState{Session: &underdrawv1.AgentSession{TranscriptPath: key}}, key},
		{"session_id", &underdrawv1.WorkState{Session: &underdrawv1.AgentSession{SessionId: jwt}}, jwt},
		{"head_commit openai project key", &underdrawv1.WorkState{HeadCommit: "key=" + openaiProject}, openaiProject},
		{"credential ref token", &underdrawv1.WorkState{CredentialRefs: []string{ghp}}, ghp},
		{"credential ref not a name", &underdrawv1.WorkState{CredentialRefs: []string{"my token"}}, "my token"},
	} {
		err := core.CheckWorkState(row.ws)
		if !errors.Is(err, core.ErrInvalidArgument) {
			t.Errorf("%s: got %v, want ErrInvalidArgument", row.name, err)
			continue
		}
		if strings.Contains(err.Error(), row.secret) {
			t.Errorf("%s: error message contains the secret: %v", row.name, err)
		}
	}
}
