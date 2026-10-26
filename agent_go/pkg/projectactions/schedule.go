// Package projectactions defines the saved actions used by every product caller.
// Runtime permissions and execution remain with the target's existing service.
package projectactions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

type Project struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Action separates the two things a caller can do, without selecting a second
// execution mode. Conversation is only the Crew's existing isolated destination.
type Action struct {
	Type         string `json:"type"`
	Tab          string `json:"tab,omitempty"`
	Function     string `json:"function,omitempty"`
	Conversation string `json:"conversation,omitempty"`
}

type After struct {
	Schedules    []string `json:"schedules"`
	Outcome      string   `json:"outcome,omitempty"`
	DelayMinutes int      `json:"delay_minutes,omitempty"`
	Deadline     string   `json:"deadline,omitempty"`
}

type Date struct {
	ID    string          `json:"id,omitempty"`
	Date  string          `json:"date"`
	Time  string          `json:"time"`
	Input json.RawMessage `json:"input,omitempty"`
}

type When struct {
	Cron     string `json:"cron,omitempty"`
	Timezone string `json:"timezone"`
	Dates    []Date `json:"dates,omitempty"`
	After    *After `json:"after,omitempty"`
	// Anchor prevents a never-run timer moving its first occurrence on each tick.
	Anchor string `json:"anchor,omitempty"`
}

type Schedule struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Project Project         `json:"project"`
	When    When            `json:"when"`
	Action  Action          `json:"action"`
	Input   json.RawMessage `json:"input"`
	RunAs   string          `json:"run_as"`
	Enabled bool            `json:"enabled"`
}

type Schedules struct {
	Schedules []Schedule `json:"schedules"`
}

// CronSchedule is the same clock contract for every product adapter.
func (w When) CronSchedule() (cron.Schedule, error) {
	tz := w.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, err
	}
	return cron.ParseStandard("CRON_TZ=" + tz + " " + w.Cron)
}

func (s Schedule) Validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Project.ID) == "" {
		return fmt.Errorf("schedule identity, name and project are required")
	}
	if strings.TrimSpace(s.RunAs) == "" {
		return fmt.Errorf("schedule caller is required")
	}
	if s.Project.Kind != "goal" && s.Project.Kind != "crew" && s.Project.Kind != "relay" {
		return fmt.Errorf("unknown schedule product")
	}
	if s.Action.Type != "run" && s.Action.Type != "message" {
		return fmt.Errorf("schedule action must be run or message")
	}
	if s.Project.Kind == "relay" && s.Action.Type == "message" {
		return fmt.Errorf("a Relay schedule runs the Relay; it does not send a chat message")
	}
	var input struct {
		Condition *struct {
			CheckMode string `json:"check_mode"`
		} `json:"condition"`
	}
	_ = json.Unmarshal(s.Input, &input)
	eventOnly := input.Condition != nil && (input.Condition.CheckMode == "event" || input.Condition.CheckMode == "inbox")
	if !eventOnly && (strings.TrimSpace(s.When.Cron) == "") == (len(s.When.Dates) == 0) {
		return fmt.Errorf("choose a cron time or calendar dates")
	}
	if !json.Valid(s.Input) {
		return fmt.Errorf("schedule input must be JSON")
	}
	return nil
}
