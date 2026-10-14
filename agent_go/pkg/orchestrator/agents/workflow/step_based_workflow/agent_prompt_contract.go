package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const AgentDescriptionFirstContractVersion = "1.0.48"
const AgentDescriptionFirstPromptContract = "description-first-v1"
const AgentDescriptionTurnID = "__step_description__"

func agentDescriptionFirst(step *AgentPlanStep) bool {
	return step != nil && !step.AuthoredPrompt && step.PromptContract == AgentDescriptionFirstPromptContract
}

// New authoring opts in. Reading or partially editing saved agents never does:
// their first items may perform external actions and require semantic migration.
func initializeNewAgentPromptContracts(step PlanStepInterface) {
	agent, ok := step.(*AgentPlanStep)
	if !ok || agent == nil {
		return
	}
	normalizeAgentTaskRules(agent)
	if !agent.AuthoredPrompt && agent.PromptContract == "" {
		agent.PromptContract = AgentDescriptionFirstPromptContract
	}
	for _, route := range agent.PredefinedRoutes {
		initializeNewAgentPromptContracts(route.SubAgentStep)
	}
}

func agentDescriptionItem(step *AgentPlanStep) AgentItem {
	item := AgentItem{ID: AgentDescriptionTurnID, Type: "user_message", Kind: "execution", Message: agentTaskDescription(step), Synthetic: true}
	if step.InitialWriteAccess != nil {
		item.WriteAccess = *step.InitialWriteAccess
	}
	return item
}

// Retain older system_rules as task content without copying them into the system.
// New/updated plans store this content only in the description's Rules section.
func agentTaskDescription(step *AgentPlanStep) string {
	if step == nil {
		return ""
	}
	return mergeTaskRules(step.Description, step.SystemRules)
}

func normalizeAgentTaskRules(step *AgentPlanStep) {
	if step == nil || step.AuthoredPrompt {
		return
	}
	step.Description = agentTaskDescription(step)
	step.SystemRules = ""
}

func mergeTaskRules(description, rules string) string {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return description
	}
	lines := strings.Split(description, "\n")
	start, end := -1, len(lines)
	fence := ""
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~") {
			n := strings.IndexFunc(text, func(r rune) bool { return r != rune(text[0]) })
			if n < 0 {
				n = len(text)
			}
			marker := text[:n]
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(text[n:]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		heading := descriptionSectionHeading.FindStringSubmatch(text)
		if heading == nil {
			continue
		}
		level := len(text) - len(strings.TrimLeft(text, "#"))
		if start >= 0 && level <= 2 {
			end = i
			break
		}
		if level == 2 && strings.EqualFold(strings.TrimSpace(heading[1]), "Rules") {
			start = i + 1
		}
	}
	if start < 0 {
		var missing []string
		text := strings.Join(strings.Fields(description), " ")
		for _, block := range strings.Split(rules, "\n\n") {
			if block = strings.TrimSpace(block); block != "" && !strings.Contains(text, strings.Join(strings.Fields(block), " ")) {
				missing = append(missing, block)
			}
		}
		if len(missing) == 0 {
			return description
		}
		return strings.TrimRight(description, "\n") + "\n\n## Rules\n" + strings.Join(missing, "\n\n")
	}
	body := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	normalize := func(text string) string { return strings.Join(strings.Fields(text), " ") }
	for _, block := range strings.Split(rules, "\n\n") {
		if block = strings.TrimSpace(block); block != "" && !strings.Contains(normalize(body), normalize(block)) {
			if body != "" {
				body += "\n\n"
			}
			body += block
		}
	}
	if normalize(body) == normalize(strings.Join(lines[start:end], "\n")) {
		return description
	}
	rebuilt := append(append([]string{}, lines[:start]...), body, "")
	rebuilt = append(rebuilt, lines[end:]...)
	return strings.Join(rebuilt, "\n")
}

// Apply the same authority layout to ordinary, delegating and preview renderers.
// The executable description is in history, not replayed as system instructions.
func applyAgentPromptContract(vars map[string]string, step *AgentPlanStep, values map[string]string) {
	if !agentDescriptionFirst(step) {
		return
	}
	vars["DescriptionFirst"] = "true"
	vars["StepDescription"] = ""
	// Task rules are carried once by the opening user description.
	vars["StepSystemRules"] = ""
}

// A stamp proves all live agent definitions were reviewed, including routes and
// orphans. It does not infer policy or delete execution items from their text.
func ValidateAgentDescriptionFirstContractStamp(content string) error {
	if err := ValidateAgentStepContractStamp(content); err != nil {
		return err
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return err
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		return err
	}
	var check func(PlanStepInterface) error
	check = func(step PlanStepInterface) error {
		agent, ok := step.(*AgentPlanStep)
		if !ok {
			return nil
		}
		if !agent.AuthoredPrompt && !agentDescriptionFirst(agent) {
			return fmt.Errorf("agent %q requires reviewed description-first-v1 migration before stamping %s", agent.ID, AgentDescriptionFirstContractVersion)
		}
		if agentDescriptionFirst(agent) && len(agent.Items) > 0 {
			first := agent.Items[0]
			if strings.TrimSpace(first.RetainedReason) == "" {
				return fmt.Errorf("agent %q: first retained item %q needs retained_reason explaining its reviewed subsequent phase and protection against repeating initial work before stamping", agent.ID, first.ID)
			}
			if _, narrowed := requestedAgentItemWriteAccess(first); narrowed && agent.InitialWriteAccess == nil {
				return fmt.Errorf("agent %q: explicitly review initial_write_access before stamping when its first follow-up narrows stores", agent.ID)
			}
			if first.Type == "scripted" && strings.TrimSpace(first.Message) == "" {
				return fmt.Errorf("agent %q: script-first migration needs a message explaining its reviewed subsequent-phase ordering; prerequisites must move upstream before stamping", agent.ID)
			}
		}
		if err := validateAgentStepFieldsTyped(agent); err != nil {
			return err
		}
		if agentDescriptionFirst(agent) && strings.TrimSpace(agent.Description) == "" {
			return fmt.Errorf("agent %q requires an executable description", agent.ID)
		}
		for _, route := range agent.PredefinedRoutes {
			if err := check(route.SubAgentStep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, steps := range [][]PlanStepInterface{plan.Steps, plan.OrphanSteps} {
		for _, step := range steps {
			if err := check(step); err != nil {
				return err
			}
		}
	}
	return nil
}

// A saved conversation from before task rules moved roles can lack that user
// context. Supply only missing content alongside the requested continuation;
// never restart the configured queue. New sessions already have the opener.
func missingAgentTaskContext(step *AgentPlanStep, history []llmtypes.MessageContent) string {
	var user strings.Builder
	for _, message := range history {
		if message.Role != llmtypes.ChatMessageTypeHuman {
			continue
		}
		for _, part := range message.Parts {
			if text, ok := part.(llmtypes.TextContent); ok {
				user.WriteString(text.Text)
				user.WriteByte('\n')
			}
		}
	}
	normalized := strings.Join(strings.Fields(user.String()), " ")
	description := strings.TrimSpace(step.Description)
	if description != "" && !strings.Contains(normalized, strings.Join(strings.Fields(description), " ")) {
		return agentTaskDescription(step)
	}
	var missing []string
	for _, block := range strings.Split(strings.TrimSpace(step.SystemRules), "\n\n") {
		if block = strings.TrimSpace(block); block != "" && !strings.Contains(normalized, strings.Join(strings.Fields(block), " ")) {
			missing = append(missing, block)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "## Rules\n" + strings.Join(missing, "\n\n")
}
