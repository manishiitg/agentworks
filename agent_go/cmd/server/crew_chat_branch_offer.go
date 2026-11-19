package server

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The own-copy offer (#2922). By default a chat edits the shared folder and
// each reply's card restores only the files that reply changed. When other
// chats are working in the same Crew, the chat's agent is told once; before
// its first edit in a Git repository it checks that repository's status and,
// when it has edits not committed yet, asks the person whether to work in its
// own copy (start_chat_branch) or keep working here.

// chatBranchOffered: chats already told, at most once per chat per server run.
var chatBranchOffered sync.Map

const chatBranchOfferRecent = 10 * time.Minute

// otherChatsWorking lists the titles of other chats of userID in the same
// workspace that are running a turn or did real work in the last few minutes.
// Helpers started by this chat do not count.
func (api *StreamingAPI) otherChatsWorking(userID, sessionID, workspacePath string, now time.Time) []string {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	var titles []string
	for id, s := range api.activeSessions {
		if s == nil || id == sessionID || s.ParentSessionID == sessionID || s.UserID != userID {
			continue
		}
		if !workspacePathsMatchForUser(userID, s.WorkspacePath, workspacePath) {
			continue
		}
		if s.Status != "running" && now.Sub(s.LastWorkedAt) > chatBranchOfferRecent {
			continue
		}
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = "an untitled chat"
		}
		titles = append(titles, fmt.Sprintf("%q", title))
	}
	sort.Strings(titles)
	return titles
}

// takeChatBranchOffer returns, once per chat, the line that tells its agent
// other chats are working here; "" when none are, or the chat already has its
// own copy.
func (api *StreamingAPI) takeChatBranchOffer(roots crewroot.Roots, userID, sessionID, workspacePath, chatKey string) string {
	if chatKey == "" || len(chatBranchesFor(roots, chatKey)) > 0 {
		return ""
	}
	if _, told := chatBranchOffered.Load(chatKey); told {
		return ""
	}
	others := api.otherChatsWorking(userID, sessionID, workspacePath, time.Now())
	if len(others) == 0 {
		return ""
	}
	if _, told := chatBranchOffered.LoadOrStore(chatKey, true); told {
		return ""
	}
	if len(others) > 3 {
		others = append(others[:3], fmt.Sprintf("%d more", len(others)-3))
	}
	log.Printf("[CHAT_BRANCH] offer chat=%s others=%d", chatKey, len(others))
	return "[Other chats are working in this Crew right now: " + strings.Join(others, ", ") + ". Before your first edit in a Git repository, run git status there. If it has edits not committed yet, ask the person once, with two choices: \"Another chat is editing this project. Work in its own copy?\" -> \"Work in its own copy\" or \"Keep working here\". If they pick their own copy, call start_chat_branch for that repository and work only in the folder it returns; otherwise edit in place and do not ask again in this chat.]"
}
