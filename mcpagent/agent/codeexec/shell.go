package codeexec

// The execute_shell_command schema and description below are production: the
// coding-agent bridge falls back to them when a host has not registered its own
// (see agent/coding_agents_bridge.go). The executor that used to sit here was
// only ever called by mcpagent's e2e tests and now lives in
// agent/codeexec/shellfixture.

// ShellCommandParams is the JSON-schema for execute_shell_command.
var ShellCommandParams = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"command": map[string]interface{}{
			"type":        "string",
			"description": "The shell command to execute",
		},
		"working_directory": map[string]interface{}{
			"type":        "string",
			"description": "Optional directory to run the command from. Must exist and be a directory.",
		},
	},
	"required": []string{"command"},
}

// ShellCommandDescription is the tool description for execute_shell_command.
const ShellCommandDescription = "Execute a shell command and return stdout, stderr, and exit code. Use this to run code, call HTTP endpoints with curl, or perform any shell operation."
