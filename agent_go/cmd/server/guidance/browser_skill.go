package guidance

import (
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The agent-browser skill points saved browser suites at its own
// references/playwright-scripted.md. Ship that guide inside the skill so every
// product with browser access has it, not only chats that also get
// builder-reference.
func init() {
	content, err := renderReferenceKind("playwright-scripted", tmplData{})
	if err != nil {
		panic(fmt.Sprintf("render playwright-scripted for agent-browser: %v", err))
	}
	if err := skills.AddBuiltinSupportingFile("agent-browser", "references/playwright-scripted.md", content); err != nil {
		panic(fmt.Sprintf("attach playwright-scripted to agent-browser: %v", err))
	}
}
