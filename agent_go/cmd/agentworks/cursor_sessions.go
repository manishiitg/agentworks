package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	var found []cursorObservation
	live := 0
	// Read at most 4096 owned records per call; prune dead kernel identities
	// before applying the live-record bound. Never silently truncate ambiguity.
	scanned := 0
	for {
		files, readErr := dir.ReadDir(256)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return liveAgentSession{}, readErr
		}
		for _, file := range files {
			scanned++
			if scanned > 4096 {
				return liveAgentSession{}, errors.New("Cursor discovery exceeds its bounded scan; clean stale observations before retrying")
			}
			raw, e := readMuseObservation(dir, file.Name())
			var record cursorObservation
			if e != nil || json.Unmarshal(raw, &record) != nil || record.PID <= 1 ||
				file.Name() != cursorObservationName(record.Host, record.Session) {
				continue
			}
			if _, e = cursorUUID(record.Session); e != nil {
				continue
			}
			current, e := cursorProcessAt(record.PID)
			if e != nil || current.Host != record.Host {
				if !cursorProcessForHost(record.Host) {
					removeMuseObservation(dir, file.Name())
				}
				continue
			}
			live++
			if live > 256 {
				return liveAgentSession{}, errors.New("Cursor discovery exceeds its live record limit")
			}
			if record.Session == session {
				found = append(found, record)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
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

// The desktop app ancestor is shared across conversations. Until the shared
// own-session lease contract proves the calling conversation, shell helpers are
// refused even with a forged native environment and an observed UUID.
func cursorRequireHelperLease(own liveAgentSession, attachment string) error {
	process, err := cursorProcessForContext(own.host)
	if err != nil {
		return err
	}
	return cursorHelperAllowed(process.Kind, attachment)
}

func cursorHelperAllowed(kind, attachment string) error {
	if kind == "desktop" {
		return errors.New("Cursor desktop helpers require a per-conversation own-session fence; use the verified hook path")
	}
	if attachment == "" {
		return errors.New("Every Cursor helper action requires the original --attachment-id")
	}
	return nil
}

func cursorProcessForContext(host string) (cursorProcess, error) {
	parts := strings.Split(host, ":")
	if len(parts) != 3 || parts[0] != "cursor" {
		return cursorProcess{}, errors.New("invalid Cursor host")
	}
	pid, err := strconv.Atoi(parts[1])
	if err != nil {
		return cursorProcess{}, err
	}
	process, err := cursorProcessAt(pid)
	if err != nil || process.Host != host {
		return cursorProcess{}, errors.New("Cursor host changed")
	}
	return process, nil
}
