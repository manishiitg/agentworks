package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// A chat's access mode (#3885): what its coding agent can reach and who approves its actions. The person picks it
// per chat from the mode menu in the message box. Plan stays the per-message plan switch.
//   - "" (This project): the project folder and the folders attached to the project; the agent approves its own
//     actions, since nothing else is reachable.
//   - auto_review: the person's whole Mac, with the program's own reviewer approving or refusing each action
//     (Claude Code's auto mode, Cursor's Auto-review). Only programs with a working reviewer offer it.
//   - full_access: the person's whole Mac, no checks.
//
// Whole-Mac reach exists only on a personal Mac; on a server every mode stays inside the chat's folders.
const (
	chatAccessProject    = ""
	chatAccessAutoReview = "auto_review"
	chatAccessFull       = "full_access"
)

// chatAccessWholeMacAlias names the grant a whole-Mac mode adds for its turn ($WORK_FOLDER_WHOLE_MAC).
const chatAccessWholeMacAlias = "whole_mac"

func normalizeChatAccessMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "project", "this_project":
		return chatAccessProject, nil
	case chatAccessAutoReview, "auto-review", "auto":
		return chatAccessAutoReview, nil
	case chatAccessFull, "full-access", "full":
		return chatAccessFull, nil
	}
	return "", fmt.Errorf("access_mode must be project, auto_review or full_access")
}

func chatAccessRank(mode string) int {
	switch mode {
	case chatAccessAutoReview:
		return 1
	case chatAccessFull:
		return 2
	}
	return 0
}

// chatAutoReviewSupported: the programs whose own reviewer was checked to work under AgentWorks' sandbox. Codex's
// reviewer only sees approval requests its own sandbox raises, and inside ours it raises none.
func chatAutoReviewSupported(provider string) bool {
	key := chatProviderKey(provider)
	return key != "" && (key == chatProviderKey(string(llmproviders.ProviderClaudeCode)) || key == chatProviderKey(string(llmproviders.ProviderCursorCLI)))
}

// chatAccessEffective is the mode a turn of this program on this host gets from the saved one: no whole Mac off a
// personal Mac, and Auto-review only where the program has a reviewer (else This project, never more access).
func chatAccessEffective(saved, provider string, personalMac bool) string {
	if !personalMac {
		return chatAccessProject
	}
	if saved == chatAccessAutoReview && !chatAutoReviewSupported(provider) {
		return chatAccessProject
	}
	return saved
}

// chatAccessSavedMode reads the chat's own saved mode, like chatClaudeCodeTransport: a turn reaches the query path
// from several places, so only the saved tab is trusted, never a value carried on the request.
func chatAccessSavedMode(ctx context.Context, owner, sessionID string) string {
	for _, candidate := range []string{owner, productWorkspaceUserID(ctx)} {
		if tab, found := chatContextTabForSession(ctx, candidate, sessionID); found {
			return tab.Identity.AccessMode
		}
	}
	return chatAccessProject
}

// withChatAccessGrant adds the person's home folder, read and write, for a whole-Mac turn. It goes into the same
// grant list the CLI sandbox, the bridge tools' folder guard and the agent's instructions are built from, so all
// three agree; the project's saved folder list does not change.
func withChatAccessGrant(grants []workflowtypes.WorkflowFolderGrant, mode string) []workflowtypes.WorkflowFolderGrant {
	if chatAccessRank(mode) == 0 {
		return grants
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return grants
	}
	home = filepath.Clean(home)
	for _, grant := range grants {
		if filepath.Clean(grant.Path) == home && grant.CanWrite() {
			return grants
		}
	}
	reason := "This chat's mode is Full access: your whole Mac, no checks."
	if mode == chatAccessAutoReview {
		reason = "This chat's mode is Auto-review: your whole Mac; the coding program's own reviewer approves each action."
	}
	return append(grants, workflowtypes.WorkflowFolderGrant{ID: "chat-access-" + mode, Alias: chatAccessWholeMacAlias, Path: home, Access: workflowtypes.FolderAccessReadWrite, Reason: reason})
}

// chatAccessHasWholeMac reports whether the project's own folders already include the person's home folder with
// write: then every mode already reaches the whole Mac, and the menu says so.
func chatAccessHasWholeMac(grants []workflowtypes.WorkflowFolderGrant) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, grant := range grants {
		if filepath.Clean(grant.Path) == filepath.Clean(home) && grant.CanWrite() {
			return true
		}
	}
	return false
}

// chatAccessOption is one row of the mode menu.
type chatAccessOption struct {
	Mode      string `json:"mode"`
	Available bool   `json:"available"`
	// Why says why a row is off for this chat (its program has no reviewer, or it runs on a server).
	Why string `json:"why,omitempty"`
}

// chatAccessView is the chat's access mode as the menu shows it.
type chatAccessView struct {
	// Mode is the saved choice; Effective is what the next turn gets on this host with this program.
	Mode      string             `json:"mode"`
	Effective string             `json:"effective"`
	Options   []chatAccessOption `json:"options"`
	// OnServer: whole-Mac modes do not exist here; every mode stays inside the chat's folders.
	OnServer bool `json:"on_server,omitempty"`
}

func chatAccessViewFor(saved, provider string, personalMac bool) *chatAccessView {
	view := &chatAccessView{Mode: saved, Effective: chatAccessEffective(saved, provider, personalMac), OnServer: !personalMac}
	view.Options = []chatAccessOption{{Mode: "project", Available: true}}
	auto := chatAccessOption{Mode: chatAccessAutoReview, Available: personalMac && chatAutoReviewSupported(provider)}
	full := chatAccessOption{Mode: chatAccessFull, Available: personalMac}
	switch {
	case !personalMac:
		auto.Why = "On a server every chat stays inside its folders."
		full.Why = auto.Why
	case !auto.Available:
		auto.Why = "This coding app has no reviewer that works here yet. Use Claude Code or Cursor, or pick another mode."
	}
	view.Options = append(view.Options, auto, full)
	if view.Mode == chatAccessProject {
		view.Mode = "project"
	}
	if view.Effective == chatAccessProject {
		view.Effective = "project"
	}
	return view
}

// setChatAccessMode saves a chat's mode. fromTool is a call from an agent's tool: it may lower the chat's access but
// never raise it; more access comes only from the person's own choice in the message box.
func setChatAccessMode(ctx context.Context, owner string, tab chatContextTab, value string, fromTool bool) (chatContextTab, error) {
	mode, err := normalizeChatAccessMode(value)
	if err != nil {
		return tab, err
	}
	if fromTool && chatAccessRank(mode) > chatAccessRank(tab.Identity.AccessMode) {
		log.Printf("[CHAT_ACCESS] owner=%s session=%s action=refused from=%q to=%q reason=tool_raise", owner, tab.Record.SessionID, tab.Identity.AccessMode, mode)
		return tab, fmt.Errorf("only the person can give this chat more access: they pick the mode in the message box")
	}
	identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, chatContextChange{AccessMode: &mode}, owner)
	if err != nil {
		return tab, err
	}
	log.Printf("[CHAT_ACCESS] owner=%s session=%s action=set mode=%q from_tool=%t", owner, tab.Record.SessionID, mode, fromTool)
	tab.Identity = identity
	return tab, nil
}
