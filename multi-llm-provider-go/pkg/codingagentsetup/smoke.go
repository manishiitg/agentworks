package codingagentsetup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var expectedMCPTools = []string{
	"list_coding_agents",
	"list_coding_agent_models",
	"delegate_coding_agent",
	"get_coding_agent_job",
	"cancel_coding_agent_job",
}

func protocolSmokeTest(parent context.Context, binary string, providers, workspaces []string) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	tempDir, err := os.MkdirTemp("", "llm-provider-mcp-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"llm-provider-doctor","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	command := exec.CommandContext(ctx, binary)
	command.Stdin = strings.NewReader(input)
	command.Env = append(withoutEnvironment(os.Environ(),
		"LLM_PROVIDER_MCP_STATE",
		"LLM_PROVIDER_MCP_ALLOWED_PROVIDERS",
		"LLM_PROVIDER_MCP_WORKSPACE_ROOTS",
		"LLM_PROVIDER_MCP_DELEGATION_DEPTH",
	),
		"LLM_PROVIDER_MCP_STATE="+filepath.Join(tempDir, "jobs.db"),
		"LLM_PROVIDER_MCP_ALLOWED_PROVIDERS="+strings.Join(providers, ","),
	)
	if len(workspaces) > 0 {
		command.Env = append(command.Env, "LLM_PROVIDER_MCP_WORKSPACE_ROOTS="+strings.Join(workspaces, string(os.PathListSeparator)))
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("server did not finish: %w", ctx.Err())
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	response := stdout.String()
	for _, tool := range expectedMCPTools {
		if !strings.Contains(response, `"name":"`+tool+`"`) {
			return fmt.Errorf("tools/list did not include %s", tool)
		}
	}
	return nil
}

func withoutEnvironment(environment []string, names ...string) []string {
	omit := make(map[string]struct{}, len(names))
	for _, name := range names {
		omit[name] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, pair := range environment {
		name, _, _ := strings.Cut(pair, "=")
		if _, found := omit[name]; !found {
			result = append(result, pair)
		}
	}
	return result
}
