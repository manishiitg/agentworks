package browserrelay

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type diagnostic struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type tabDiagnostics struct {
	Messages []diagnostic
	Errors   []diagnostic
}

// Called with b.mu held. Extension events contain only authorized shared tabs.
func (b *Binding) collectDiagnostics(raw json.RawMessage) {
	var event struct {
		Method    string `json:"method"`
		SessionID string `json:"sessionId"`
		Params    struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
			Args      []struct {
				Value               json.RawMessage `json:"value"`
				Description         string          `json:"description"`
				UnserializableValue string          `json:"unserializableValue"`
			} `json:"args"`
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		} `json:"params"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	target := b.childTargets[event.SessionID]
	if target == "" && strings.HasPrefix(event.SessionID, "session-") {
		target = "tab-" + strings.TrimPrefix(event.SessionID, "session-")
	}
	if event.Method == "Target.attachedToTarget" && target != "" {
		if b.childTargets == nil {
			b.childTargets = map[string]string{}
		}
		if len(b.childTargets) < 512 {
			b.childTargets[event.Params.SessionID] = target
		}
		return
	}
	if event.Method == "Target.detachedFromTarget" {
		delete(b.childTargets, event.Params.SessionID)
		return
	}
	if event.Method == "Target.targetDestroyed" {
		delete(b.diagnostics, event.Params.TargetID)
		for id, root := range b.childTargets {
			if root == event.Params.TargetID {
				delete(b.childTargets, id)
			}
		}
		return
	}
	if target == "" || (event.Method != "Runtime.consoleAPICalled" && event.Method != "Runtime.exceptionThrown") {
		return
	}
	if b.diagnostics == nil {
		b.diagnostics = map[string]*tabDiagnostics{}
	}
	logs := b.diagnostics[target]
	if logs == nil {
		if len(b.diagnostics) >= 32 {
			return
		}
		logs = &tabDiagnostics{}
		b.diagnostics[target] = logs
	}
	entry := diagnostic{Type: event.Params.Type}
	if event.Method == "Runtime.consoleAPICalled" {
		parts := []string{}
		for _, arg := range event.Params.Args {
			part := arg.Description
			if len(arg.Value) > 0 {
				var text string
				if json.Unmarshal(arg.Value, &text) == nil {
					part = text
				} else {
					part = string(arg.Value)
				}
			} else if arg.UnserializableValue != "" {
				part = arg.UnserializableValue
			}
			parts = append(parts, part)
		}
		entry.Text = strings.Join(parts, " ")
	} else {
		entry.Type = "error"
		entry.Text = event.Params.ExceptionDetails.Exception.Description
		if entry.Text == "" {
			entry.Text = event.Params.ExceptionDetails.Text
		}
	}
	if len(entry.Text) > 2048 {
		entry.Text = entry.Text[:2048]
	}
	if event.Method == "Runtime.consoleAPICalled" {
		logs.Messages = appendDiagnostic(logs.Messages, entry)
	} else {
		logs.Errors = appendDiagnostic(logs.Errors, entry)
	}
}

func appendDiagnostic(entries []diagnostic, entry diagnostic) []diagnostic {
	entries = append(entries, entry)
	if len(entries) > 100 {
		entries = entries[len(entries)-100:]
	}
	return entries
}

// Diagnostics replaces the CLI's connection-wide cache with per-shared-tab logs.
// The caller holds the binding's controller gate and supplies the verified CLI target.
func (b *Binding) Diagnostics(target, command string, clear bool) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extension == nil || time.Now().After(b.expires) {
		return nil, fmt.Errorf("CHROME_EXTENSION_DISCONNECTED: reconnect the browser")
	}
	logs := b.diagnostics[target]
	entries := []diagnostic{}
	key := "messages"
	if command == "errors" {
		key = "errors"
	}
	if logs != nil {
		if command == "errors" {
			entries = append(entries, logs.Errors...)
			if clear {
				logs.Errors = nil
			}
		} else {
			entries = append(entries, logs.Messages...)
			if clear {
				logs.Messages = nil
			}
		}
	}
	if clear {
		return map[string]interface{}{"cleared": true, "scope": "tab"}, nil
	}
	return map[string]interface{}{key: entries, "scope": "tab"}, nil
}
