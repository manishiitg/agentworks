package codexcli

import (
	"context"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Auto-review for Codex chats (#4602, owner 28 Nov: "yes we should make it
// work"). Codex's reviewer (approvalsReviewer "auto_review") only judges what
// Codex's own sandbox refuses, and Codex's sandbox cannot start inside the
// AgentWorks Seatbelt sandbox (sandbox_apply: Operation not permitted). So an
// Auto-review turn on a Mac starts Codex outside ours, under a Codex
// permissions profile that keeps the same lines:
//   - reads everywhere except AgentWorks' protected data (reopened where the
//     chat has a grant, as our sandbox does);
//   - writes only in the chat's folders, its private home and temp folders;
//   - no network.
// Anything outside goes to Codex's reviewer, which approves or refuses it.
// Switching a chat in or out of Auto-review starts a new Codex process.

const (
	metadataKeyCodexAutoReview = "codex_auto_review"
	codexAutoReviewProfile     = "agentworks-auto-review"
)

// codexWithAutoReview marks opts for an Auto-review turn of this chat, on a
// host where our sandbox is Seatbelt (the only place it blocks Codex's own).
func codexWithAutoReview(ctx context.Context, owner string, opts *llmtypes.CallOptions) *llmtypes.CallOptions {
	if opts == nil || !opts.CLISecurity.SeatbeltEnforced() || !llmtypes.CodingAgentAutoReviewForSession(ctx, owner) {
		return opts
	}
	next := *opts
	metadata := llmtypes.Metadata{}
	if opts.Metadata != nil {
		metadata = *opts.Metadata
	}
	custom := make(map[string]interface{}, len(metadata.Custom)+1)
	for key, value := range metadata.Custom {
		custom[key] = value
	}
	custom[metadataKeyCodexAutoReview] = true
	metadata.Custom = custom
	next.Metadata = &metadata
	return &next
}

func codexAutoReview(opts *llmtypes.CallOptions) bool {
	return opts != nil && codexOptionBool(opts, metadataKeyCodexAutoReview)
}

// codexAutoReviewPermissions is the Codex permissions profile for an
// Auto-review thread: the same reach our Seatbelt profile gives (seatbelt.go),
// with refusals going to the reviewer instead of failing.
func codexAutoReviewPermissions(policy *llmtypes.CLISecurityPolicy, cwd string) map[string]any {
	filesystem := map[string]any{":root": "read", ":tmpdir": "write", ":slash_tmp": "write"}
	set := func(access string, paths ...string) {
		for _, path := range paths {
			if path = strings.TrimSpace(path); path != "" {
				filesystem[path] = access
			}
		}
	}
	if policy != nil {
		set("deny", policy.ProtectedRoots...)
		set("read", policy.WorkspaceReadPaths...)
		set("read", policy.HostReadPaths...)
		set("write", policy.PrivateHome)
		set("write", policy.WorkspaceWritePaths...)
		set("write", policy.HostWritePaths...)
	}
	set("write", cwd)
	return map[string]any{"filesystem": filesystem, "network": map[string]any{"enabled": false}}
}

// codexAutoReviewThreadParams puts Auto-review on a thread's start or resume.
// Codex ignores a permissions profile when a sandbox mode is set, so the
// thread's sandbox is left out.
func codexAutoReviewThreadParams(params, threadConfig map[string]any, opts *llmtypes.CallOptions, cwd string) {
	if !codexAutoReview(opts) {
		return
	}
	delete(params, "sandbox")
	threadConfig["permissions"] = map[string]any{codexAutoReviewProfile: codexAutoReviewPermissions(opts.CLISecurity, cwd)}
	params["permissions"] = codexAutoReviewProfile
	params["approvalPolicy"] = "on-request"
	params["approvalsReviewer"] = "auto_review"
}

// codexGuardianRow shows the reviewer's verdict (guardianWarning: "Automatic
// approval review approved (risk: low, ...): why") as one row.
func codexGuardianRow(message string, metadata map[string]any) []llmtypes.StreamChunk {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	label := "Auto-review"
	lower := strings.ToLower(message)
	switch {
	case strings.HasPrefix(lower, "automatic approval review approved"):
		label = "Auto-review approved an action"
	case strings.HasPrefix(lower, "automatic approval review denied"), strings.HasPrefix(lower, "automatic approval review rejected"), strings.HasPrefix(lower, "automatic approval review declined"):
		label = "Auto-review refused an action"
	}
	return codexNoticeRow("guardian-"+codexRandomHex(6), "codex_auto_review", label, message, metadata)
}
