package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools"
)

// withToolNameHints serves a bridge tool call and, when it failed on an old
// tool name or an MCP-only action, adds the shape that works in this chat.
// In-app agents still meet old names (run_full_workflow, execute_step,
// send_message) in guidance and their own memory after the tools were merged
// into families, and the bare "unknown" error sent them guessing (a live
// server on 2026-10-28, PLAT-856-397). The old names stay uncallable.
func withToolNameHints(w http.ResponseWriter, tool string, serve func(http.ResponseWriter)) {
	rec := &internalResponseCapture{header: http.Header{}}
	serve(rec)
	rec.body = *bytes.NewBuffer(annotateToolNameHints(rec.body.Bytes(), tool))
	copyInternalResponse(w, rec)
}

var unknownToolNamesPattern = regexp.MustCompile(`(?:unknown|requested)=\[([^\]]*)\]`)

func annotateToolNameHints(body []byte, tool string) []byte {
	var outer map[string]interface{}
	if json.Unmarshal(body, &outer) != nil {
		return body
	}
	errText, _ := outer["error"].(string)
	if errText == "" || outer["success"] == true {
		return body
	}
	hint := toolNameHint(tool, errText)
	if hint == "" {
		return body
	}
	outer["error"] = strings.TrimSpace(errText) + " " + hint
	encoded, err := json.Marshal(outer)
	if err != nil {
		return body
	}
	return encoded
}

func toolNameHint(tool, errText string) string {
	var hints []string
	seen := map[string]bool{}
	add := func(hint string) {
		if hint != "" && !seen[hint] {
			seen[hint] = true
			hints = append(hints, hint)
		}
	}
	for _, match := range unknownToolNamesPattern.FindAllStringSubmatch(errText, -1) {
		names := strings.FieldsFunc(match[1], func(r rune) bool {
			return r == ' ' || r == ',' || r == '"' || r == '[' || r == ']'
		})
		for _, name := range names {
			add(platformtools.RetiredNameHint(name))
		}
	}
	// Panel skills are read with read_skill in product chat; help action=topic
	// and action=skill exist only for MCP clients.
	if strings.TrimSpace(tool) == "help" && strings.Contains(errText, `field "action" must be one of`) {
		add("In this chat, read a skill or one of its reference files with read_skill; help action=topic is only for MCP clients.")
	}
	return strings.Join(hints, " ")
}
