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
	event := PythonRunEvidence{Kind: action, At: now.Format(time.RFC3339Nano)}
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
	return hcpo.writePythonHelperState(saveCtx, path.Join(hcpo.pythonHelperStateRoot(), "evidence", name), string(data))
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
		item.Name, item.Detail, item.SourceCall = boundedRunEvidenceText(item.Name), boundedRunEvidenceText(item.Detail), boundedRunEvidenceText(item.SourceCall)
		items = append(items, item)
	}
	return items, nil
}
