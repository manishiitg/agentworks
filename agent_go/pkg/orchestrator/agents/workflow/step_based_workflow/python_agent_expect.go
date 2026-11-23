package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// pythonAgentExpectTries is how many correction turns an agent() call with expect= gets after its first answer
// fails the checks, before the call fails (owner, 23 Nov, #3616).
const pythonAgentExpectTries = 2

// pythonAgentExpect is agent(expect=...): the old plan steps' validation schema (files and database rows, checked
// by RunPreValidation without a model), plus JSON checks on the agent's final answer. Optional; a call without it
// behaves as before.
type pythonAgentExpect struct {
	Files  []FileValidationRule  `json:"files,omitempty"`
	DB     []DBValidationRule    `json:"db,omitempty"`
	Answer []JSONValidationCheck `json:"answer,omitempty"`
}

func parsePythonAgentExpect(arguments map[string]interface{}) (*pythonAgentExpect, error) {
	raw, present := arguments["expect"]
	if !present || raw == nil {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("expect must be a JSON object: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	var expect pythonAgentExpect
	if err := decoder.Decode(&expect); err != nil {
		return nil, fmt.Errorf("expect takes files, db and answer: %w", err)
	}
	if len(expect.Files) == 0 && len(expect.DB) == 0 && len(expect.Answer) == 0 {
		return nil, fmt.Errorf("expect needs at least one of files, db or answer")
	}
	// Reuse the schema's own unmarshal fixes (double-escaped patterns) and limits.
	schema := expect.schema("")
	if err := validateSchemaLimits(schema); err != nil {
		return nil, fmt.Errorf("expect: %w", err)
	}
	return &expect, nil
}

// schema turns expect into a ValidationSchema; answerFile is the run-relative JSON copy of the answer.
func (e *pythonAgentExpect) schema(answerFile string) *ValidationSchema {
	schema := &ValidationSchema{Files: append([]FileValidationRule{}, e.Files...), DB: e.DB}
	if len(e.Answer) > 0 && answerFile != "" {
		schema.Files = append(schema.Files, FileValidationRule{FileName: answerFile, MustExist: true, JSONChecks: e.Answer})
	}
	forEachSchemaCheck(schema, func(_ string, check *JSONValidationCheck) {
		if check.Pattern != "" {
			check.Pattern = fixDoubleEscapedPattern(check.Pattern)
		}
	})
	return schema
}

// pythonAgentAnswerJSON reads the answer as JSON: the whole text, or its last ```json fenced block.
func pythonAgentAnswerJSON(answer string) (interface{}, bool) {
	candidates := []string{strings.TrimSpace(answer)}
	if at := strings.LastIndex(answer, "```json"); at >= 0 {
		rest := answer[at+len("```json"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			candidates = append([]string{strings.TrimSpace(rest[:end])}, candidates...)
		}
	}
	for _, text := range candidates {
		var value interface{}
		if text != "" && json.Unmarshal([]byte(text), &value) == nil {
			return value, true
		}
	}
	return nil, false
}

// checkPythonAgentExpect runs the checks against the run folder and the answer. It returns the parsed answer (when
// answer checks were asked for) and the plain failure lines, empty when everything passed.
func (hcpo *StepBasedWorkflowOrchestrator) checkPythonAgentExpect(ctx context.Context, expect *pythonAgentExpect, runRoot, attempt string, try int, answer string) (interface{}, []string, error) {
	var data interface{}
	answerFile := ""
	if len(expect.Answer) > 0 {
		value, ok := pythonAgentAnswerJSON(answer)
		if !ok {
			return nil, []string{"Your final answer must be JSON (the whole answer, or a ```json block), so it can be checked."}, nil
		}
		data = value
		body, _ := json.Marshal(value)
		answerFile = fmt.Sprintf("helpers/answers/%s-expect-%d.json", attempt, try)
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := hcpo.writePythonHelperState(writeCtx, filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, answerFile)), string(body))
		cancel()
		if err != nil {
			return nil, nil, fmt.Errorf("cannot save the answer for checking: %w", err)
		}
	}
	schema := expect.schema(answerFile)
	results, err := RunPreValidation(ctx, schema, runRoot, hcpo.BaseOrchestrator)
	if err != nil {
		return nil, nil, err
	}
	SavePreValidationLog(ctx, hcpo.BaseOrchestrator, runRoot, attempt, attempt, results, schema, hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
		PreValidationAttempt{ExecutionMode: "python_agent", ValidationPhase: "expect", ExecutionAttempt: 1, ValidationAttempt: try + 1})
	if results.OverallPass {
		return data, nil, nil
	}
	var failures []string
	for _, e := range results.Summary.Errors {
		where := e.File
		if where == answerFile {
			where = "your answer"
		}
		line := strings.TrimSpace(e.Message)
		if e.Path != "" {
			line = e.Path + ": " + line
		}
		failures = append(failures, fmt.Sprintf("%s: %s", where, line))
	}
	if len(failures) == 0 {
		failures = []string{"The output did not pass its checks."}
	}
	return data, failures, nil
}

func pythonAgentCorrection(failures []string) string {
	return "Your work did not pass these checks:\n- " + strings.Join(failures, "\n- ") + "\nFix them and answer again."
}
