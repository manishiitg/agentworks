package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

type slackAccountIdentity struct{ User, Team string }

func slackTokenIdentity(ctx context.Context, token *oauth2.Token) (slackAccountIdentity, error) {
	id := slackAccountIdentity{}
	if user, ok := token.Extra("authed_user").(map[string]interface{}); ok {
		id.User, _ = user["id"].(string)
	}
	if team, ok := token.Extra("team").(map[string]interface{}); ok {
		id.Team, _ = team["id"].(string)
	}
	if id.User != "" && id.Team != "" {
		return id, nil
	}
	// Older saved logins have no identity metadata. Ask Slack who that token
	// belongs to before allowing a reconnect to replace it.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/auth.test", nil)
	if err != nil {
		return id, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	resp, err := netguard.Client(15 * time.Second).Do(req)
	if err != nil {
		return id, fmt.Errorf("Could not check the Slack account. Try signing in again from Integrations → Slack.")
	}
	defer resp.Body.Close()
	var result struct {
		OK   bool   `json:"ok"`
		User string `json:"user_id"`
		Team string `json:"team_id"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&result) != nil || !result.OK || result.User == "" || result.Team == "" {
		return id, fmt.Errorf("Could not verify the Slack account. Connect it as a new account in Integrations → Slack.")
	}
	return slackAccountIdentity{User: result.User, Team: result.Team}, nil
}

func validatePersonalSlackToken(ctx context.Context, owner, name string, token *oauth2.Token) error {
	identity, err := slackTokenIdentity(ctx, token)
	if err != nil {
		return err
	}
	server, err := personalSlackServer(owner, name)
	if err != nil {
		return err
	}
	previous := slackAccountIdentity{User: server.SlackUserID, Team: server.SlackTeamID}
	if previous.User == "" {
		_, cfg, err := placeMCPServerConfig(owner, name)
		if err != nil {
			return err
		}
		if old, err := oauth.NewTokenStore(cfg.OAuth.TokenFile).Load(); err == nil {
			previous, err = slackTokenIdentity(ctx, old)
			if err != nil {
				return err
			}
		}
	}
	if previous.User != "" && previous != identity {
		return fmt.Errorf("This is a different Slack account; connect it as a new account.")
	}
	placeMCPMu.Lock()
	defer placeMCPMu.Unlock()
	servers, err := listPlaceMCPServersLocked(owner)
	if err != nil {
		return err
	}
	for i := range servers {
		if servers[i].Name != name || !isPersonalSlack(servers[i]) {
			continue
		}
		if servers[i].SlackUserID != "" && (servers[i].SlackUserID != identity.User || servers[i].SlackTeamID != identity.Team) {
			return fmt.Errorf("This is a different Slack account; connect it as a new account.")
		}
		servers[i].SlackUserID, servers[i].SlackTeamID = identity.User, identity.Team
		dir, err := placeMCPDir(owner)
		if err != nil {
			return err
		}
		return writePlaceMCPJSON(filepath.Join(dir, "servers.json"), servers)
	}
	return fmt.Errorf("This Slack connection was removed. Connect it again in Integrations → Slack.")
}
