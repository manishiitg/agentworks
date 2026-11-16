package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One-time schema conversion, before Team readers start. New readers never
// fall back to terminal.named_agent_id. Unrelated JSON values remain raw.
func convertTeamBridgeNamedID(raw []byte) ([]byte, bool, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, err
	}
	var agents map[string]json.RawMessage
	if err := json.Unmarshal(doc["agents"], &agents); err != nil {
		if len(doc["agents"]) == 0 {
			return raw, false, nil
		}
		return nil, false, err
	}
	changed := false
	for id, encoded := range agents {
		var agent map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &agent); err != nil {
			return nil, false, err
		}
		var terminal map[string]json.RawMessage
		if len(agent["terminal"]) == 0 || string(agent["terminal"]) == "null" {
			continue
		}
		if err := json.Unmarshal(agent["terminal"], &terminal); err != nil {
			return nil, false, err
		}
		legacy, ok := terminal["named_agent_id"]
		if !ok {
			continue
		}
		var old, current string
		if err := json.Unmarshal(legacy, &old); err != nil {
			return nil, false, err
		}
		if currentRaw, ok := agent["named_agent_id"]; ok {
			if err := json.Unmarshal(currentRaw, &current); err != nil {
				return nil, false, err
			}
		}
		if current != "" && old != "" && current != old {
			return nil, false, fmt.Errorf("conflicting named Bridge identities on Team row")
		}
		if current == "" && old != "" {
			agent["named_agent_id"] = legacy
		}
		delete(terminal, "named_agent_id")
		b, err := json.Marshal(terminal)
		if err != nil {
			return nil, false, err
		}
		agent["terminal"] = b
		b, err = json.Marshal(agent)
		if err != nil {
			return nil, false, err
		}
		agents[id] = b
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	b, err := json.Marshal(agents)
	if err != nil {
		return nil, false, err
	}
	doc["agents"] = b
	b, err = json.MarshalIndent(doc, "", "  ")
	return append(b, '\n'), true, err
}

func migrateTeamBridgeNamedIDs() error {
	root, err := crewroot.StateRoot()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	type change struct {
		path          string
		before, after []byte
		project       string
	}
	changes := []change{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, "team-folders", entry.Name(), "team.json")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Team migration requires regular files")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next, changed, err := convertTeamBridgeNamedID(raw)
		if err != nil {
			return err
		}
		if changed {
			changes = append(changes, change{path, raw, next, entry.Name()})
		}
	}
	// Every original is backed up before the first conversion is written.
	for _, c := range changes {
		file, err := os.OpenFile(c.path+".before-bridge-named", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			info, e := os.Lstat(c.path + ".before-bridge-named")
			if e != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("Team Bridge migration backup is unavailable")
			}
			previous, e := os.ReadFile(c.path + ".before-bridge-named")
			if e != nil || !bytes.Equal(previous, c.before) {
				return fmt.Errorf("Team Bridge migration backup differs from the current original")
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("Team Bridge migration backup refused: %w", err)
		}
		_, writeErr := file.Write(c.before)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, c := range changes {
		var doc json.RawMessage = c.after
		if err := fsutil.WriteJSONAtomic(c.path, doc, 0600); err != nil {
			return err
		}
		log.Printf("[AGENT-MESSAGE] event=migrated project=%s outcome=named_id_moved", c.project)
	}
	return nil
}
