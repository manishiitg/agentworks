package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// A chat's mode (#3885, split from access on 26 Nov): how the chat's coding agent works, picked per chat from the
// mode menu in the message box. What it can reach is the project's access (Folders on this Mac), never the chat's
// mode (owner, 26 Nov: "access is at project level.. mode is chat level"). Plan stays the per-message plan switch.
//   - "" (Auto): the agent approves its own actions inside the folders it can reach.
//   - auto_review: the program's own reviewer approves or refuses each action (Claude Code's auto mode, Cursor's
//     Auto-review, Antigravity's Auto mode). Only programs with a working reviewer offer it.
//
// full_access was a whole-Mac mode until 26 Nov. A chat still saved with it runs as Auto; whole Mac is now the
// project's access.
const (
	chatAccessProject    = ""
	chatAccessAutoReview = "auto_review"
	chatAccessLegacyFull = "full_access"
)

func normalizeChatAccessMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "project", "this_project", "auto", "normal", chatAccessLegacyFull, "full-access", "full":
		return chatAccessProject, nil
	case chatAccessAutoReview, "auto-review":
		return chatAccessAutoReview, nil
	}
	return "", fmt.Errorf("mode must be auto or auto_review")
}

// chatAccessRank orders modes by how much the agent may do unchecked: Auto-review has a reviewer, Auto does not.
// A tool may move a chat to a lower rank, never a higher one.
func chatAccessRank(mode string) int {
	if mode == chatAccessAutoReview {
		return 0
	}
	return 1
}

// chatAutoReviewSupported: the programs whose own reviewer was checked to work under AgentWorks' sandbox. Codex's
// reviewer only sees approval requests its own sandbox raises, and inside ours it raises none.
func chatAutoReviewSupported(provider string) bool {
	key := chatProviderKey(provider)
	return key != "" && (key == chatProviderKey(string(llmproviders.ProviderClaudeCode)) || key == chatProviderKey(string(llmproviders.ProviderCursorCLI)) ||
		key == chatProviderKey(string(llmproviders.ProviderAgyCLI))) // agy's own Auto mode (acpcli AutoReviewMode)
}

// chatAccessEffective is the mode a turn of this program on this host gets from the saved one: Auto-review only on a
// personal Mac and where the program has a reviewer (else Auto); a legacy full_access is Auto.
func chatAccessEffective(saved, provider string, personalMac bool) string {
	if saved == chatAccessAutoReview && personalMac && chatAutoReviewSupported(provider) {
		return chatAccessAutoReview
	}
	return chatAccessProject
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
	// OnServer: the chat runs on a server, where Auto-review is not offered yet.
	OnServer bool `json:"on_server,omitempty"`
}

func chatAccessViewFor(saved, provider string, personalMac bool) *chatAccessView {
	view := &chatAccessView{Mode: "project", Effective: "project", OnServer: !personalMac}
	if saved == chatAccessAutoReview {
		view.Mode = chatAccessAutoReview
	}
	if chatAccessEffective(saved, provider, personalMac) == chatAccessAutoReview {
		view.Effective = chatAccessAutoReview
	}
	auto := chatAccessOption{Mode: chatAccessAutoReview, Available: personalMac && chatAutoReviewSupported(provider)}
	switch {
	case !personalMac:
		auto.Why = "Auto-review runs only on your Mac for now."
	case !auto.Available:
		auto.Why = "This coding app has no reviewer that works here yet. Use Claude Code, Cursor or Antigravity."
	}
	view.Options = []chatAccessOption{{Mode: "project", Available: true}, auto}
	return view
}

// setChatAccessMode saves a chat's mode. fromTool is a call from an agent's tool: it may turn Auto-review on but never
// off; dropping the reviewer is only the person's own choice in the message box.
func setChatAccessMode(ctx context.Context, owner string, tab chatContextTab, value string, fromTool bool) (chatContextTab, error) {
	mode, err := normalizeChatAccessMode(value)
	if err != nil {
		return tab, err
	}
	if fromTool && chatAccessRank(mode) > chatAccessRank(tab.Identity.AccessMode) {
		log.Printf("[CHAT_ACCESS] owner=%s session=%s action=refused from=%q to=%q reason=tool_raise", owner, tab.Record.SessionID, tab.Identity.AccessMode, mode)
		return tab, fmt.Errorf("only the person can turn Auto-review off: they pick the mode in the message box")
	}
	identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, chatContextChange{AccessMode: &mode}, owner)
	if err != nil {
		return tab, err
	}
	log.Printf("[CHAT_ACCESS] owner=%s session=%s action=set mode=%q from_tool=%t", owner, tab.Record.SessionID, mode, fromTool)
	tab.Identity = identity
	return tab, nil
}
