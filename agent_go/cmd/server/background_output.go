package server

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/commandlabel"
)

// Live output of the commands coding agents run, during their turn and after
// the reply ended, and Stop for a command that outlived its turn (#3131).
// Providers emit llmtypes.BackgroundOutput (Codex app-server output deltas,
// Claude's task output file, Cursor's terminal files); the chat shows it on
// the command's own card as live-only tool_call_output events, each holding
// the command's current output tail.

// backgroundOutputTailBytes is how much of a command's output the card shows.
const backgroundOutputTailBytes = 16 * 1024

// codexCommandWorkPrefix marks a Codex command in the chat's background work.
const codexCommandWorkPrefix = "codex-command:"

// cursorShellWorkPrefix marks a Cursor background shell in the chat's background work.
const cursorShellWorkPrefix = "cursor-shell:"

var (
	codexBackgroundCommands    = codexcli.CodexBackgroundCommands
	stopCodexBackgroundCommand = codexcli.StopCodexBackgroundCommand
	cursorBackgroundShells     = cursorcli.CursorBackgroundCommands
	stopCursorBackgroundShell  = cursorcli.StopCursorBackgroundCommand
)

type backgroundOutputTail struct {
	provider string
	text     string
	dropped  bool
	stopID   string
	seq      int
	// onItsOwn: the command ran past its turn. native: its card is closed by
	// the provider's own completion, which carries the output (Codex).
	onItsOwn, native bool
}

var backgroundOutputs = struct {
	sync.Mutex
	tails map[string]*backgroundOutputTail // session + "\x00" + tool call
}{tails: map[string]*backgroundOutputTail{}}

func (api *StreamingAPI) wireBackgroundOutput() {
	llmtypes.SetBackgroundOutputListener(api.recordBackgroundOutput)
}

// backgroundOutputStopID is the chat's background-work item that stops the
// command, or "" when it cannot be stopped on its own.
func backgroundOutputStopID(output llmtypes.BackgroundOutput) string {
	if !output.CanStop || output.Done || output.TaskID == "" {
		return ""
	}
	switch output.Provider {
	case "claude-code":
		return claudeTaskWorkPrefix + output.TaskID
	case "codex-cli":
		return codexCommandWorkPrefix + output.TaskID
	case "cursor-cli":
		return cursorShellWorkPrefix + output.TaskID
	}
	return ""
}

func (api *StreamingAPI) nativeCommandOpen(sessionID, toolCallID string) bool {
	for _, command := range api.eventStore.OpenNativeCommands(sessionID) {
		if command.ID == toolCallID {
			return true
		}
	}
	return false
}

func (api *StreamingAPI) recordBackgroundOutput(output llmtypes.BackgroundOutput) {
	sessionID := strings.TrimSpace(output.Owner)
	if api == nil || api.eventStore == nil || sessionID == "" || output.ToolCallID == "" || !api.eventStore.IsDurableChatSession(sessionID) {
		return
	}
	native := api.nativeCommandOpen(sessionID, output.ToolCallID)
	key := sessionID + "\x00" + output.ToolCallID
	stopID := backgroundOutputStopID(output)
	backgroundOutputs.Lock()
	tail := backgroundOutputs.tails[key]
	first := tail == nil
	if first {
		tail = &backgroundOutputTail{provider: output.Provider}
		backgroundOutputs.tails[key] = tail
	}
	tail.text += output.Text
	tail.dropped = tail.dropped || output.Dropped
	if len(tail.text) > backgroundOutputTailBytes {
		tail.text, tail.dropped = llmtypes.BackgroundOutputTail(tail.text, backgroundOutputTailBytes), true
	}
	stopChanged := stopID != tail.stopID
	tail.stopID = stopID
	tail.onItsOwn = tail.onItsOwn || output.CanStop
	tail.native = tail.native || native
	tail.seq++
	shown := *tail
	if output.Done {
		delete(backgroundOutputs.tails, key)
	}
	backgroundOutputs.Unlock()
	if output.Done && output.Provider == "claude-code" {
		claudeFinishedTasks.noteOutput(sessionID, output.TaskID, shown.text)
	}

	if first || stopChanged || output.Done {
		log.Printf("[BACKGROUND_OUTPUT] session=%s provider=%s tool=%s bytes=%d dropped=%t stoppable=%t done=%t", sessionID, output.Provider, output.ToolCallID, len(shown.text), shown.dropped, stopID != "", output.Done)
	}
	fields := map[string]interface{}{"tool_call_id": output.ToolCallID, "provider": output.Provider, "output": shown.text, "dropped": shown.dropped, "done": output.Done}
	if stopID != "" {
		fields["stop_id"] = stopID
	}
	api.addToolOutputEvent(sessionID, fmt.Sprintf("tool-output:%s:%d", output.ToolCallID, shown.seq), fields)
	// A command whose card shows no other result keeps its last output.
	if output.Done && shown.onItsOwn && !shown.native && strings.TrimSpace(shown.text) != "" {
		final := map[string]interface{}{"final": true}
		for k, v := range fields {
			final[k] = v
		}
		api.addToolOutputEvent(sessionID, "tool-output-final:"+output.ToolCallID, final)
		log.Printf("[BACKGROUND_OUTPUT] final_saved session=%s provider=%s tool=%s bytes=%d", sessionID, output.Provider, output.ToolCallID, len(shown.text))
	}
}

func (api *StreamingAPI) addToolOutputEvent(sessionID, id string, fields map[string]interface{}) {
	now := time.Now()
	data := agentevents.NewAgentEvent(storeevents.NewGenericEventData(storeevents.ToolCallOutputEventType, fields))
	data.SessionID = sessionID
	api.eventStore.AddEvent(sessionID, storeevents.Event{ID: id, Type: storeevents.ToolCallOutputEventType, Timestamp: now, SessionID: sessionID,
		ExecutionKind: "main_agent", TerminalOwnerID: "main:" + sessionID, Data: data})
}

// endBackgroundOutputs ends the live output of a provider's commands in a
// chat whose process closed: they stopped with it.
func (api *StreamingAPI) endBackgroundOutputs(sessionID, provider string) {
	prefix := sessionID + "\x00"
	backgroundOutputs.Lock()
	var ended []string
	for key, tail := range backgroundOutputs.tails {
		if strings.HasPrefix(key, prefix) && tail.provider == provider {
			ended = append(ended, strings.TrimPrefix(key, prefix))
		}
	}
	backgroundOutputs.Unlock()
	for _, id := range ended {
		api.recordBackgroundOutput(llmtypes.BackgroundOutput{Provider: provider, Owner: sessionID, ToolCallID: id, Done: true})
	}
}

// codexCommandBackgroundWork lists the chat's Codex commands that outlived
// their turn, each with its own Stop.
func codexCommandBackgroundWork(sessionID string) []SessionBackgroundWorkItem {
	items := []SessionBackgroundWorkItem{}
	for _, command := range codexBackgroundCommands(sessionID) {
		label := commandlabel.Label(command.Label)
		if label == "" {
			label = "Background command"
		}
		items = append(items, SessionBackgroundWorkItem{ID: codexCommandWorkPrefix + command.ID, Kind: "native_command", Label: label, StartedAt: command.StartedAt, CanStop: true})
	}
	return items
}

func stopCodexCommandBackgroundWork(sessionID, execID string) bool {
	id := strings.TrimPrefix(execID, codexCommandWorkPrefix)
	if id == "" || !stopCodexBackgroundCommand(sessionID, id) {
		return false
	}
	log.Printf("[CODEX_BACKGROUND] stop from background work session=%s command=%s", sessionID, id)
	return true
}

// cursorShellBackgroundWork lists the chat's Cursor shells running in the
// background, each with its own Stop.
func cursorShellBackgroundWork(sessionID string) []SessionBackgroundWorkItem {
	items := []SessionBackgroundWorkItem{}
	for _, shell := range cursorBackgroundShells(sessionID) {
		label := commandlabel.Label(shell.Label)
		if label == "" {
			label = "Background command"
		}
		items = append(items, SessionBackgroundWorkItem{ID: cursorShellWorkPrefix + shell.ID, Kind: "native_command", Label: label, StartedAt: shell.StartedAt, CanStop: true})
	}
	return items
}

func stopCursorShellBackgroundWork(sessionID, execID string) bool {
	id := strings.TrimPrefix(execID, cursorShellWorkPrefix)
	if id == "" || !stopCursorBackgroundShell(sessionID, id) {
		return false
	}
	log.Printf("[CURSOR_BACKGROUND] stop from background work session=%s shell=%s", sessionID, id)
	return true
}
