package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// The same source/destination admission and ordinary upload handler as browser
// context. Neither a command nor a shell ID/path is accepted from the caller.
func (api *StreamingAPI) addTerminalChatContext(w http.ResponseWriter, r *http.Request, claims *UserClaims, project, workspace string, in crewChatContextRequest) (string, int, bool, string) {
	if in.TerminalTab < 1 || in.TerminalTab > codeShellMaxTabs {
		writeAgentProfileError(w, 400, "Choose this Crew's terminal tab, from 1 to 3.")
		return "", 0, false, "terminal_tab"
	}
	source := r.Clone(r.Context())
	address := *r.URL
	q := address.Query()
	q.Set("tab", strconv.Itoa(in.TerminalTab))
	address.RawQuery = q.Encode()
	source.URL = &address
	_, shellID, _, _, _, status, _ := api.codeShellTargetFull(source)
	if status != 0 {
		writeAgentProfileError(w, status, "Open an available terminal in this Crew.")
		return "", 0, false, "terminal_access"
	}
	commands := []codeShellContextCommand{}
	for _, command := range codeShellContextCommands(shellID) {
		if command.ExitCode != nil {
			commands = append(commands, command)
		}
	}
	if in.Operation == "probe" {
		writeAgentProfileJSON(w, 200, map[string]any{"source": "terminal", "terminal_tab": in.TerminalTab, "commands": commands})
		return shellID, 0, true, "ready"
	}
	var selected *codeShellContextCommand
	for _, command := range commands {
		if command.ID == in.CommandID && command.ExitCode != nil {
			copy := command
			selected = &copy
			break
		}
	}
	if selected == nil {
		writeAgentProfileError(w, 409, "Select output from a completed command in this connected terminal. Its command label is unavailable.")
		return shellID, 0, false, "command_unavailable"
	}
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > 16384 || !utf8.ValidString(in.Text) || in.StartLine < 0 || in.EndLine < in.StartLine || in.EndLine-in.StartLine > 1000 {
		writeAgentProfileError(w, 400, "Select up to 16 KiB of output from one command, then add it again.")
		return shellID, 0, false, "selection_bounds"
	}
	// Text and line coordinates remain explicitly untrusted person-selected
	// context. The command label comes only from this source's shell markers.
	if !api.admitPendingChatUpload(w, r, project, in.ConversationKey, in.SessionID) {
		return shellID, 0, false, "access_changed"
	}
	name := "terminal-selection-" + uuid.NewString() + ".json"
	data, _ := json.MarshalIndent(map[string]any{"source": "terminal", "untrusted_context": true, "terminal_tab": in.TerminalTab, "command_id": selected.ID, "command": selected.Command, "exit_code": *selected.ExitCode, "text": in.Text, "start_line": in.StartLine, "end_line": in.EndLine}, "", "  ")
	rel := path.Join("uploads", name)
	buffer := &externalBufferedResponse{}
	api.externalWriteCrewFile(buffer, r, claims, map[string]any{"crew_id": project, "path": rel, "content_base64": base64.StdEncoding.EncodeToString(data)})
	if buffer.status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(buffer.status)
		_, _ = w.Write(buffer.body.Bytes())
		return shellID, 0, false, "upload_failed"
	}
	writeAgentProfileJSON(w, 200, map[string]any{"source": "terminal", "capture": map[string]any{"command": selected.Command, "text": in.Text}, "files": []crewChatContextFile{{Name: name, Path: path.Join(workspace, rel), Type: "file"}}, "preview_path": ""})
	return shellID, len(data), true, "attached_files_ready"
}
