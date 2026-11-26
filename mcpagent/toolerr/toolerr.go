// Package toolerr holds the one definition of "this tool result looks like a
// failure", shared by the in-process agent loop and the HTTP bridge handlers.
//
// It lives outside both because the masked-failure class appears on both paths
// and a second copy would drift silently — the same trap documented for event
// ownership and the retention lists.
package toolerr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tool-failure logging markers.
//
// Two markers, one prefix. `grep '\[TOOL_ERROR'` returns everything; the
// suffixed forms let you separate what is certain from what is guessed.
//
//	[TOOL_ERROR]         the tool reported failure — err != nil, or IsError set
//	[TOOL_ERROR_SUSPECT] the transport reported success but the payload carries
//	                     a structured failure fact
//
// The second exists because reported success is not evidence of success. It is
// intentionally field/envelope-aware: arbitrary stdout and prose belong to the
// agent's semantic interpretation and must not become platform errors merely
// because they contain words such as "failed", "not found", or "forbidden".
const (
	Marker        = "[TOOL_ERROR]"
	SuspectMarker = "[TOOL_ERROR_SUSPECT]"
)

var httpFailurePrefix = regexp.MustCompile(`(?i)^HTTP(?:/\S+)?\s+([45][0-9]{2})(?:\s|$)`)

// jsonEnvelopeFields are the field names whose string content may itself be a
// nested transport envelope worth JSON-decoding and inspecting recursively —
// how MCP bridge results typically arrive (JSON stringified inside a
// content/text block). stdout/stderr are deliberately excluded: they carry a
// tool's own arbitrary output, not a nested envelope. Confirmed live: `cat`
// on a file whose CONTENT was a captured API error response
// ({"success":false,"error":...}) was misclassified as the shell command
// itself failing, via this exact decode-and-recurse step, even though its
// real exit code was 0. Their plain TEXT is still checked against the prefix
// patterns below — an "ERROR: ..." or "tool execution failed: ..." message
// is a genuine self-reported failure — only the JSON-decode-and-recurse step
// into arbitrary nested structure is skipped for these two fields.
var jsonEnvelopeFields = map[string]bool{
	"":        true, // the top-level call, before any field is known
	"content": true,
	"text":    true,
	"result":  true,
	"error":   true,
}

// CanonicalFailure recognizes only payload signals strong enough to change the
// runtime result from success to error. The same structural contract is used by
// Suspicious logging so observability cannot contradict the result sent to the
// LLM.
//
// Bridge results are often JSON nested inside MCP content/text strings, so the
// classifier unwraps those envelopes recursively. Plain prose that merely
// mentions an error remains successful.
func CanonicalFailure(resultText string) (string, bool) {
	return canonicalFailureValue(strings.TrimSpace(resultText), "", 0)
}

// CanonicalFailureForTool suppresses payload promotion for tools whose normal
// job is to return arbitrary problem records or documentation. Their genuine
// executor failures already carry err/IsError; inspecting domain rows such as
// {"status":"failed"} would confuse data with transport state.
func CanonicalFailureForTool(toolName, resultText string) (string, bool) {
	if problemReportingTools[strings.TrimSpace(toolName)] {
		return "", false
	}
	return CanonicalFailure(resultText)
}

// toolNameFromEnvelope matches the one harness-generated error shape observed
// across every unattributed [TOOL_ERROR] marker in the 2026-08-04 scan:
// "... tool execution failed: layer=custom_tool_handler tool=record_pulse_worklist
// session=...". It is deliberately this narrow, not a generic "tool=" scanner,
// because a broader pattern risks matching an unrelated tool name mentioned
// inside a nested envelope and misattributing the marker to it — the transport
// wrapper's own name (what actually failed to relay the call) must win unless
// this exact, unambiguous harness phrase is present.
var toolNameFromEnvelope = regexp.MustCompile(`tool execution failed: layer=\S+ tool=(\S+)`)

// ToolNameFromResult recovers the tool name from a result payload when the
// stream chunk carrying it arrived with an empty name.
//
// This is the last-resort fallback, tried only after the structured
// ToolCallID correlation (matching this end event back to its start event)
// has already failed. On 2026-08-04, 35 of 90 sampled
// "[TOOL_ERROR] CLI tool payload failure" markers carried tool="" — the name
// was only recoverable by regexing the same nested envelope this function
// reads. Making that regex a shared, tested function (instead of leaving each
// caller to write its own ad hoc extraction, or a human to do it by hand while
// reading logs) is the actual fix; the alternative is the marker staying
// attributable in principle but not in practice.
func ToolNameFromResult(resultText string) (string, bool) {
	if m := toolNameFromEnvelope.FindStringSubmatch(resultText); len(m) == 2 {
		name := strings.Trim(m[1], `"'`)
		if name != "" {
			return name, true
		}
	}
	return "", false
}

func canonicalFailureValue(value interface{}, field string, depth int) (string, bool) {
	if depth > 12 || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return "", false
		}
		if jsonEnvelopeFields[field] {
			if decoded, ok := decodeJSONValue(trimmed); ok {
				if signal, failed := canonicalFailureValue(decoded, field, depth+1); failed {
					return signal, true
				}
			}
		}
		lowered := strings.ToLower(trimmed)
		if strings.HasPrefix(lowered, "error: tool execution failed:") ||
			strings.HasPrefix(lowered, "tool execution failed:") ||
			strings.HasPrefix(lowered, "tool execution canceled:") ||
			strings.HasPrefix(lowered, "tool execution timed out:") {
			return "tool execution failure envelope", true
		}
		if strings.HasPrefix(lowered, "error:") {
			return "error-prefixed result", true
		}
		if match := httpFailurePrefix.FindStringSubmatch(trimmed); len(match) == 2 {
			return "http_status=" + match[1], true
		}
		if field == "stderr" && (strings.Contains(lowered, "permission denied") ||
			strings.Contains(lowered, "operation not permitted") ||
			strings.Contains(lowered, "authorization denied") ||
			strings.Contains(lowered, "access denied")) {
			return "permission denial in stderr", true
		}
	case []interface{}:
		for _, item := range typed {
			if signal, failed := canonicalFailureValue(item, field, depth+1); failed {
				return signal, true
			}
		}
	case map[string]interface{}:
		if field == "error" {
			// A transport error envelope is a failure whatever the inner key is
			// called. Cursor's built-in read tool reports the deny-hook verdict as
			// {"error":{"errorMessage":"..."}} while glob uses
			// {"error":{"error":"..."}}; only the latter was recognised, so the
			// same denied call rendered as a success for one tool and a failure
			// for the other (RTS, 2026-09-03). Scoped to the "error" field so a
			// domain record carrying a "message" stays out of it.
			for _, key := range []string{"error", "errorMessage", "error_message", "message", "msg", "reason", "detail", "details"} {
				if msg, ok := stringFieldFold(typed, key); ok && strings.TrimSpace(msg) != "" {
					return "error.message", true
				}
			}
		}
		if explicitBoolean(typed, "success") == boolFalse ||
			explicitBoolean(typed, "ok") == boolFalse {
			return "success=false", true
		}
		if explicitBoolean(typed, "is_error") == boolTrue ||
			explicitBoolean(typed, "iserror") == boolTrue {
			return "is_error=true", true
		}
		if status, ok := stringFieldFold(typed, "status"); ok {
			switch strings.ToLower(strings.TrimSpace(status)) {
			case "error", "failed", "failure", "denied", "unauthorized", "forbidden":
				return "status=" + strings.ToLower(strings.TrimSpace(status)), true
			}
		}
		for _, key := range []string{"exit_code", "exitcode"} {
			if raw, ok := valueFieldFold(typed, key); ok {
				if code, valid := numericCode(raw); valid && code != 0 {
					return fmt.Sprintf("%s=%d", key, code), true
				}
			}
		}
		for _, key := range []string{"status_code", "statuscode", "http_status", "httpstatus"} {
			if raw, ok := valueFieldFold(typed, key); ok {
				if code, valid := numericCode(raw); valid && code >= 400 {
					return fmt.Sprintf("http_status=%d", code), true
				}
			}
		}

		// Recurse through known transport-envelope fields, not arbitrary domain
		// data. A tool may legitimately return a record whose own status is
		// "failed"; inspecting every nested value would turn that discussion into
		// a tool failure.
		for _, key := range []string{"content", "text", "result", "stdout", "stderr", "error"} {
			if nested, ok := valueFieldFold(typed, key); ok {
				if signal, failed := canonicalFailureValue(nested, strings.ToLower(key), depth+1); failed {
					return signal, true
				}
			}
		}
	}
	return "", false
}

func decodeJSONValue(text string) (interface{}, bool) {
	if text == "" || !strings.ContainsAny(text[:1], `{[\"`) {
		return nil, false
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

type boolState uint8

const (
	boolMissing boolState = iota
	boolFalse
	boolTrue
)

func explicitBoolean(values map[string]interface{}, key string) boolState {
	raw, ok := valueFieldFold(values, key)
	if !ok {
		return boolMissing
	}
	value, ok := raw.(bool)
	if !ok {
		return boolMissing
	}
	if value {
		return boolTrue
	}
	return boolFalse
}

func valueFieldFold(values map[string]interface{}, key string) (interface{}, bool) {
	for name, value := range values {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
		if normalized == key || strings.ReplaceAll(normalized, "_", "") == strings.ReplaceAll(key, "_", "") {
			return value, true
		}
	}
	return nil, false
}

func stringFieldFold(values map[string]interface{}, key string) (string, bool) {
	raw, ok := valueFieldFold(values, key)
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	return value, ok
}

func numericCode(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// Suspicious is retained as the logging-facing API, but it intentionally shares
// CanonicalFailure's field-aware contract. Semantic content is not a transport
// failure signal.
func Suspicious(resultText string) (string, bool) {
	return CanonicalFailure(resultText)
}

// nonZeroExitCodeSignal finds an exit_code field whose value is not 0. Shell
// results carry the real outcome here while the transport reports success, so a
// non-zero value is the single most reliable failure signal available.
func nonZeroExitCodeSignal(lowered string) (string, bool) {
	for _, key := range []string{`"exit_code":`, `"exit_code" :`, `"exitcode":`, "exit_code=", "exit code "} {
		index := strings.Index(lowered, key)
		if index < 0 {
			continue
		}
		rest := strings.TrimSpace(lowered[index+len(key):])
		rest = strings.TrimLeft(rest, `"' `)
		if rest == "" {
			continue
		}
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			continue
		}
		value := rest[:digits]
		if strings.Trim(value, "0") != "" {
			return "exit_code=" + value, true
		}
	}
	return "", false
}

// truncateToolResultForLog bounds a tool result so one pathological payload
// cannot flood the log. Matches the 400-byte budget the bridge handlers use.
func TruncateForLog(s string) string {
	return truncateTo(s, 400)
}

func truncateTo(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// problemReportingTools return problem descriptions as their normal payload, so
// content-sniffing them is guaranteed noise. Measured on two live workflows:
// 70 of 173 suspect hits came from these five, and not one was a tool failure —
// a skill doc describing error handling, a Pulse backlog listing "not found"
// findings, a module state quoting concern text.
//
// This suppresses only the heuristic. A genuine failure in these tools still
// returns a non-nil error and is reported under the confirmed Marker, so
// nothing real is lost.
var problemReportingTools = map[string]bool{
	"read_skill":                    true,
	"get_pulse_finding_backlog":     true,
	"get_pulse_module_state":        true,
	"get_pulse_review_result":       true,
	"get_workflow_command_guidance": true,
	"query_workflow_db":             true,
	// PLAT-746: a recorded failed Python check is typed domain data.
	// A genuine handler/transport error still produces a failure envelope.
	"python_automation":         true,
	"record_pulse_worklist":     true,
	"organize_global_learnings": true,
	"consolidate_knowledgebase": true,
	// PLAT-127. A message-sequence orchestrator step's route catalog dump
	// (get_route_description with no route_id, listing every configured route's
	// full behavior prose) hit this same shape on 2026-08-17: 12 of 12 suspects
	// on one workflow, zero of them an actual failure -- one whose prose happened
	// to contain a word this heuristic treats as a failure signal.
	"get_route_description": true,
}

// SuspiciousForTool is the logging-facing name for the same per-tool canonical
// classifier. Keeping one classifier prevents a successful tool call from being
// returned to the model while observability labels it as a failure.
func SuspiciousForTool(toolName, resultText string) (string, bool) {
	return CanonicalFailureForTool(toolName, resultText)
}

// Per-field budgets for TruncateArgsForLog. A short diagnostic field must
// survive alongside a long one.
const (
	argFieldBudget = 200
	argTotalBudget = 900
)

// TruncateArgsForLog bounds a tool-argument JSON object field by field instead
// of chopping the serialized blob at a fixed offset.
//
// Head-first truncation loses whichever field happens to serialize late. A real
// case: a diff_patch_workspace_file denial logged only the opening of a large
// `diff`, so `filepath` — the one field that explained the failure — was cut
// off entirely and had to be recovered from the error text. Keys are cheap and
// always diagnostic; only values are trimmed.
//
// Valid non-object input falls back to plain truncation. Malformed JSON is
// omitted because a partial credential field cannot be safely decoded.
func TruncateArgsForLog(argsJSON string) string {
	trimmed := strings.TrimSpace(redactSecretValuesForLog(argsJSON))
	if trimmed == "" {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || len(fields) == 0 {
		return TruncateForLog(trimmed)
	}

	// Sorted so the same call always logs the same way and two lines can be
	// diffed against each other.
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(truncateTo(renderArgValue(fields[name]), argFieldBudget))
		if builder.Len() > argTotalBudget {
			builder.WriteString(" ...(more fields omitted)")
			break
		}
	}
	builder.WriteByte('}')
	return builder.String()
}

// renderArgValue prefers the unquoted string form so a path or command reads
// cleanly in a log line, and falls back to the raw JSON for other types.
func renderArgValue(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	return strings.TrimSpace(string(raw))
}

// redactSecretValuesForLog replaces the values of the argument fields that carry a secret a person gave an agent to
// store (`value`, `values`, `api_key`, `client_secret`, and `secrets.set` of a settings call) with a marker, at any depth, so a failed secret call
// never writes the secret into the log. Names and every other field are kept.
// Malformed JSON is omitted; it may contain an incomplete secret value.
func redactSecretValuesForLog(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		return argsJSON
	}
	var parsed any
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return "[redacted] (invalid JSON arguments omitted)"
	}
	changed := false
	var walk func(node any, parent string) any
	walk = func(node any, parent string) any {
		switch typed := node.(type) {
		case map[string]any:
			for key, value := range typed {
				lower := strings.ToLower(key)
				if lower == "value" || lower == "values" || lower == "api_key" || lower == "client_secret" || lower == "credential" || (lower == "set" && strings.ToLower(parent) == "secrets") {
					if value != nil {
						typed[key] = "[redacted]"
						changed = true
						continue
					}
				}
				typed[key] = walk(value, key)
			}
			return typed
		case []any:
			for i := range typed {
				typed[i] = walk(typed[i], parent)
			}
			return typed
		}
		return node
	}
	walk(parsed, "")
	if !changed {
		return argsJSON
	}
	redacted, err := json.Marshal(parsed)
	if err != nil {
		return "[arguments omitted]"
	}
	return string(redacted)
}
