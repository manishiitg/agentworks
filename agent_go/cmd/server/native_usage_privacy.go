package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func (api *StreamingAPI) nativeUsageOwner(ctx context.Context, session string) bool {
	user := GetUserIDFromContext(ctx)
	if user == "" || api.eventStore == nil || api.eventStore.GetSessionOwner(session) != user {
		return false
	}
	cfg := common.GetSessionShellConfig(session)
	return cfg == nil || !cfg.ReadOnlyAccess
}

// Native plan windows belong to the account's own chat. Context usage remains
// useful project state; a shared tab/dashboard must not inherit account limits.
func stripNativeAccountFields(value interface{}, usage, statusLine bool) {
	switch v := value.(type) {
	case map[string]interface{}:
		if kind, _ := v["type"].(string); kind == "status_line" {
			usage = true
			statusLine = true
		} else if kind == "token_usage" || kind == "token_count" || kind == "llm_generation_end" {
			usage = true
		}
		if chunk, _ := v["chunk_type"].(string); chunk == "status_line" {
			usage = true
			statusLine = true
		}
		for key, child := range v {
			if usage {
				switch strings.ToLower(key) {
				case "rate_limits", "rate_limit_windows", "limit_id", "plan_type", "plan", "credits", "usage_limit", "usage_limits":
					delete(v, key)
					continue
				case "status_extras":
					items, _ := child.([]interface{})
					var contextOnly []interface{}
					for _, item := range items {
						if text, ok := item.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "ctx ") {
							contextOnly = append(contextOnly, text)
						}
					}
					v[key] = contextOnly
					continue
				}
				if statusLine && (key == "text" || key == "line" || key == "raw" || key == "raw_map") {
					delete(v, key)
					continue
				}
			}
			stripNativeAccountFields(child, usage || key == "status_meta" || key == "generation_info", statusLine)
		}
	case []interface{}:
		for _, child := range v {
			stripNativeAccountFields(child, usage, statusLine)
		}
	}
}

func (api *StreamingAPI) nativeUsageResponse(r *http.Request, session string, value interface{}) interface{} {
	if api.nativeUsageOwner(r.Context(), session) {
		return value
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var copy interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&copy) != nil {
		return value
	}
	stripNativeAccountFields(copy, false, false)
	return copy
}

func (api *StreamingAPI) nativeUsageSnapshot(ctx context.Context, s terminals.Snapshot) terminals.Snapshot {
	meta := map[string]interface{}{}
	for key, v := range s.Status.StatusMeta {
		meta[key] = v
	}
	api.nativeCostsMu.Lock()
	a := api.nativeCosts[s.SessionID]
	api.nativeCostsMu.Unlock()
	if a != nil {
		a.mu.Lock()
		usage := a.usage
		a.mu.Unlock()
		if usage.ContextUsed > 0 {
			meta["context_used_tokens"] = usage.ContextUsed
		}
		if usage.ContextWindow > 0 {
			meta["context_window_tokens"] = usage.ContextWindow
		}
		if api.nativeUsageOwner(ctx, s.SessionID) && len(usage.Limits) > 0 {
			var windows []interface{}
			for _, limit := range usage.Limits {
				windows = append(windows, map[string]interface{}{"name": limit.Name, "used_percent": limit.UsedPercent, "window_minutes": limit.WindowMinutes, "resets_at": limit.ResetsAt})
			}
			meta["rate_limit_windows"] = windows
		}
	}
	if !api.nativeUsageOwner(ctx, s.SessionID) {
		raw, err := json.Marshal(meta)
		if err == nil {
			var copy map[string]interface{}
			if json.Unmarshal(raw, &copy) == nil {
				stripNativeAccountFields(copy, true, false)
				meta = copy
			}
		}
	}
	s.Status.StatusMeta = meta
	return s
}
