package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/mcpagent/llm"
)

// installProviderAccountConfigHook routes every InitializeLLM through the
// server-account admission, including the agent's own re-initializations
// (continuation relaunch, model switch) that never pass through handleQuery.
func installProviderAccountConfigHook() {
	llm.ConfigHook = llmguard.WithServerAccountAdmission
}
