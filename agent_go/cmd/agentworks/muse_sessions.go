package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Muse's transcript index has no live process binding and its external session
// ingress can be closed. Observe only UUIDs supplied by the approved native hook.
// These records are a local discovery catalog, not registrations or attachments:
// no credentials, message bodies, login, bridge startup or network calls.
type museSessionObservation struct {
	Session string `json:"session_id"`
	PID     int    `json:"pid"`
	Host    string `json:"host_key"`
	Cwd     string `json:"cwd"`
}

func museSessionDirectory(bridgeRoot string) string {
	return filepath.Join(filepath.Dir(bridgeRoot), "muse-sessions")
}

func museObservationName(host string) string {
	return host + ".json"
}

func observeMuseSession(root string, pid int, host string, input pluginHookInput) {
	id, err := uuid.Parse(input.SessionID)
	if err != nil || pid <= 1 || host == "" {
		return
	}
	dir, err := openMuseObservationDirectory(museSessionDirectory(root), input.Event != "SessionEnd")
	if err != nil {
		return
	}
	defer dir.Close()
	name := museObservationName(host)
	b, readErr := readMuseObservation(dir, name)
	var old museSessionObservation
	hasOld := readErr == nil && json.Unmarshal(b, &old) == nil
	if input.Event == "SessionEnd" {
		if hasOld && old.Host == host && old.Session == id.String() {
			removeMuseObservation(dir, name)
		}
		return
	}
	if input.Event != "UserPromptSubmit" && input.Event != "Stop" {
		return
	}
	// A native prompt establishes the active chat after a switch. A late Stop
	// from the previous chat cannot overwrite it or rebind the MCP child.
	if input.Event == "Stop" && hasOld && old.Session != id.String() {
		return
	}
	b, err = json.Marshal(museSessionObservation{Session: id.String(), PID: pid, Host: host, Cwd: input.Cwd})
	if err != nil {
		return
	}
	_ = writeMuseObservation(dir, name, b)
}

func discoverMuseSessions(root string) []liveAgentSession {
	dir, err := openMuseObservationDirectory(museSessionDirectory(root), false)
	if err != nil {
		return nil
	}
	defer dir.Close()
	files, _ := dir.ReadDir(256)
	var candidates []liveAgentSession
	uuidCounts := map[string]int{}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		b, err := readMuseObservation(dir, file.Name())
		var observed museSessionObservation
		if err != nil || json.Unmarshal(b, &observed) != nil || observed.PID <= 1 {
			continue
		}
		id, err := uuid.Parse(observed.Session)
		if err != nil || file.Name() != museObservationName(observed.Host) {
			continue
		}
		host, err := providerPIDHostKey("muse", observed.PID)
		if err != nil || host != observed.Host {
			if !providerHostAlive("muse", observed.Host) {
				removeMuseObservation(dir, file.Name())
			}
			continue // Process restart/PID reuse requires a fresh native hook.
		}
		uuidCounts[id.String()]++
		candidates = append(candidates, liveAgentSession{provider: "muse", session: id.String(), host: host, cwd: observed.Cwd})
	}
	var found []liveAgentSession
	for _, candidate := range candidates {
		if uuidCounts[candidate.session] == 1 {
			found = append(found, candidate)
		}
	}
	return found // The same UUID on multiple live processes is ambiguous.
}

func museSessionForHost(root, host string) (string, error) {
	for _, session := range discoverMuseSessions(root) {
		if session.host == host {
			return session.session, nil
		}
	}
	return "", errors.New("Muse has no unambiguous native hook session for this process; submit /agentworks attach in the intended chat")
}

const museDiscoveryHint = "Muse chats appear only after an approved hook observes them. If yours is missing, install/review the Muse plugin, restart Muse and type /agentworks attach in that chat."
