package server

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// compactionPreferences are a person's two compaction switches. They apply to every chat the person owns, in
// every Crew (owner, 4 Nov: "full user setting.. it should apply for all his chats"), and are changed from any
// chat's menu, its model tool or MCP.
type compactionPreferences struct {
	// IdleOff stops "Auto-compact when idle" for the person's chats; it is on unless they turn it off.
	IdleOff bool `json:"idle_compaction_off,omitempty"`
	// EarlyOff stops "Compact at 300K" (compacting a chat as soon as a reply ends over compactEarlyTokens); it is on
	// unless they turn it off (owner, 10 Nov: a Claude chat had grown to 450k and every step re-read it).
	EarlyOff bool `json:"compact_early_off,omitempty"`
}

// compactEarly is whether "Compact at 300K" is on for the person.
func (p compactionPreferences) compactEarly() bool { return !p.EarlyOff }

var compactionPreferencesMu sync.Mutex

func compactionPreferencesPath(user string) string {
	return filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "compaction.json"))
}

// loadCompactionPreferences reads a person's switches; a missing or unreadable file gives the defaults
// (idle compaction on, compact at 300K off).
func loadCompactionPreferences(ctx context.Context, user string) compactionPreferences {
	var pref compactionPreferences
	if user == "" {
		return pref
	}
	raw, exists, err := readFileFromWorkspace(ctx, compactionPreferencesPath(user))
	if err == nil && exists {
		_ = json.Unmarshal([]byte(raw), &pref)
	}
	return pref
}

// saveCompactionPreferences changes the given switches (nil keeps one) for all of the person's chats.
func saveCompactionPreferences(ctx context.Context, user string, idle, early *bool) (compactionPreferences, error) {
	compactionPreferencesMu.Lock()
	defer compactionPreferencesMu.Unlock()
	pref := loadCompactionPreferences(ctx, user)
	if idle != nil {
		pref.IdleOff = !*idle
	}
	if early != nil {
		pref.EarlyOff = !*early
	}
	raw, _ := json.Marshal(pref)
	if err := writeRawFileToWorkspace(ctx, compactionPreferencesPath(user), string(raw)); err != nil {
		return pref, err
	}
	log.Printf("[CHAT_CONTEXT] %s set compaction for all their chats idle_compaction=%t compact_early=%t", user, !pref.IdleOff, pref.compactEarly())
	return pref, nil
}
