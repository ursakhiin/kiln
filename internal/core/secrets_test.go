package core_test

import (
	"errors"
	"strings"
	"testing"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/internal/core"
)

func TestCheckWorkState(t *testing.T) {
	if err := core.CheckWorkState(nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	ok := &kilnv1.WorkState{
		Branch:         "feat/x",
		CredentialRefs: []string{"GITHUB_TOKEN"},
		Extra:          map[string]string{"pr": "42"},
	}
	if err := core.CheckWorkState(ok); err != nil {
		t.Errorf("valid work state: %v", err)
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

	for _, row := range []struct {
		name   string
		ws     *kilnv1.WorkState
		secret string
	}{
		{"progress", &kilnv1.WorkState{Progress: "used " + aws}, aws},
		{"branch", &kilnv1.WorkState{Branch: ghp}, ghp},
		{"pull_request_url", &kilnv1.WorkState{PullRequestUrl: "https://x/" + pat}, pat},
		{"files", &kilnv1.WorkState{Files: []string{slack}}, slack},
		{"extra value", &kilnv1.WorkState{Extra: map[string]string{"k": anthropic}}, anthropic},
		{"extra key", &kilnv1.WorkState{Extra: map[string]string{sk: "v"}}, sk},
		{"transcript_path", &kilnv1.WorkState{Session: &kilnv1.AgentSession{TranscriptPath: key}}, key},
		{"session_id", &kilnv1.WorkState{Session: &kilnv1.AgentSession{SessionId: jwt}}, jwt},
		{"credential ref token", &kilnv1.WorkState{CredentialRefs: []string{ghp}}, ghp},
		{"credential ref not a name", &kilnv1.WorkState{CredentialRefs: []string{"my token"}}, "my token"},
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
