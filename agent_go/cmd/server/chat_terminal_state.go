package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const chatTerminalNotice = "This chat is open in your terminal; attach it back to continue here, or choose Continue here instead."

// One private receipt, not a lease. Recovery supersedes the receipt permanently;
// an old terminal cannot overwrite a chat continued here after recovery.
type chatTerminalState struct {
	Revision      int64  `json:"revision"`
	MirrorProject string `json:"mirror_project,omitempty"`
	JournalBytes  int64  `json:"journal_bytes"`
	JournalSHA    string `json:"journal_sha"`
	Owner         string `json:"owner"`
	Chat          string `json:"chat"`
	Session       string `json:"session"`
	Native        string `json:"native"`
	Receipt       string `json:"receipt"`
	Out           bool   `json:"out"`
}

func chatTerminalPath(session string) (string, error) {
	if session == "" {
		return "", fmt.Errorf("choose a chat")
	}
	root, e := crewroot.StateRoot()
	if e != nil {
		return "", e
	}
	return filepath.Join(root, "chat-terminal", crewTransferDigest(session)+".json"), nil
}

func readChatTerminal(session string) (chatTerminalState, error) {
	var s chatTerminalState
	p, e := chatTerminalPath(session)
	if e != nil {
		return s, e
	}
	data, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(data, &s); e != nil {
		return s, e
	}
	if s.Session != session {
		return s, fmt.Errorf("invalid terminal receipt")
	}
	return s, nil
}

func saveChatTerminal(s chatTerminalState) error {
	p, e := chatTerminalPath(s.Session)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	return fsutil.WriteJSONAtomic(p, s, 0600)
}

func chatTerminalTurnRefusal(session string) string {
	s, e := readChatTerminal(session)
	if e != nil {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=receipt_unreadable", session, session)
		return "This chat's terminal handoff could not be checked. Use Continue here instead to recover it."
	}
	if !s.Out {
		return ""
	}
	log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=continued_in_terminal", s.Chat, session)
	return chatTerminalNotice
}

func (api *StreamingAPI) chatTerminalWorkRunning(session string) bool {
	if api.crewAgentTurnRunning(session) || api.bgAgentRegistry != nil && api.bgAgentRegistry.HasRunningAgents(session) {
		return true
	}
	status := api.sessionDisplayStatus(session)
	return status.HasRunningBackgroundAgents || status.Status == "running" || status.Status == "paused"
}
