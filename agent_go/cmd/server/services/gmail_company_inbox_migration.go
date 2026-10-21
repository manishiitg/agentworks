package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// One-off retirement before old reserved receivers could become ordinary shared accounts.
// Remove after all servers have moved from company receiving to personal Vault inboxes.
func retireCompanyInboxConnections(ctx context.Context, raw string) (string, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return raw, err
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(cfg["connections"], &records); err != nil {
		return raw, nil
	}
	kept := []map[string]json.RawMessage{}
	retired := map[string]bool{}
	for _, c := range records {
		var reserved bool
		_ = json.Unmarshal(c["agent_inbox_receiver"], &reserved)
		if reserved {
			var id string
			_ = json.Unmarshal(c["id"], &id)
			retired[id] = true
		} else {
			kept = append(kept, c)
		}
	}
	if len(retired) == 0 {
		return raw, nil
	}
	backup := gmailConfigFilePath() + ".before-user-inboxes-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := writeWorkspaceFile(ctx, workspaceAPIURL(), backup, raw); err != nil {
		return raw, fmt.Errorf("cannot back up retired company inbox connections")
	}
	cfg["connections"], _ = json.Marshal(kept)
	var id string
	_ = json.Unmarshal(cfg["default_connection_id"], &id)
	if retired[id] {
		delete(cfg, "default_connection_id")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return raw, err
	}
	if err = writeWorkspaceFile(ctx, workspaceAPIURL(), gmailConfigFilePath(), string(data)); err != nil {
		return raw, err
	}
	return string(data), nil
}
