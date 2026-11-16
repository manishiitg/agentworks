//go:build !linux

package clisandbox

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

func flushClaudeCredentialSync(*llmtypes.CLISecurityPolicy) {}
func beginClaudeCredentialSync(*llmtypes.CLISecurityPolicy, string) (func(), error) {
	return func() {}, nil
}
