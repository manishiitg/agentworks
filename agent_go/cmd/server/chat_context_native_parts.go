package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

const (
	claudeTranscriptBreakdownCacheMaxEntries = 256
	claudeTranscriptAsyncColdBytes           = 1 << 20 // 1 MB: avoid blocking GET /api/chat-context on cold huge transcripts (#4667)
	museBreakdownCacheFreshTTL               = 15 * time.Second
	museBreakdownCacheStaleTTL               = 5 * time.Minute
	museBreakdownCacheMaxEntries             = 256
)

type claudeTranscriptBreakdownEntry struct {
	size        int64
	modTimeNano int64
	breakdown   claudecode.ContextBreakdown
	ok          bool
	lastUsed    time.Time
}

// claudeTranscriptBreakdownCache caches parsed Claude Code transcript breakdowns
// keyed by resolved transcript path and validated against (size, modTime).
type claudeTranscriptBreakdownCache struct {
	mu                   sync.Mutex
	entries              map[string]claudeTranscriptBreakdownEntry
	refreshing           map[string]bool
	maxEntries           int
	staleWhileRevalidate bool
	asyncColdBytes       int64
	parse                func(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool)
}

func parseClaudeTranscriptBreakdown(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool) {
	return claudecode.ContextBreakdownFromTranscript(nativeID, workingDir, accountHome)
}

var defaultClaudeTranscriptBreakdownCache = &claudeTranscriptBreakdownCache{
	maxEntries:           claudeTranscriptBreakdownCacheMaxEntries,
	staleWhileRevalidate: true,
	asyncColdBytes:       claudeTranscriptAsyncColdBytes,
	parse:                parseClaudeTranscriptBreakdown,
}

type museBreakdownCacheEntry struct {
	breakdown  musecli.ContextBreakdown
	ok         bool
	expires    time.Time
	staleUntil time.Time
}

type museBreakdownCache struct {
	mu         sync.Mutex
	entries    map[string]museBreakdownCacheEntry
	refreshing map[string]bool
	freshTTL   time.Duration
	staleTTL   time.Duration
	read       func(nativeID, dataHome string) (musecli.ContextBreakdown, bool)
}

func readMuseContextBreakdown(nativeID, dataHome string) (musecli.ContextBreakdown, bool) {
	return musecli.ReadContextBreakdown(nativeID, dataHome)
}

var defaultMuseBreakdownCache = &museBreakdownCache{
	entries:    map[string]museBreakdownCacheEntry{},
	refreshing: map[string]bool{},
	freshTTL:   museBreakdownCacheFreshTTL,
	staleTTL:   museBreakdownCacheStaleTTL,
	read:       readMuseContextBreakdown,
}

func (c *museBreakdownCache) store(key string, b musecli.ContextBreakdown, ok bool, now time.Time) {
	freshTTL := c.freshTTL
	if freshTTL <= 0 {
		freshTTL = museBreakdownCacheFreshTTL
	}
	staleTTL := c.staleTTL
	if staleTTL <= 0 {
		staleTTL = museBreakdownCacheStaleTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]museBreakdownCacheEntry{}
	}
	for k, e := range c.entries {
		if now.After(e.staleUntil) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= museBreakdownCacheMaxEntries {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = museBreakdownCacheEntry{
		breakdown:  b,
		ok:         ok,
		expires:    now.Add(freshTTL),
		staleUntil: now.Add(staleTTL),
	}
}

func (c *museBreakdownCache) getOrRead(nativeID, dataHome string) (musecli.ContextBreakdown, bool) {
	readFn := c.read
	if readFn == nil {
		readFn = readMuseContextBreakdown
	}
	key := nativeID + "\x00" + dataHome
	now := time.Now()
	c.mu.Lock()
	if entry, found := c.entries[key]; found && now.Before(entry.staleUntil) {
		needsRefresh := !now.Before(entry.expires) && !c.refreshing[key]
		if needsRefresh {
			if c.refreshing == nil {
				c.refreshing = map[string]bool{}
			}
			c.refreshing[key] = true
		}
		c.mu.Unlock()
		if needsRefresh {
			go func() {
				defer func() {
					c.mu.Lock()
					delete(c.refreshing, key)
					c.mu.Unlock()
				}()
				defer recoverDetachedGoroutine("muse_context_breakdown", key)
				b, ok := readFn(nativeID, dataHome)
				c.store(key, b, ok, time.Now())
			}()
		}
		return entry.breakdown, entry.ok
	}
	// On cold miss, coalesce concurrent reads in background so repeated polls don't pile up.
	if c.refreshing == nil {
		c.refreshing = map[string]bool{}
	}
	if !c.refreshing[key] {
		c.refreshing[key] = true
		c.mu.Unlock()
		go func() {
			defer func() {
				c.mu.Lock()
				delete(c.refreshing, key)
				c.mu.Unlock()
			}()
			defer recoverDetachedGoroutine("muse_context_breakdown", key)
			b, ok := readFn(nativeID, dataHome)
			c.store(key, b, ok, time.Now())
		}()
		return musecli.ContextBreakdown{}, false
	}
	c.mu.Unlock()
	return musecli.ContextBreakdown{}, false
}

func (c *claudeTranscriptBreakdownCache) store(transcriptPath string, size, modTimeNano int64, breakdown claudecode.ContextBreakdown, ok bool) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]claudeTranscriptBreakdownEntry{}
	}
	limit := c.maxEntries
	if limit <= 0 {
		limit = claudeTranscriptBreakdownCacheMaxEntries
	}
	if _, exists := c.entries[transcriptPath]; !exists {
		for len(c.entries) >= limit {
			var oldestKey string
			var oldestAt time.Time
			first := true
			for k, v := range c.entries {
				if first || v.lastUsed.Before(oldestAt) {
					oldestKey, oldestAt, first = k, v.lastUsed, false
				}
			}
			if first {
				break
			}
			delete(c.entries, oldestKey)
		}
	}
	c.entries[transcriptPath] = claudeTranscriptBreakdownEntry{
		size:        size,
		modTimeNano: modTimeNano,
		breakdown:   breakdown,
		ok:          ok,
		lastUsed:    now,
	}
}

func (c *claudeTranscriptBreakdownCache) getOrParse(nativeID, workingDir, accountHome string) (claudecode.ContextBreakdown, bool) {
	parse := c.parse
	if parse == nil {
		parse = parseClaudeTranscriptBreakdown
	}
	transcriptPath, err := resolveClaudeNativeTranscriptPath(workingDir, nativeID, accountHome)
	if err != nil || transcriptPath == "" {
		return parse(nativeID, workingDir, accountHome)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil || info.IsDir() {
		return parse(nativeID, workingDir, accountHome)
	}
	size, modTimeNano := info.Size(), info.ModTime().UnixNano()
	now := time.Now()

	c.mu.Lock()
	if entry, found := c.entries[transcriptPath]; found {
		entry.lastUsed = now
		c.entries[transcriptPath] = entry
		if entry.size == size && entry.modTimeNano == modTimeNano {
			c.mu.Unlock()
			return entry.breakdown, entry.ok
		}
		if c.staleWhileRevalidate {
			if c.refreshing == nil {
				c.refreshing = map[string]bool{}
			}
			if !c.refreshing[transcriptPath] {
				c.refreshing[transcriptPath] = true
				go func(path string, sz, mtime int64) {
					defer func() {
						c.mu.Lock()
						delete(c.refreshing, path)
						c.mu.Unlock()
					}()
					defer recoverDetachedGoroutine("claude_transcript_breakdown", path)
					b, ok := parse(nativeID, workingDir, accountHome)
					c.store(path, sz, mtime, b, ok)
				}(transcriptPath, size, modTimeNano)
			}
			c.mu.Unlock()
			return entry.breakdown, entry.ok
		}
	}
	if c.staleWhileRevalidate && c.asyncColdBytes > 0 && size >= c.asyncColdBytes {
		if c.refreshing == nil {
			c.refreshing = map[string]bool{}
		}
		if !c.refreshing[transcriptPath] {
			c.refreshing[transcriptPath] = true
			go func(path string, sz, mtime int64) {
				defer func() {
					c.mu.Lock()
					delete(c.refreshing, path)
					c.mu.Unlock()
				}()
				defer recoverDetachedGoroutine("claude_transcript_breakdown", path)
				b, ok := parse(nativeID, workingDir, accountHome)
				c.store(path, sz, mtime, b, ok)
			}(transcriptPath, size, modTimeNano)
		}
		c.mu.Unlock()
		return claudecode.ContextBreakdown{}, false
	}
	c.mu.Unlock()

	breakdown, ok := parse(nativeID, workingDir, accountHome)
	c.store(transcriptPath, size, modTimeNano, breakdown, ok)
	return breakdown, ok
}

// nativeChatContextParts sizes what fills a Claude Code chat from its own transcript: Claude Code reports only the
// total, so this is the one way to show what the total is made of.
func (api *StreamingAPI) nativeChatContextParts(sessionID string, breakdown nativeBreakdown, usedTokens int) []chatContextPart {
	var ours int
	// Only Claude Code's setup is known to hold the chat's own instructions.
	if sizes, known := api.promptParts.Load(sessionID); known && breakdown.app == claudeCodeApp {
		s := sizes.(mcpagent.PromptPartSizes)
		ours = (s.Instructions + s.Added + s.Runtime) / charsPerToken
	}
	return nativeContextParts(breakdown.ContextBreakdown, breakdown.app, ours, usedTokens)
}

const claudeCodeApp, museApp = "Claude Code", "Muse"

// nativeBreakdown is a chat's breakdown and the app that reported it.
type nativeBreakdown struct {
	claudecode.ContextBreakdown
	app string
}

// nativeChatBreakdown reads what fills a Claude Code chat (its transcript) or a Muse chat (the request trace in its
// session log); false for other coding agents or when it cannot be read.
func (api *StreamingAPI) nativeChatBreakdown(ctx context.Context, owner string, tab chatContextTab) (nativeBreakdown, bool) {
	if nativeID, accountHome, ok := api.nativeChatSession(ctx, owner, tab, "muse-cli"); ok {
		dataHome := ""
		if accountHome != "" {
			dataHome = filepath.Join(accountHome, ".local", "share")
		}
		m, ok := defaultMuseBreakdownCache.getOrRead(nativeID, dataHome)
		if !ok {
			return nativeBreakdown{}, false
		}
		// Muse's trace counts bytes; the parts are scaled to the total the chat reports, so only proportions matter.
		return nativeBreakdown{app: museApp, ContextBreakdown: claudecode.ContextBreakdown{
			Total: m.Total / charsPerToken, Setup: m.Setup / charsPerToken, Skills: m.Skills / charsPerToken,
			AppNotes: m.Context / charsPerToken, Conversation: (m.Messages + m.Replies + m.ToolResults) / charsPerToken,
		}}, true
	}
	nativeID, workingDir, accountHome, ok := api.nativeChatTranscript(ctx, owner, tab)
	if !ok {
		return nativeBreakdown{}, false
	}
	b, ok := defaultClaudeTranscriptBreakdownCache.getOrParse(nativeID, workingDir, accountHome)
	return nativeBreakdown{ContextBreakdown: b, app: claudeCodeApp}, ok
}

// nativeChatTranscript finds a Claude Code chat's own session and where its transcript is kept.
func (api *StreamingAPI) nativeChatTranscript(ctx context.Context, owner string, tab chatContextTab) (nativeID, workingDir, accountHome string, ok bool) {
	nativeID, accountHome, ok = api.nativeChatSession(ctx, owner, tab, "claude-code")
	if !ok {
		return "", "", "", false
	}
	runtime := chatContextRuntimeMetadata(owner, tab.Record)
	return nativeID, strings.TrimSpace(runtime.AgentSessionHandle.Provider.WorkingDir), accountHome, true
}

// nativeChatSession finds a chat's own native session when the chat runs on provider, and the HOME of its account.
func (api *StreamingAPI) nativeChatSession(ctx context.Context, owner string, tab chatContextTab, provider string) (nativeID, accountHome string, ok bool) {
	runtime := chatContextRuntimeMetadata(owner, tab.Record)
	if runtime == nil || runtime.AgentSessionHandle == nil {
		return "", "", false
	}
	handle := runtime.AgentSessionHandle
	if chatProviderKey(firstNonEmptyTrimmed(handle.Provider.Provider, runtime.Provider)) != chatProviderKey(provider) {
		return "", "", false
	}
	nativeID = firstNonEmptyTrimmed(handle.Provider.NativeSessionID, runtime.ExternalSessionID)
	if nativeID == "" {
		return "", "", false
	}
	if handle.ConnectionID != "" {
		scope := providerAccountScope{Principal: owner, WorkspacePath: tab.Record.WorkspacePath}
		if keys, err := api.connectionAPIKeys(ctx, scope, provider, handle.ConnectionID); err == nil && keys != nil {
			accountHome = keys.RuntimeEnvironment["HOME"]
		}
	}
	return nativeID, accountHome, true
}

// chatContextPartItem is one item behind a part of the Context pane's breakdown, with its text.
type chatContextPartItem struct {
	Label  string `json:"label"`
	Text   string `json:"text"`
	Tokens int    `json:"tokens"`
	Count  int    `json:"count,omitempty"`
}

// chatContextPartTexts are the parts whose items a Claude Code chat's transcript holds.
var chatContextPartTexts = map[string]bool{
	claudecode.ContextPartSetup: true, claudecode.ContextPartSkills: true, claudecode.ContextPartFiles: true,
	claudecode.ContextPartResent: true, claudecode.ContextPartSummary: true, claudecode.ContextPartNotes: true,
}

// GET /api/chat-context/part?session_id=…&key=skills lists what one part of the caller's own Claude Code chat is
// made of, with each item's text and rough size, biggest first.
func (api *StreamingAPI) handleChatContextPart(w http.ResponseWriter, r *http.Request) {
	owner := productWorkspaceUserID(r.Context())
	tab, found := chatContextTabForSession(r.Context(), owner, r.URL.Query().Get("session_id"))
	if !found {
		writeAgentProfileError(w, http.StatusNotFound, "Only this chat's owner can see what fills it.")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if !chatContextPartTexts[key] {
		writeAgentProfileError(w, http.StatusBadRequest, "This part has no saved text to show.")
		return
	}
	nativeID, workingDir, accountHome, ok := api.nativeChatTranscript(r.Context(), owner, tab)
	var texts map[string][]claudecode.ContextItem
	if ok {
		texts, ok = claudecode.ContextTextsFromTranscript(nativeID, workingDir, accountHome)
	}
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "This chat's saved log can't be read right now. Send a message and try again.")
		return
	}
	items := make([]chatContextPartItem, 0, len(texts[key]))
	for _, item := range texts[key] {
		items = append(items, chatContextPartItem{Label: item.Label, Text: item.Text, Tokens: max(1, item.Chars/charsPerToken), Count: item.Count})
	}
	out := map[string]any{"items": items}
	if len(items) == 0 && key == claudecode.ContextPartSetup {
		out["note"] = "This Claude Code version doesn't save its own rules and tools in the chat log, so only their size is known."
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// nativeContextParts turns a transcript breakdown into the pane's parts, scaled to the total the chat reports now.
// ours is the size of the instructions AgentWorks put in the setup, when known.
func nativeContextParts(b claudecode.ContextBreakdown, app string, ours, usedTokens int) []chatContextPart {
	if b.Total <= 0 {
		return nil
	}
	scale := func(n int) int {
		if usedTokens <= 0 {
			return n
		}
		return int(int64(n) * int64(usedTokens) / int64(b.Total))
	}
	var parts []chatContextPart
	add := func(key, label string, tokens int, note string) {
		if tokens = scale(tokens); tokens > 0 {
			// Only Claude Code's transcript holds the parts' text.
			parts = append(parts, chatContextPart{Key: key, Label: label, Tokens: tokens, Note: note, Openable: app == claudeCodeApp && chatContextPartTexts[key]})
		}
	}
	setup := b.Setup
	if ours > 0 && ours < setup {
		add("instructions", "Your instructions", ours, "")
		setup -= ours
	}
	add("app_setup", app+"'s own rules and tools", setup, "")
	add("skills", "Skills list", b.Skills, "")
	add("files", "Instruction files (CLAUDE.md, AGENTS.md)", b.InstructionFiles, "")
	if b.ResentCount > 0 {
		note := "Your instructions changed while this chat was open, so the change was added to one of your messages. It stays in this chat until it compacts."
		label := "Instruction updates"
		if b.ResentCount > 1 {
			note = "Your instructions changed while this chat was open, so each change was added to one of your messages. They stay in this chat until it compacts."
			label = "Instruction updates (" + strconv.Itoa(b.ResentCount) + ")"
		}
		add("resent", label, b.Resent, note)
	}
	add("summary", "Summary of the earlier chat", b.Summary, "")
	notes := "Reminders Claude Code adds"
	if app != claudeCodeApp {
		notes = "Context " + app + " adds (memory, workspace notes)"
	}
	add("notes", notes, b.AppNotes, "")
	// The conversation takes what is left, so the parts add up to the reported total.
	target := usedTokens
	if target <= 0 {
		target = b.Total
	}
	rest := target
	for _, part := range parts {
		rest -= part.Tokens
	}
	if rest > 0 {
		parts = append(parts, chatContextPart{Key: "messages", Label: "Conversation", Tokens: rest})
	}
	return parts
}
