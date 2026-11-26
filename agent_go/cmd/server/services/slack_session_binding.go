package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// One file per thread avoids unrelated channels overwriting each other's
// pointers. RouteKey is checked on restore, so repointing cannot adopt history.
func slackBindingPath(thread ThreadID) string {
	sum := sha256.Sum256([]byte(thread.Key()))
	return fmt.Sprintf("config/slack-threads/%x.json", sum)
}

func (s *SlackService) LoadBotSessionBinding(ctx context.Context, thread ThreadID, routeKey string) (BotSessionBinding, bool, error) {
	binding, found, err := loadSlackBinding(ctx, thread)
	if err != nil {
		return binding, false, err
	}
	if !found && thread.ConnectionID != "" {
		// Bindings written before thread keys carried the app connection
		// live at the three-part path. RouteKey still gates adoption, so a
		// second app in the thread cannot pick up the first app's session.
		legacy := thread
		legacy.ConnectionID = ""
		if binding, found, err = loadSlackBinding(ctx, legacy); err != nil {
			return binding, false, err
		}
	}
	return binding, found && binding.RouteKey == routeKey, nil
}

func loadSlackBinding(ctx context.Context, thread ThreadID) (BotSessionBinding, bool, error) {
	raw, found, err := readWorkspaceFile(ctx, workspaceAPIURL(), slackBindingPath(thread))
	if err != nil || !found {
		return BotSessionBinding{}, false, err
	}
	var binding BotSessionBinding
	if err := json.Unmarshal([]byte(raw), &binding); err != nil {
		return binding, false, err
	}
	return binding, binding.SessionID != "", nil
}

// HasOtherBotSessionBinding reports whether another Slack app holds a
// durable conversation in this thread (on another destination). Plain replies in a thread shared by
// several bots are ambiguous, so none of them answers without a tag.
func (s *SlackService) HasOtherBotSessionBinding(ctx context.Context, thread ThreadID, ownRouteKey string) bool {
	curCfg := s.slackConfig()
	if s == nil || curCfg == nil {
		return false
	}
	ids := []string{""}
	defaultID := defaultSlackConnectionID(curCfg)
	for _, conn := range curCfg.Connections {
		if conn.Enabled && conn.ID != defaultID {
			ids = append(ids, conn.ID)
		}
	}
	for _, id := range ids {
		if id == thread.ConnectionID {
			continue
		}
		peer := thread
		peer.ConnectionID = id
		// A legacy three-part binding with this app's own route key is this
		// app's pre-upgrade session, not another bot.
		if binding, found, err := loadSlackBinding(ctx, peer); err == nil && found && binding.RouteKey != ownRouteKey {
			return true
		}
	}
	return false
}
func (s *SlackService) SaveBotSessionBinding(ctx context.Context, thread ThreadID, binding BotSessionBinding) error {
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	return writeWorkspaceFile(ctx, workspaceAPIURL(), slackBindingPath(thread), string(raw))
}
func (s *SlackService) ClearBotSessionBinding(ctx context.Context, thread ThreadID) error {
	return s.SaveBotSessionBinding(ctx, thread, BotSessionBinding{})
}
