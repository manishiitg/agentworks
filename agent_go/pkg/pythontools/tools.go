// Package pythontools binds user-authored Python functions to the existing
// custom-tool registry. Execution is always supplied by the owning agent's
// guarded shell executor; this package never starts a host process.
package pythontools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const Category = "python_tools"
const maxInputBytes = 64 * 1024

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type ReadFile func(context.Context, string) (string, error)
type Executor = func(context.Context, map[string]interface{}) (string, error)

// Tool metadata lives beside main.py in code/tools/<name>/. The directory name
// is the callable name; it cannot provide an arbitrary path or shell command.
type Tool struct {
	Name           string                 `json:"-"`
	Description    string                 `json:"description"`
	Parameters     map[string]interface{} `json:"parameters"`
	TimeoutSeconds int                    `json:"timeout_seconds,omitempty"`
	validator      *jsonschema.Schema
}

func (t Tool) Directory() string { return path.Join("code", "tools", t.Name) }

// SelectedNames requires explicit per-tool selection. A wildcard would silently
// give existing agents new capabilities when the builder creates another tool.
func SelectedNames(selections []string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, selection := range selections {
		name, selected := strings.CutPrefix(selection, Category+":")
		if !selected {
			continue
		}
		if !namePattern.MatchString(name) || strings.HasPrefix(name, "mcp_") {
			return nil, fmt.Errorf("invalid Python tool selection %q: use python_tools:<name> with a lowercase identifier, no wildcard", selection)
		}
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names, nil
}

// Load validates selected definitions and requires saved, nonempty Python
// source. It does not execute/import user code while authoring or publishing.
func Load(ctx context.Context, selections []string, read ReadFile) ([]Tool, error) {
	names, err := SelectedNames(selections)
	if err != nil {
		return nil, err
	}
	var tools []Tool
	for _, name := range names {
		directory := path.Join("code", "tools", name)
		raw, err := read(ctx, directory+"/tool.json")
		if err != nil {
			return nil, fmt.Errorf("Python tool %q: read tool.json: %w", name, err)
		}
		if len(raw) > 64*1024 {
			return nil, fmt.Errorf("Python tool %q: tool.json exceeds 64 KiB", name)
		}
		var tool Tool
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&tool); err != nil {
			return nil, fmt.Errorf("Python tool %q: invalid tool.json: %w", name, err)
		}
		if err := decoder.Decode(new(interface{})); err != io.EOF {
			return nil, fmt.Errorf("Python tool %q: tool.json must contain one JSON object", name)
		}
		tool.Name = name
		if strings.TrimSpace(tool.Description) == "" || len(tool.Description) > 4096 {
			return nil, fmt.Errorf("Python tool %q: description must contain 1–4096 bytes", name)
		}
		if tool.Parameters["type"] != "object" {
			return nil, fmt.Errorf("Python tool %q: parameters must be an object JSON schema", name)
		}
		if tool.TimeoutSeconds == 0 {
			tool.TimeoutSeconds = 60
		}
		if tool.TimeoutSeconds < 1 || tool.TimeoutSeconds > 300 {
			return nil, fmt.Errorf("Python tool %q: timeout_seconds must be between 1 and 300", name)
		}
		compiler := jsonschema.NewCompiler()
		// Only embedded metaschemas and refs within this schema are permitted.
		// Never read server files or fetch URLs from an authored $ref.
		compiler.UseLoader(jsonschema.SchemeURLLoader{})
		const uri = "https://agentworks.invalid/python-tool.json"
		if err := compiler.AddResource(uri, tool.Parameters); err != nil {
			return nil, fmt.Errorf("Python tool %q: invalid parameters schema: %w", name, err)
		}
		tool.validator, err = compiler.Compile(uri)
		if err != nil {
			return nil, fmt.Errorf("Python tool %q: invalid parameters schema: %w", name, err)
		}
		source, err := read(ctx, directory+"/main.py")
		if err != nil {
			return nil, fmt.Errorf("Python tool %q: read main.py: %w", name, err)
		}
		if strings.TrimSpace(source) == "" || len(source) > 1024*1024 {
			return nil, fmt.Errorf("Python tool %q: main.py must contain 1 byte–1 MiB", name)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// Logs remain stderr. Only run(input)'s returned value reaches the model as
// JSON. -B avoids writing __pycache__ beside immutable published source.
const runner = `import base64, contextlib, importlib.util, json, sys
source, payload = sys.argv[1:3]
with contextlib.redirect_stdout(sys.stderr):
    sys.path.insert(0, str(__import__("pathlib").Path(source).parent))
    spec = importlib.util.spec_from_file_location("agentworks_custom_tool", source)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    value = module.run(json.loads(base64.b64decode(payload)))
    result = json.dumps(value, ensure_ascii=False, allow_nan=False)
print(result)
`

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Bind captures an already guarded, step-specific shell executor. Arguments
// never supply paths, environment, commands or timeouts to that executor.
func (t Tool) Bind(shell Executor, sourcePath, workingDirectory string) Executor {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("Python tool %q: invalid arguments: %w", t.Name, err)
		}
		if len(raw) > maxInputBytes {
			return "", fmt.Errorf("Python tool %q: arguments exceed 64 KiB", t.Name)
		}
		var normalized interface{}
		if err := json.Unmarshal(raw, &normalized); err != nil {
			return "", err
		}
		if err := t.validator.Validate(normalized); err != nil {
			return "", fmt.Errorf("Python tool %q: arguments do not match parameters schema: %w", t.Name, err)
		}
		response, err := shell(ctx, map[string]interface{}{
			"command":           "python3 -B -c " + quote(runner) + " " + quote(sourcePath) + " " + quote(base64.StdEncoding.EncodeToString(raw)),
			"working_directory": workingDirectory,
			"timeout":           t.TimeoutSeconds,
			"use_shell":         true,
		})
		if err != nil {
			return "", fmt.Errorf("Python tool %q: %w", t.Name, err)
		}
		var result struct {
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode *int   `json:"exit_code"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal([]byte(response), &result); err != nil || result.ExitCode == nil {
			return "", fmt.Errorf("Python tool %q: invalid sandbox response", t.Name)
		}
		if *result.ExitCode != 0 || result.Error != "" {
			return "", fmt.Errorf("Python tool %q failed (exit %d): %s %s", t.Name, *result.ExitCode, result.Error, strings.TrimSpace(result.Stderr))
		}
		value := strings.TrimSpace(result.Stdout)
		if !json.Valid([]byte(value)) {
			return "", fmt.Errorf("Python tool %q did not return complete JSON (output may exceed the shared shell limit)", t.Name)
		}
		return value, nil
	}
}
