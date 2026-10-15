package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The Cursor desktop app runs every chat in one process and shares one
// messaging MCP server between them, so the server cannot tell which chat is
// calling. Cursor's beforeMCPExecution hook does know: it gets the exact
// conversation_id with the tool name and arguments. The hook (after the
// Cursor.app ancestry check) writes a one-shot fence keyed by the tool and a
// hash of its full arguments; the server consumes the matching fence and takes
// the chat from it, never from the arguments. Send and reply carry a unique
// submission_id and ack a message_id, so two chats cannot share a key; any
// other collision is refused as ambiguous.

const (
	cursorMessagesServer = "agentworks-messages"
	cursorFenceTTL       = 120 * time.Second
	cursorFenceScanLimit = 4096
)

var cursorMessageTools = []string{"agent_message_set_purpose", "agent_message_peers", "agent_message_send", "agent_message_read", "agent_message_ack", "agent_message_reply"}

type cursorFence struct {
	Conversation string `json:"conversation_id"`
	Host         string `json:"host_key"`
	Tool         string `json:"tool"`
	ExpiresMS    int64  `json:"expires_unix_ms"`
}

func cursorFenceDirectory(root string) string {
	return filepath.Join(filepath.Dir(root), "cursor-fences")
}

// cursorMessageTool maps the hook's tool name (Cursor may prefix it with the
// server name) to one of the messaging tools; anything else is not ours.
func cursorMessageTool(name string) string {
	for _, tool := range cursorMessageTools {
		if name == tool || strings.HasSuffix(name, "_"+tool) || strings.HasSuffix(name, "-"+tool) || strings.HasSuffix(name, ":"+tool) {
			return tool
		}
	}
	return ""
}

// cursorCallKey hashes the tool and its full arguments as canonical JSON
// (object keys sorted), the same on the hook and the server side.
func cursorCallKey(tool string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(tool+"\x00"), raw...))
	return hex.EncodeToString(sum[:]), nil
}

// cursorHookArgs parses the hook's tool_input (a JSON string) into arguments.
func cursorHookArgs(input string) (map[string]any, error) {
	args := map[string]any{}
	if strings.TrimSpace(input) == "" {
		return args, nil
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return nil, errors.New("Cursor sent tool input that is not a JSON object")
	}
	return args, nil
}

func writeCursorFence(root, host, conversation, toolName, toolInput string, now time.Time) error {
	tool := cursorMessageTool(toolName)
	if tool == "" {
		return nil
	}
	if _, err := cursorUUID(conversation); err != nil {
		return err
	}
	args, err := cursorHookArgs(toolInput)
	if err != nil {
		return err
	}
	key, err := cursorCallKey(tool, args)
	if err != nil {
		return err
	}
	dir, err := openMuseObservationDirectory(cursorFenceDirectory(root), true)
	if err != nil {
		return err
	}
	defer dir.Close()
	raw, err := json.Marshal(cursorFence{Conversation: conversation, Host: host, Tool: tool, ExpiresMS: now.Add(cursorFenceTTL).UnixMilli()})
	if err != nil {
		return err
	}
	// One record per announced call, so two identical calls in a row from the
	// same chat each find their own record.
	if err = writeMuseObservation(dir, key+"-"+conversation+"-"+uuid.NewString()+".json", raw); err != nil {
		return err
	}
	hookLog(root, "cursor fence written conversation=%s tool=%s", conversation, tool)
	return nil
}

// consumeCursorFence returns the one chat whose hook announced exactly this
// call and uses up one of that chat's records for it. Expired records are
// pruned. No record, or records from several chats for the same call, is
// refused with a plain message (the ambiguous records are removed).
func consumeCursorFence(root, host, toolName string, args map[string]any, now time.Time) (string, error) {
	refuse := errors.New("AgentWorks could not confirm which Cursor chat made this call. Try again in the chat; if it keeps failing, run agentworks setup cursor and restart Cursor.")
	tool := cursorMessageTool(toolName)
	key, err := cursorCallKey(tool, args)
	if tool == "" || err != nil {
		hookLog(root, "cursor fence refused tool=%s reason=unknown_tool", toolName)
		return "", refuse
	}
	dir, err := openMuseObservationDirectory(cursorFenceDirectory(root), false)
	if err != nil {
		hookLog(root, "cursor fence refused tool=%s reason=no_fences", tool)
		return "", refuse
	}
	defer dir.Close()
	type match struct {
		name  string
		fence cursorFence
	}
	var found []match
	chats := map[string]bool{}
	scanned := 0
	for {
		files, readErr := dir.ReadDir(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return "", readErr
		}
		for _, file := range files {
			if scanned++; scanned > cursorFenceScanLimit {
				break
			}
			name := file.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			raw, e := readMuseObservation(dir, name)
			var fence cursorFence
			if e != nil || json.Unmarshal(raw, &fence) != nil || now.UnixMilli() > fence.ExpiresMS {
				removeMuseObservation(dir, name)
				continue
			}
			if strings.HasPrefix(name, key+"-"+fence.Conversation+"-") && fence.Host == host && fence.Tool == tool {
				found = append(found, match{name, fence})
				chats[fence.Conversation] = true
			}
		}
		if errors.Is(readErr, io.EOF) || scanned > cursorFenceScanLimit {
			break
		}
	}
	if len(chats) > 1 {
		for _, m := range found {
			removeMuseObservation(dir, m.name)
		}
		hookLog(root, "cursor fence refused tool=%s reason=ambiguous chats=%d", tool, len(chats))
		return "", errors.New("Two Cursor chats made the same call at the same moment, so AgentWorks could not tell them apart. Try again.")
	}
	if len(found) == 0 {
		hookLog(root, "cursor fence refused tool=%s reason=missing_or_expired", tool)
		return "", refuse
	}
	removeMuseObservation(dir, found[0].name)
	hookLog(root, "cursor fence consumed conversation=%s tool=%s", found[0].fence.Conversation, tool)
	return found[0].fence.Conversation, nil
}
