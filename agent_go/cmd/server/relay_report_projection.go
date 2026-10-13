package server

import (
	"encoding/json"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func validRelayReportRunID(runID string) bool {
	return runID != "" && runID != "." && runID != ".." && len(runID) <= 200 && !strings.ContainsAny(runID, "/\\")
}

func relayReportInput(root *os.Root, guard *wf.FolderGuard, runID string) (string, any) {
	if !validRelayReportRunID(runID) {
		return "", nil
	}
	file := path.Join("webhooks/deliveries", runID+".json")
	if guard != nil && !guard.Allows(file, false) {
		return "", nil
	}
	var delivery WorkflowWebhookDelivery
	var payload struct {
		Caller string         `json:"relay_caller"`
		Args   map[string]any `json:"args"`
	}
	if !readReportRelayJSON(root, file, &delivery) || json.Unmarshal(delivery.Payload, &payload) != nil {
		return "", nil
	}
	return relayReportText(payload.Caller), payload.Args["INPUT"]
}

func relayReportText(value string) string {
	value = redaction.Text(value)
	if len(value) <= 4096 {
		return value
	}
	end := 4096
	for !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "… [truncated]"
}

// Keep product results and step outputs, not the trace's prompts, credentials,
// agent-call transcripts or arbitrary files. The same projection feeds Ask/UI.
func relayReportValue(value any) any {
	value = redactRelayReportValue(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"unavailable": true}
	}
	if len(encoded) > 16<<10 {
		return map[string]any{"truncated": true, "recorded_bytes": len(encoded)}
	}
	return value
}

func redactRelayReportValue(value any) any {
	switch v := value.(type) {
	case string:
		redacted := redaction.Text(v)
		if len(redacted) > 4096 {
			return map[string]any{"truncated": true, "recorded_bytes": len(v), "text_excerpt": relayReportText(redacted)}
		}
		return redacted
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redactRelayReportValue(child)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			name := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
			sensitive := name == "authorization" || name == "cookie" || name == "setcookie" || name == "credentials"
			for _, suffix := range []string{"password", "passwd", "secret", "apikey", "token", "privatekey"} {
				sensitive = sensitive || strings.HasSuffix(name, suffix)
			}
			if sensitive {
				out[key] = "[redacted]"
			} else {
				out[key] = redactRelayReportValue(child)
			}
		}
		return out
	default:
		return value
	}
}
