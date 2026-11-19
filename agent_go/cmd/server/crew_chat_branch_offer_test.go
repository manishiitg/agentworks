package server

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #2922: a chat is offered its own copy only while another chat of the same
// person works in the same Crew, and only once; its own helpers do not count.
func TestChatBranchOfferOnlyWhenAnotherChatWorksAndOnce(t *testing.T) {
	base := t.TempDir()
	roots := crewroot.Roots{Files: filepath.Join(base, "files"), Private: filepath.Join(base, "private")}
	api := &StreamingAPI{activeSessions: map[string]*ActiveSessionInfo{}}
	const ws = "Crew/example"
	api.activeSessions["mine"] = &ActiveSessionInfo{SessionID: "mine", UserID: "u1", WorkspacePath: ws, Status: "running"}
	api.activeSessions["helper"] = &ActiveSessionInfo{SessionID: "helper", ParentSessionID: "mine", UserID: "u1", WorkspacePath: ws, Status: "running"}
	api.activeSessions["idle"] = &ActiveSessionInfo{SessionID: "idle", UserID: "u1", WorkspacePath: ws, Status: "completed", LastWorkedAt: time.Now().Add(-time.Hour)}
	api.activeSessions["elsewhere"] = &ActiveSessionInfo{SessionID: "elsewhere", UserID: "u1", WorkspacePath: "Crew/other", Status: "running"}
	api.activeSessions["someone"] = &ActiveSessionInfo{SessionID: "someone", UserID: "u2", WorkspacePath: ws, Status: "running"}
	if line := api.takeChatBranchOffer(roots, "u1", "mine", ws, "chat-offer-a"); line != "" {
		t.Fatalf("offered with no other chat working: %s", line)
	}
	api.activeSessions["other"] = &ActiveSessionInfo{SessionID: "other", UserID: "u1", WorkspacePath: ws, Status: "running", Title: "Fix login"}
	line := api.takeChatBranchOffer(roots, "u1", "mine", ws, "chat-offer-a")
	if !strings.Contains(line, `"Fix login"`) || !strings.Contains(line, "start_chat_branch") {
		t.Fatalf("offer: %q", line)
	}
	if again := api.takeChatBranchOffer(roots, "u1", "mine", ws, "chat-offer-a"); again != "" {
		t.Fatalf("offered twice: %s", again)
	}
}
