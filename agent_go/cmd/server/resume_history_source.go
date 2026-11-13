package server

import (
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Only source identity and resume configuration live here, never another parsed
// history. The one parsed copy remains conversationHistory, under conversationMux.
type resumeHistorySource struct {
	user, workspace, path string
	header, log           os.FileInfo
	target                resolvedResumeTarget
}

func statResumeHistory(user, workspace, path string) *resumeHistorySource {
	local := resolvedChatHistoryPath(chatHistoryStoragePath(path))
	header, err := os.Stat(local)
	if err != nil || !header.Mode().IsRegular() {
		return nil
	}
	var log os.FileInfo
	if chatlog.IsConversationPath(local) {
		log, err = os.Stat(chatlog.HistoryLogPath(local))
		if err != nil && !os.IsNotExist(err) {
			return nil
		}
	}
	return &resumeHistorySource{user: user, workspace: workspace, path: path, header: header, log: log}
}
func sameResumeFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func (a *resumeHistorySource) same(b *resumeHistorySource) bool {
	return a != nil && b != nil && a.user == b.user && a.workspace == b.workspace && a.path == b.path && sameResumeFile(a.header, b.header) && sameResumeFile(a.log, b.log)
}

func (api *StreamingAPI) reusableProductResumeTarget(user, workspace, session string) (*resolvedResumeTarget, bool) {
	if api == nil {
		return nil, false
	}
	api.conversationMux.RLock()
	source := api.resumeHistorySources[session]
	history, loaded := api.conversationHistory[session]
	api.conversationMux.RUnlock()
	if source == nil || !loaded || source.user != user || source.workspace != workspace {
		return nil, false
	}
	if !source.same(statResumeHistory(user, workspace, source.path)) {
		return nil, false
	}
	// Copy the slice header and small configuration only. Existing readers do not
	// mutate content parts; each runner takes its own history slice before use.
	target := source.target
	target.History = history
	target.source = source
	return &target, true
}

// Called only with conversationMux held. A source stamp is recorded only for
// the exact parse installed as this session's canonical history.
func (api *StreamingAPI) rememberResumeHistorySourceLocked(session string, target *resolvedResumeTarget) {
	if target.source == nil {
		return
	}
	source := *target.source
	source.target = *target
	source.target.History = nil
	source.target.source = nil
	if api.resumeHistorySources == nil {
		api.resumeHistorySources = map[string]*resumeHistorySource{}
	}
	api.resumeHistorySources[session] = &source
}

// Our successful full snapshot write already has the parsed history. Publish
// that exact version, rather than forcing the next turn to parse our own write.
func (api *StreamingAPI) rememberWrittenResumeHistory(user, session, path string, history []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime) {
	if api == nil {
		return
	}
	api.conversationMux.Lock()
	defer api.conversationMux.Unlock()
	old := api.resumeHistorySources[session]
	if old == nil || old.user != user || strings.TrimSpace(old.path) != strings.TrimSpace(path) {
		return
	}
	next := statResumeHistory(user, old.workspace, path)
	if next == nil {
		return
	}
	next.target = old.target
	next.target.Runtime = runtime
	api.conversationHistory[session] = history
	api.resumeHistorySources[session] = next
}
