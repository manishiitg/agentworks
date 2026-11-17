package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The steps written in a workflow's Python files, read from the code without running it. The Tasks tab uses them to
// list every step of a task before it has run, with what each step does.

type crewTaskStep struct {
	Title string `json:"title"`
	// Description is the first line of the step function's docstring.
	Description string `json:"description,omitempty"`
	// Kind is "agent" when the function calls agent(...), otherwise "script".
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

var (
	stepDecoratorPattern = regexp.MustCompile(`^@step\(\s*(?:title\s*=\s*)?("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')\s*\)\s*$`)
	stepDefPattern       = regexp.MustCompile(`^(?:async\s+)?def\s+\w+\s*\(`)
	stepAgentCall        = regexp.MustCompile(`(^|[^\w.])agent\s*\(`)
)

// crewTaskStepsOf lists the @step functions of one Python file in the order they are written.
func crewTaskStepsOf(source string) []crewTaskStep {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	steps := []crewTaskStep{}
	for i := 0; i < len(lines); i++ {
		match := stepDecoratorPattern.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		title := match[1]
		if unquoted, err := strconv.Unquote(title); err == nil && title[0] == '"' {
			title = unquoted
		} else {
			title = title[1 : len(title)-1]
		}
		title = strings.TrimSpace(title)
		def := i + 1
		for def < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[def]), "@") {
			def++ // other decorators between @step and def
		}
		if title == "" || def >= len(lines) || !stepDefPattern.MatchString(lines[def]) {
			continue
		}
		// The signature may span lines; its last line ends with a colon.
		bodyStart := def
		for bodyStart < len(lines) && !strings.HasSuffix(strings.TrimSpace(stripPythonComment(lines[bodyStart])), ":") {
			bodyStart++
		}
		bodyStart++
		body := []string{}
		for j := bodyStart; j < len(lines); j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed != "" && !strings.HasPrefix(lines[j], " ") && !strings.HasPrefix(lines[j], "\t") && !strings.HasPrefix(trimmed, "#") {
				break // the next top-level statement ends this function
			}
			body = append(body, lines[j])
		}
		kind := "script"
		for _, line := range body {
			if stepAgentCall.MatchString(stripPythonComment(line)) {
				kind = "agent"
				break
			}
		}
		steps = append(steps, crewTaskStep{Title: title, Description: firstDocstringLine(body), Kind: kind, Line: i + 1})
	}
	return steps
}

func stripPythonComment(line string) string {
	if at := strings.Index(line, "#"); at >= 0 && !strings.ContainsAny(line[:at], `"'`) {
		return line[:at]
	}
	return line
}

// firstDocstringLine returns the first sentence-line of a function's docstring, or "" when it has none.
func firstDocstringLine(body []string) string {
	for _, line := range body {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, quote := range []string{`"""`, `'''`, `"`, `'`} {
			if !strings.HasPrefix(trimmed, quote) {
				continue
			}
			text := strings.TrimPrefix(trimmed, quote)
			if end := strings.Index(text, quote); end >= 0 {
				text = text[:end]
			}
			text = strings.TrimSpace(text)
			if len(text) > 300 {
				text = text[:300]
			}
			return text
		}
		return "" // the first statement is code, so there is no docstring
	}
	return ""
}

// handleCrewTaskSteps returns, for each Python file of the workflow, the steps written in it. Like the script outline,
// it is for the Crew's Builder.
func (api *StreamingAPI) handleCrewTaskSteps(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok {
		http.Error(w, "This Crew is unavailable.", http.StatusNotFound)
		return
	}
	resource := &crewWorkflowResources{userID: owner}
	source, err := resource.ResolveCrewScript(r.Context(), binding.Root, "")
	if err != nil {
		http.Error(w, "Could not read this workflow's code.", http.StatusBadRequest)
		return
	}
	files := map[string][]crewTaskStep{}
	for name, content := range source.Files {
		if strings.HasSuffix(name, ".py") {
			if steps := crewTaskStepsOf(string(content)); len(steps) > 0 {
				files[name] = steps
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"files": files})
}
