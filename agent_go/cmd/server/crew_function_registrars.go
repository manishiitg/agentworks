package server

import (
	"context"
	"time"
)

// crewFunctionResultOnlyRegistrar admits progress reporting on a function
// invocation received by a guest, without granting callable agent functions
// or a manual completion tool.
type crewFunctionResultOnlyRegistrar struct{ definitionToolRegistrar }

// crewFunctionCallOnlyRegistrar admits the outbound function-call tools only: a shared caller's turn (a function call or an
// `ask` to the Crew, which runs with the owner's authority, 856-59) may discover and call the functions of other Crews and
// workflows, but never define or delete functions (function definitions are owner-only) or send free messages.
type crewFunctionCallOnlyRegistrar struct{ definitionToolRegistrar }

var crewFunctionCallTools = map[string]bool{"project_functions_list": true, "project_functions_call": true, "project_functions_get_call": true, "project_functions_reply": true}

func (r crewFunctionCallOnlyRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), category string) error {
	if !crewFunctionCallTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, exec, category)
}

func (r crewFunctionCallOnlyRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if !crewFunctionCallTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, exec, timeout, category)
}

// crewFunctionListOnlyRegistrar gives a guest's read-only ask the one read-only function tool: knowing what a Crew can do
// is the point of an ask, and the functions are how a guest acts. It cannot call, define or delete a function.
type crewFunctionListOnlyRegistrar struct{ definitionToolRegistrar }

func (r crewFunctionListOnlyRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), category string) error {
	if name != "project_functions_list" {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, exec, category)
}

func (r crewFunctionListOnlyRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if name != "project_functions_list" {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, exec, timeout, category)
}

var crewFunctionResultTools = map[string]bool{"report_function_progress": true}

func (r crewFunctionResultOnlyRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), category string) error {
	if !crewFunctionResultTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, exec, category)
}

func (r crewFunctionResultOnlyRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if !crewFunctionResultTools[name] {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, exec, timeout, category)
}
