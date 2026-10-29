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

// nativeUsageSnapshot is a terminal's status as the browser sees it. Plan limits are left out of every terminal:
// the chat's usage chip (/api/chat-context) is the one place that shows them. The main agent's terminal carries
// the chat's context figures from the one reader, chatUsage, so after a provider switch it no longer shows the
// previous provider's numbers; other terminals keep what their own CLI reported.
func (api *StreamingAPI) nativeUsageSnapshot(s terminals.Snapshot) terminals.Snapshot {
	main := terminals.IsMainAgentTerminal(s) && strings.TrimSpace(s.SessionID) != ""
	if len(s.Status.StatusMeta) == 0 && !main {
		return s
	}
	meta := map[string]interface{}{}
	if raw, err := json.Marshal(s.Status.StatusMeta); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	stripNativeAccountFields(meta, true, false)
	if main {
		delete(meta, "context_used_tokens")
		delete(meta, "context_window_tokens")
		delete(meta, "context_usage_percent")
		if extras, ok := meta["status_extras"].([]interface{}); ok {
			var kept []interface{}
			for _, item := range extras {
				if text, ok := item.(string); !ok || !strings.HasPrefix(strings.TrimSpace(text), "ctx ") {
					kept = append(kept, item)
				}
			}
			meta["status_extras"] = kept
		}
		used, window, _ := api.chatUsage(s.SessionID, api.chatUsageSubjectForSession(s.SessionID), false)
		if used > 0 {
			meta["context_used_tokens"] = used
		}
		if window > 0 {
			meta["context_window_tokens"] = window
		}
	}
	s.Status.StatusMeta = meta
	return s
}
