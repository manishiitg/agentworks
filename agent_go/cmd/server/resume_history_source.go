package server

import (
	"os"
	"reflect"
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
	persistedBody         int
	persistedTurns        int
	prefixStable          bool
	persistedSystem       bool
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
	if target.Base > 0 {
		// A window was read from this exact file version, so its rows are the
		// file's last rows: the next save appends after them. The full writer
		// would instead re-read and re-merge the whole file.
		source.persistedBody = len(chatHistoryBody(target.History))
		source.persistedSystem = len(target.History) > 0 && target.History[0].Role == llmtypes.ChatMessageTypeSystem
		source.persistedTurns = target.BaseTurns + visibleResumeUserCount(target.History)
		source.prefixStable = source.log != nil && appendableHistoryShape(target.History)
	}
	if api.resumeHistorySources == nil {
		api.resumeHistorySources = map[string]*resumeHistorySource{}
	}
	api.resumeHistorySources[session] = &source
}

// forgetResumeWindow marks the session's in-memory history as complete.
func (api *StreamingAPI) forgetResumeWindow(session string) {
	if api == nil {
		return
	}
	api.conversationMux.Lock()
	defer api.conversationMux.Unlock()
	if source := api.resumeHistorySources[session]; source != nil && source.target.Base > 0 {
		next := *source
		next.target.Base, next.target.BaseTurns = 0, 0
		api.resumeHistorySources[session] = &next
	}
}

// resumeWindowBase is how many saved messages come before the session's
// in-memory history (0: memory holds all of it).
func (api *StreamingAPI) resumeWindowBase(session string) int {
	if api == nil {
		return 0
	}
	api.conversationMux.RLock()
	defer api.conversationMux.RUnlock()
	if source := api.resumeHistorySources[session]; source != nil {
		return source.target.Base
	}
	return 0
}

// Our successful full snapshot write already has the parsed history. Publish
// that exact version, rather than forcing the next turn to parse our own write.
func (api *StreamingAPI) rememberWrittenResumeHistory(user, session, path string, history []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime, resumeTurns ...int) {
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
	next.persistedBody = len(chatHistoryBody(history))
	next.persistedSystem = len(history) > 0 && history[0].Role == llmtypes.ChatMessageTypeSystem
	if len(resumeTurns) > 0 {
		next.persistedTurns = resumeTurns[0]
	} else {
		next.persistedTurns = visibleResumeUserCount(history)
	}
	next.prefixStable = next.log != nil && appendableHistoryShape(history)
	api.conversationHistory[session] = history
	api.resumeHistorySources[session] = next
}

// rememberLiveInputAppend advances the saved tail-append proof after
// appendLiveInputToPersistedChatHistory appends a single user message directly
// to the conversation log, so the turn's completion save does not fall back to
// a full-file rewrite with fallback=file_changed.
func (api *StreamingAPI) rememberLiveInputAppend(user, session, path string, before *resumeHistorySource, message llmtypes.MessageContent) {
	if api == nil || before == nil {
		return
	}
	api.conversationMux.Lock()
	defer api.conversationMux.Unlock()
	old := api.resumeHistorySources[session]
	if old == nil || old.user != user || strings.TrimSpace(old.path) != strings.TrimSpace(path) || !old.prefixStable || old.log == nil {
		return
	}
	if !sameResumeFile(old.log, before.log) {
		return
	}
	next := statResumeHistory(user, old.workspace, path)
	if next == nil || next.log == nil {
		return
	}
	current := api.conversationHistory[session]
	body := chatHistoryBody(current)
	var nextHistory []llmtypes.MessageContent
	switch {
	case len(body) == old.persistedBody:
		nextHistory = append(append([]llmtypes.MessageContent(nil), current...), message)
	case len(body) > old.persistedBody && reflect.DeepEqual(body[old.persistedBody], message):
		nextHistory = current
	default:
		return
	}
	next.target = old.target
	next.persistedBody = old.persistedBody + 1
	next.persistedSystem = old.persistedSystem
	next.persistedTurns = old.persistedTurns + visibleResumeUserCount([]llmtypes.MessageContent{message})
	next.prefixStable = appendableHistoryShape(nextHistory)
	api.conversationHistory[session] = nextHistory
	api.resumeHistorySources[session] = next
}

// Server history snapshots are immutable message values. Check the previously
// saved prefix at each replacement, before dropping its canonical slice. Keep
// only counts/proof, never another slice of old message bodies.
func (api *StreamingAPI) replaceConversationHistoryLocked(session string, next []llmtypes.MessageContent) {
	if source := api.resumeHistorySources[session]; source != nil && source.prefixStable {
		oldBody, newBody := chatHistoryBody(api.conversationHistory[session]), chatHistoryBody(next)
		n := source.persistedBody
		if source.persistedSystem && (len(next) == 0 || next[0].Role != llmtypes.ChatMessageTypeSystem) || !appendableHistoryShape(next) || len(oldBody) < n || len(newBody) < n || !samePersistedPrefix(oldBody, newBody, n) {
			invalid := *source
			invalid.prefixStable = false
			api.resumeHistorySources[session] = &invalid
		}
	}
	api.conversationHistory[session] = next
}
func chatHistoryBody(history []llmtypes.MessageContent) []llmtypes.MessageContent {
	for len(history) > 0 && history[0].Role == llmtypes.ChatMessageTypeSystem {
		history = history[1:]
	}
	return history
}

func appendableHistoryShape(history []llmtypes.MessageContent) bool {
	body := chatHistoryBody(history)
	if len(history)-len(body) > 1 {
		return false
	}
	for _, message := range body {
		if message.Role == llmtypes.ChatMessageTypeSystem {
			return false
		}
	}
	return true
}

func samePersistedPrefix(old, next []llmtypes.MessageContent, n int) bool {
	if len(old) < n || len(next) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if old[i].Role != next[i].Role || len(old[i].Parts) != len(next[i].Parts) {
			return false
		}
		// Wrapper snapshots copy message structs, not immutable content parts.
		// Shared parts need no reflective walk, JSON encoding or key allocation.
		if len(old[i].Parts) > 0 && &old[i].Parts[0] == &next[i].Parts[0] {
			continue
		}
		if !reflect.DeepEqual(old[i], next[i]) {
			return false
		}
	}
	return true
}
