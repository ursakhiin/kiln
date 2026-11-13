package core

import (
	"fmt"
	"regexp"
	"slices"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
)

// secretPatterns match common credential formats. Work state is committed to
// git with its task, so values matching these are rejected.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                              // AWS access key ID
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`),                                    // GitHub token
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),                                  // GitHub fine-grained token
	regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),                                  // Slack token
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`),                                     // Anthropic API key
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),                                           // OpenAI-style API key
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),                            // PEM private key
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWT
}

// credentialRef is what a credential_refs entry must look like: a name, such
// as an environment variable, never a value.
var credentialRef = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// CheckWorkState returns ErrInvalidArgument if work state contains anything
// that looks like a credential value. Errors name the field, never the value.
func CheckWorkState(ws *kilnv1.WorkState) error {
	if ws == nil {
		return nil
	}
	type field struct{ name, value string }
	fields := []field{
		{"branch", ws.GetBranch()},
		{"worktree_path", ws.GetWorktreePath()},
		{"base_commit", ws.GetBaseCommit()},
		{"head_commit", ws.GetHeadCommit()},
		{"pull_request_url", ws.GetPullRequestUrl()},
		{"progress", ws.GetProgress()},
		{"session.host", ws.GetSession().GetHost()},
		{"session.host_version", ws.GetSession().GetHostVersion()},
		{"session.session_id", ws.GetSession().GetSessionId()},
		{"session.transcript_path", ws.GetSession().GetTranscriptPath()},
		{"session.model", ws.GetSession().GetModel()},
	}
	for i, f := range ws.GetFiles() {
		fields = append(fields, field{fmt.Sprintf("files[%d]", i), f})
	}
	for i, r := range ws.GetCredentialRefs() {
		name := fmt.Sprintf("credential_refs[%d]", i)
		if !credentialRef.MatchString(r) {
			return fmt.Errorf("%s must be a credential name, not a value: %w", name, ErrInvalidArgument)
		}
		fields = append(fields, field{name, r})
	}
	keys := make([]string, 0, len(ws.GetExtra()))
	for k := range ws.GetExtra() {
		keys = append(keys, k)
	}
	slices.Sort(keys) // deterministic error for the same input
	for i, k := range keys {
		// A key can itself be the secret, so keys are named by position only.
		fields = append(fields, field{fmt.Sprintf("an extra key (#%d in sorted order)", i+1), k})
		if looksLikeSecret(k) {
			continue // don't put a secret key in the value's field name
		}
		fields = append(fields, field{fmt.Sprintf("extra[%q]", k), ws.GetExtra()[k]})
	}
	for _, f := range fields {
		if looksLikeSecret(f.value) {
			return fmt.Errorf("%s looks like a credential; record credential names in credential_refs, never values: %w", f.name, ErrInvalidArgument)
		}
	}
	return nil
}

func looksLikeSecret(s string) bool {
	for _, p := range secretPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}
