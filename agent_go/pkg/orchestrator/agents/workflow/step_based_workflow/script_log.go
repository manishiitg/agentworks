package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type scriptLogKey struct{}
type scriptLogReceipt struct {
	LogFile         string `json:"log_file"`
	LogError        string `json:"log_error,omitempty"`
	LogComplete     bool   `json:"log_complete"`
	OutputBytes     int    `json:"output_bytes"`
	OutputTruncated bool   `json:"output_truncated"`
	apiPath         string
	value           string
	dispatched      bool
}

func (r *scriptLogReceipt) result(value string, err error) string {
	var decoded interface{} = value
	if json.Valid([]byte(value)) {
		_ = json.Unmarshal([]byte(value), &decoded)
	}
	reply := struct {
		Value interface{} `json:"value"`
		OK    bool        `json:"ok"`
		Error string      `json:"error,omitempty"`
		*scriptLogReceipt
	}{Value: decoded, OK: err == nil, scriptLogReceipt: r}
	if err != nil {
		reply.Error = err.Error()
	}
	data, _ := json.Marshal(reply)
	return string(data)
}

// One invocation owns one file. A pre-launch refusal gets a diagnostic file;
// after dispatch only the shell service writes it, including after cancellation.
func (hcpo *StepBasedWorkflowOrchestrator) prepareScriptLog(ctx context.Context, outputDir string) (context.Context, *scriptLogReceipt) {
	r, _ := ctx.Value(scriptLogKey{}).(*scriptLogReceipt)
	if r == nil {
		r = &scriptLogReceipt{}
	}
	r.LogError = ""
	r.LogComplete = false
	r.dispatched = false
	r.OutputBytes = 0
	r.OutputTruncated = false
	r.apiPath = filepath.ToSlash(filepath.Join(outputDir, fmt.Sprintf("script-output-%d.log", time.Now().UnixNano())))
	r.LogFile = workflowAbsPath(r.apiPath)
	return context.WithValue(ctx, scriptLogKey{}, r), r
}

func (hcpo *StepBasedWorkflowOrchestrator) finishScriptLog(ctx context.Context, receipt *scriptLogReceipt, result *ScriptedFastPathResult) {
	if result == nil {
		return
	}
	if !receipt.dispatched {
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		reason := result.Error
		if reason == "" {
			reason = "saved main.py was not launched"
		}
		if hcpo.WorkspaceClient == nil {
			receipt.LogError = "full log unavailable: no workspace client"
		} else if err := hcpo.WriteWorkspaceFile(logCtx, receipt.apiPath, time.Now().UTC().Format(time.RFC3339Nano)+" [process] not started: "+reason+"\n"); err != nil {
			receipt.LogError = "full log unavailable: " + err.Error()
		} else {
			receipt.LogComplete = true
		}
	}
	result.OutputBytes = receipt.OutputBytes
	result.OutputTruncated = receipt.OutputTruncated
	result.LogFile = receipt.LogFile
	result.LogError = receipt.LogError
	result.LogComplete = receipt.LogComplete
}

// PLAT-746: full stdout/stderr lives in the streamed log. Metadata, UI events,
// inspection text and subsequent agent turns receive only a head/tail preview.
const scriptOutputHeadBytes = 2 * 1024
const scriptOutputTailBytes = 8 * 1024
const scriptTracebackBytes = 16 * 1024

func scriptOutputPreview(output, logFile string) string {
	if len(output) <= scriptOutputHeadBytes+scriptOutputTailBytes {
		return output
	}
	tail := len(output) - scriptOutputTailBytes
	if traceback := strings.LastIndex(output, "Traceback (most recent call last):"); traceback >= 0 && len(output)-traceback <= scriptTracebackBytes && traceback < tail {
		tail = traceback
	}
	if tail <= scriptOutputHeadBytes {
		return output
	}
	marker := fmt.Sprintf("\n... [output truncated; %d bytes total. Full output: %s. Read the log in bounded chunks; do not rerun.] ...\n", len(output), logFile)
	return strings.ToValidUTF8(output[:scriptOutputHeadBytes], "�") + marker + strings.ToValidUTF8(output[tail:], "�")
}
