package claudecode

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeStatuslineRateLimitWindows reads the rate-limit windows Claude Code
// last wrote to the statusline sidecar for a tmux session.
//
// PLAT-101 ranks reset-time sources, and this is the top one: the CLI states
// `rate_limits.<window>.resets_at` as an exact epoch, whereas the pane prints
// a bare wall clock ("resets 10am") that has to be reconstructed into an
// instant. Both were already being collected — the structured form only ever
// travelled outward to the UI, so the failure path reconstructed a time it did
// not need to.
//
// One-shot and best-effort by design. Unlike readClaudeStatuslineWithWait this
// never polls: the only caller is already holding a usage-limit wall, and
// blocking a failing turn for fifteen seconds to sharpen a timestamp is a bad
// trade. A missing, empty, or unparseable sidecar yields no windows and the
// caller falls back to the next source.
func readClaudeStatuslineRateLimitWindows(sessionName string) ([]llmtypes.RateLimitWindow, bool) {
	if sessionName == "" {
		return nil, false
	}
	// A terminal running the mod: Claude Code's own figures, as of its latest report.
	if windows, ok := claudeModRateLimitWindows(sessionName); ok {
		return windows, true
	}
	raw, err := os.ReadFile(claudeStatuslinePath(sessionName))
	if err != nil {
		return nil, false
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, false
	}
	if _, ok := rawMap["rate_limits"].(map[string]interface{}); !ok {
		return nil, false
	}
	return claudeRateLimitWindows(rawMap), true
}

// claudeSessionAccountKeys maps a tmux session to the hashed identity of the
// credential it runs under.
//
// The account is what a quota belongs to, but the code that reads the
// statusline sidecar only ever knows a session name — the credential lives on
// the adapter. Registering the pairing when the session's statusline is
// configured lets those readers attribute an observation to the right account
// without the credential travelling any further than it already does.
var (
	claudeSessionAccountMu   sync.RWMutex
	claudeSessionAccountKeys = map[string]string{}
)

func rememberClaudeSessionAccount(sessionName, credential string) {
	key := llmtypes.AccountRateLimitKey(credential)
	if sessionName == "" || key == "" {
		return
	}
	claudeSessionAccountMu.Lock()
	claudeSessionAccountKeys[sessionName] = key
	claudeSessionAccountMu.Unlock()
}

func claudeSessionAccountKey(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	claudeSessionAccountMu.RLock()
	defer claudeSessionAccountMu.RUnlock()
	return claudeSessionAccountKeys[sessionName]
}

// recordClaudeSessionRateLimitWindows publishes a session's observation to its
// account, so every other session on the same subscription can read it.
//
// Silently does nothing for a session with no registered credential. An
// unidentified observation cannot be attributed, and attributing it to a shared
// default bucket would let one account's exhaustion gate another's work.
func recordClaudeSessionRateLimitWindows(sessionName string, windows []llmtypes.RateLimitWindow, observedAt time.Time) {
	llmtypes.RecordAccountRateLimitWindows(claudeSessionAccountKey(sessionName), windows, observedAt)
}

// UsageLimitState reports, from the Claude statusline sidecar of the session
// running in tmuxSession, whether the account's plan usage is known and
// whether a rate-limit window is exhausted. Claude writes rate_limits only
// after its first API response, so a fresh pane reports known=false.
func UsageLimitState(tmuxSession string) (known, exhausted bool) {
	known, exhausted, _, _ = claudeStatuslineUsageLimitState(strings.TrimSpace(tmuxSession), time.Now())
	return known, exhausted
}

// StatusLineForTmuxSession returns the statusline Claude Code last wrote for
// the session running in tmuxSession: tokens, cost, and plan usage under
// StatusExtrasMetaKey / RateLimitWindowsMetaKey. It never waits; false when
// Claude has not written one yet (before its first response).
//
// A launch-only turn hands the reply to the caller's transcript streamer, so
// the adapter's own polling loops, which stream this snapshot, never run for
// it. Callers read it here instead (RTS 2026-09-27: no usage on the terminal
// hover for Crew chats).
func StatusLineForTmuxSession(tmuxSession string) (*llmtypes.StatusLine, bool) {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return nil, false
	}
	raw, err := os.ReadFile(claudeStatuslinePath(tmuxSession))
	if err != nil || len(strings.TrimSpace(string(raw))) == 0 {
		return nil, false
	}
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil || status == nil {
		return nil, false
	}
	return status, true
}
