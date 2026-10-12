// Package executioninspection reads execution-local evidence from the durable
// transcript. It never reads today's plan or another attempt's mutable files.
package executioninspection

import (
	"strconv"
	"strings"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

type Script struct {
	Origin      string `json:"origin,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	Path        string `json:"path,omitempty"`
	Source      string `json:"source,omitempty"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	Error       string `json:"error,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Status      string `json:"status"`
}

func Records(transcript *orchevents.BackgroundAgentTranscript) (string, []Script) {
	mode := ""
	scripts := []Script{}
	byStart := map[string]int{}
	for _, event := range transcript.Events {
		if event.Type == "execution_mode" {
			if mode == "" && (event.Text == "agent" || event.Text == "script" || event.Text == "validation") {
				mode = event.Text
			}
			continue
		}
		if !strings.HasPrefix(event.Type, "script_") {
			continue
		}
		key := event.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00")
		index, exists := byStart[key]
		if !exists {
			index = len(scripts)
			byStart[key] = index
			scripts = append(scripts, Script{StartedAt: key, Status: "running"})
		}
		script := &scripts[index]
		switch event.Type {
		case "script_origin":
			script.Origin = event.Text
		case "script_source":
			script.Source = event.Text
		case "script_path":
			script.Path = event.Text
		case "script_stdout":
			script.Stdout = event.Text
		case "script_stderr":
			script.Stderr = event.Text
		case "script_error":
			script.Error = event.Text
		case "script_exit":
			if code, err := strconv.Atoi(event.Text); err == nil {
				script.ExitCode = &code
			}
		case "script_end":
			script.CompletedAt = event.Text
		case "script_status":
			script.Status = event.Text
		}
	}
	if mode == "" && len(scripts) > 0 {
		mode = "script"
	}
	// Older item headers carry their declared type in their recorded name.
	// No inference from the current workflow or the absence of a transcript.
	if mode == "" && transcript.Kind == "agent_item" {
		switch {
		case strings.HasSuffix(transcript.Name, "(user_message)"), strings.HasSuffix(transcript.Name, "(foreach)"):
			mode = "agent"
		case strings.HasSuffix(transcript.Name, "(scripted)"):
			mode = "script"
		case strings.HasSuffix(transcript.Name, "(prevalidation)"):
			mode = "validation"
		}
	}
	return mode, scripts
}
