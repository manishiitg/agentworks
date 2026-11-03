package agent

import (
	"fmt"
	"log"
	"sort"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// logPromptSize records how large an agent's instructions, skills and direct tools are when the agent is
// finalized, and which instruction sections are largest. Every model step re-reads this text, so it explains
// token use and the wait before the first reply (PLAT-856, a person's Claude limit ran out fast).
func logPromptSize(sessionID, provider string, def mcpagent.AgentDefinition) {
	skillChars := 0
	for _, skill := range def.Skills {
		if skill != nil {
			skillChars += len(skill.Name) + len(skill.Description)
		}
	}
	toolChars := 0
	for _, tool := range def.Tools.Direct {
		toolChars += len(tool.Name) + len(tool.Description) + len(fmt.Sprint(tool.InputSchema))
	}
	type section struct {
		title string
		chars int
	}
	var sections []section
	current := section{title: "(start)"}
	for _, line := range strings.Split(def.Instructions, "\n") {
		trimmed := strings.TrimSpace(line)
		heading := strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") ||
			(strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "</") && strings.HasSuffix(trimmed, ">") && !strings.Contains(trimmed, " ") && len(trimmed) < 60)
		if heading && current.chars > 0 {
			sections = append(sections, current)
			current = section{title: trimmed}
		} else if heading {
			current.title = trimmed
		}
		current.chars += len(line) + 1
	}
	sections = append(sections, current)
	sort.Slice(sections, func(i, j int) bool { return sections[i].chars > sections[j].chars })
	top := make([]string, 0, 12)
	for i, s := range sections {
		if i == 12 {
			break
		}
		title := s.title
		if len(title) > 50 {
			title = title[:50]
		}
		top = append(top, fmt.Sprintf("%q:%d", title, s.chars))
	}
	log.Printf("[PROMPT_SIZE] session=%s provider=%s instructions_chars=%d sections=%d skills=%d skill_listing_chars=%d direct_tools=%d direct_tool_chars=%d largest=[%s]",
		sessionID, provider, len(def.Instructions), len(sections), len(def.Skills), skillChars, len(def.Tools.Direct), toolChars, strings.Join(top, " "))
}
