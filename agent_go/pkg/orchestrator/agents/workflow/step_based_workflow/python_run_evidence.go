package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PythonRunEvidence is the public projection of one completed check or notice.
// Arguments, helper results, credentials and private continuation state stay out.
type PythonRunEvidence struct {
	ID         string `json:"id,omitempty"`
	Kind       string `json:"kind"`
	At         string `json:"at"`
	Name       string `json:"name,omitempty"`
	OK         *bool  `json:"ok,omitempty"`
	Detail     string `json:"detail"`
	SourceCall string `json:"source_call,omitempty"`
}

func boundedRunEvidenceText(value string) string {
	runes := []rune(redaction.Text(value))
	if len(runes) > 500 {
		return string(runes[:499]) + "…"
	}
	return string(runes)
}

func (hcpo *StepBasedWorkflowOrchestrator) savePythonRunEvidence(ctx context.Context, id, action string, arguments map[string]interface{}, reply pythonHelperReply) error {
	if action != "check" && action != "notify" {
		return nil
	}
	now := time.Now().UTC()
	event := PythonRunEvidence{ID: id, Kind: action, At: now.Format(time.RFC3339Nano)}
	event.SourceCall, _ = arguments["source_call"].(string)
	if action == "check" {
		ok, valid := arguments["ok"].(bool)
		event.Name, _ = arguments["name"].(string)
		if !valid || event.Name == "" {
			return nil
		}
		event.OK = &ok
		event.Detail, _ = arguments["detail"].(string)
	} else {
		event.Detail, _ = arguments["text"].(string)
		if !reply.OK {
			event.Detail = reply.Error
		}
	}
	clean := boundedRunEvidenceText
	event.Name, event.Detail, event.SourceCall = clean(event.Name), clean(event.Detail), clean(event.SourceCall)
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	// A separate file per call retains repeats and redo attempts. Transport
	// duplicates return the existing receipt before reaching this write.
	name := fmt.Sprintf("%020d-%s.json", now.UnixNano(), id)
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := hcpo.writePythonHelperState(saveCtx, path.Join(hcpo.pythonHelperStateRoot(), "evidence", name), string(data)); err != nil {
		return err
	}
	return hcpo.upsertRunMetadata(saveCtx, hcpo.selectedRunFolder, func(meta map[string]interface{}) {
		items, _ := meta["python_evidence"].([]interface{})
		items = append(items, event)
		if len(items) > 200 {
			items = items[len(items)-200:]
		}
		meta["python_evidence"] = items
	})
}

// PythonStepEvidence records actual calls independently of a chat's live notifier.
// The enclosing run's execution_id identifies the producing attempt.
type PythonStepEvidence struct {
	ExecutionID string `json:"execution_id,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceCall  string `json:"source_call"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	LogNote     string `json:"log_note"`
}

func (hcpo *StepBasedWorkflowOrchestrator) savePythonStepEvidence(ctx context.Context, event PythonStepEvidence) error {
	event.Name = boundedRunEvidenceText(event.Name)
	event.SourceCall = boundedRunEvidenceText(event.SourceCall)
	event.Detail = boundedRunEvidenceText(event.Detail)
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return hcpo.upsertRunMetadata(saveCtx, hcpo.selectedRunFolder, func(meta map[string]interface{}) {
		steps, _ := meta["python_steps"].(map[string]interface{})
		if steps == nil {
			steps = map[string]interface{}{}
		}
		event.ExecutionID, _ = meta["execution_id"].(string)
		steps[event.ID] = event
		meta["python_steps"] = steps
	})
}

// ReadPythonRunEvidence is called only after the server authorizes the workspace.
// Anchored reads keep an event or planted link inside this run's owning storage.
func ReadPythonRunEvidence(docs, workspace, runFolder string) ([]PythonRunEvidence, error) {
	items := []PythonRunEvidence{}
	if err := ValidateWorkflowRunFolder(runFolder); err != nil {
		return nil, err
	}
	view := workflowfiles.WorkflowFS{Docs: docs, Root: workspace}
	directory := path.Join("runs", runFolder, "helpers", "evidence")
	entries, err := fs.ReadDir(view, directory)
	if errors.Is(err, fs.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) > 200 {
		names = names[len(names)-200:]
	}
	for _, name := range names {
		file, err := view.Open(path.Join(directory, name))
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 16385))
		file.Close()
		if readErr != nil {
			return nil, readErr
		}
		var item PythonRunEvidence
		if len(raw) > 16384 || json.Unmarshal(raw, &item) != nil || (item.Kind != "check" && item.Kind != "notify") {
			return nil, fmt.Errorf("run evidence is unreadable")
		}
		if _, err := time.Parse(time.RFC3339Nano, item.At); err != nil {
			return nil, fmt.Errorf("run evidence has no timestamp")
		}
		if item.Kind == "check" && (item.OK == nil || item.Name == "") {
			return nil, fmt.Errorf("check evidence is incomplete")
		}
		if item.Kind == "notify" {
			item.OK = nil
			item.Name = ""
		}
		item.ID, item.Name, item.Detail, item.SourceCall = boundedRunEvidenceText(item.ID), boundedRunEvidenceText(item.Name), boundedRunEvidenceText(item.Detail), boundedRunEvidenceText(item.SourceCall)
		items = append(items, item)
	}
	return items, nil
}
