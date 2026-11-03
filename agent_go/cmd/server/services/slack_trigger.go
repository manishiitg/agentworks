package services

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
	"github.com/slack-go/slack/slackevents"
	"strings"
)

// SlackTrigger is configured by the route owner, never by message payloads.
type SlackTrigger struct {
	InputPreset     string                           `json:"input_preset,omitempty"`
	Inputs          map[string]string                `json:"inputs,omitempty"`
	Match           *workflowtrigger.Match           `json:"match,omitempty"`
	PayloadMappings *workflowtrigger.PayloadMappings `json:"payload_mappings,omitempty"`
	Context         *SlackTriggerContext             `json:"context,omitempty"`
	Type            string                           `json:"type"` // human_message or trusted_app
	AppID           string                           `json:"app_id,omitempty"`
	BotID           string                           `json:"bot_id,omitempty"`
	Contains        string                           `json:"contains,omitempty"`
	RouteSelections map[string]string                `json:"route_selections,omitempty"`
	StepID          string                           `json:"step_id,omitempty"`
	GroupNames      []string                         `json:"group_names,omitempty"`
}
type SlackTriggerHandler func(context.Context, string, *slackevents.MessageEvent) error

func (s *SlackService) SetTriggerHandler(handler SlackTriggerHandler) {
	s.triggerHandler = handler
	s.propagateToChildren(func(child *SlackService) { child.triggerHandler = handler })
}
func SlackTriggerMatches(trigger *SlackTrigger, event *slackevents.MessageEvent, ownUserID string) bool {
	if trigger == nil || event == nil || event.TimeStamp == "" || event.User == ownUserID && ownUserID != "" || event.ThreadTimeStamp != "" && event.ThreadTimeStamp != event.TimeStamp {
		return false
	}
	if event.SubType != "" && event.SubType != "bot_message" {
		return false
	}
	switch trigger.Type {
	case "human_message":
		if event.BotID != "" || event.User == "" {
			return false
		}
	case "trusted_app":
		if event.BotID == "" || trigger.BotID == "" && trigger.AppID == "" {
			return false
		}
		if trigger.BotID != "" && event.BotID != trigger.BotID {
			return false
		}
		appID := ""
		if event.Message != nil && event.Message.BotProfile != nil {
			appID = event.Message.BotProfile.AppID
		}
		if trigger.AppID != "" && appID != trigger.AppID {
			return false
		}
	default:
		return false
	}
	if trigger.Contains != "" && !strings.Contains(event.Text, trigger.Contains) {
		return false
	}
	payload, err := json.Marshal(event)
	return err == nil && trigger.Match.Matches(payload)
}

// Old selectors must refuse even when explicitly null; omission means defaults.
func (t *SlackTrigger) UnmarshalJSON(data []byte) error {
	type plain SlackTrigger
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, old := fields["group_names"]; old {
		return fmt.Errorf("group selection is retired; use optional input_preset and inputs for one run")
	}
	*t = SlackTrigger(value)
	return nil
}

// Context is restricted to the triggering channel and excludes later messages.
type SlackTriggerContext struct {
	Limit           int  `json:"limit"`
	LookbackMinutes int  `json:"lookback_minutes"`
	IncludeThreads  bool `json:"include_threads,omitempty"`
}

func (c *SlackTriggerContext) Validate() error {
	if c == nil {
		return nil
	}
	if c.Limit < 1 || c.Limit > 100 || c.LookbackMinutes < 1 || c.LookbackMinutes > 1440 {
		return fmt.Errorf("Slack context requires limit 1–100 and lookback_minutes 1–1440")
	}
	return nil
}
