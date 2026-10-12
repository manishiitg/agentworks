package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

type cursorObservation struct {
	Session string `json:"session_id"`
	PID     int    `json:"pid"`
	Host    string `json:"host_key"`
	Kind    string `json:"kind"`
	Cwd     string `json:"cwd"`
}

func cursorUUID(value string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != strings.ToLower(value) {
		return "", errors.New("Cursor did not supply a canonical native UUID")
	}
	return id.String(), nil
}

func cursorObservationDirectory(root string) string {
	return filepath.Join(filepath.Dir(root), "cursor-sessions")
}

func cursorObservationName(host, session string) string {
	// Host keys contain ':', unsuitable for portable filenames.
	return strings.ReplaceAll(host, ":", "-") + "-" + session + ".json"
}

func observeCursor(root string, process cursorProcess, input pluginHookInput) error {
	dir, err := openMuseObservationDirectory(cursorObservationDirectory(root), input.Event != "SessionEnd")
	if err != nil {
		return err
	}
	defer dir.Close()
	name := cursorObservationName(process.Host, input.SessionID)
	if input.Event == "SessionEnd" {
		removeMuseObservation(dir, name)
		return nil
	}
	if input.Event != "SessionStart" && input.Event != "UserPromptSubmit" && input.Event != "Stop" {
		return nil
	}
	raw, err := json.Marshal(cursorObservation{Session: input.SessionID, PID: process.PID, Host: process.Host, Kind: process.Kind, Cwd: input.Cwd})
	if err != nil {
		return err
	}
	return writeMuseObservation(dir, name, raw)
}

func cursorObservedSession(root string, process cursorProcess, session string) (liveAgentSession, error) {
	dir, err := openMuseObservationDirectory(cursorObservationDirectory(root), false)
	if err != nil {
		return liveAgentSession{}, errors.New("Cursor has no verified native hook observation")
	}
	defer dir.Close()
	files, err := dir.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return liveAgentSession{}, err
	}
	if len(files) > 256 {
		return liveAgentSession{}, errors.New("Cursor discovery exceeds its bounded record limit; clean stale observations before retrying")
	}
	var found []cursorObservation
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), "-"+session+".json") {
			continue
		}
		raw, e := readMuseObservation(dir, file.Name())
		var record cursorObservation
		if e != nil || json.Unmarshal(raw, &record) != nil || record.Session != session ||
			file.Name() != cursorObservationName(record.Host, record.Session) {
			continue
		}
		current, e := cursorProcessAt(record.PID)
		if e == nil && current.Host == record.Host {
			found = append(found, record)
		}
	}
	if len(found) != 1 || found[0].Host != process.Host {
		return liveAgentSession{}, errors.New("Cursor native UUID is stale or ambiguous for this exact process")
	}
	return liveAgentSession{provider: "cursor", session: session, host: process.Host, cwd: found[0].Cwd}, nil
}

// Root PLAT-861 can wire this into its own-session command without provider,
// session, process or endpoint overrides supplied by the model.
func cursorCallingContext() (liveAgentSession, error) {
	process, err := cursorAncestor()
	if err != nil {
		return liveAgentSession{}, err
	}
	session, err := cursorUUID(os.Getenv("CURSOR_CONVERSATION_ID"))
	if err != nil {
		return liveAgentSession{}, errors.New("Cursor helper requires native CURSOR_CONVERSATION_ID")
	}
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return liveAgentSession{}, err
	}
	return cursorObservedSession(root, process, session)
}
