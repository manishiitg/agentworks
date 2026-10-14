package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResourceIntentContractVersion is the contract where every step description
// states, under "## Resources", how the step uses the workflow's knowledge
// base, database, Brain and learnings (or that it does not). It is a
// definition-only review: no grant is added, widened or moved.
const ResourceIntentContractVersion = "1.0.49"

type resourceKind struct {
	key      string
	label    string
	synonyms []string
}

var resourceKinds = []resourceKind{
	{"kb", "Knowledge base", []string{"knowledge base", "knowledgebase", "kb"}},
	{"database", "Database", []string{"database", "workflow database", "db"}},
	{"brain", "Brain", []string{"brain", "shared brain"}},
	{"learnings", "Learnings", []string{"learnings", "workflow learnings", "learning"}},
}

// descriptionSectionBody returns the body of the first level-2 section with
// the given heading, up to the next heading of the same or a higher level.
func descriptionSectionBody(description, heading string) (string, bool) {
	var body strings.Builder
	found := false
	for _, line := range strings.Split(description, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := descriptionSectionHeading.FindStringSubmatch(trimmed); m != nil {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if found && level <= 2 {
				break
			}
			if !found && level == 2 && strings.EqualFold(strings.TrimSpace(m[1]), heading) {
				found = true
				continue
			}
		}
		if found {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	return body.String(), found
}

// resourceIntentLine splits "- Knowledge base: reads ..." into a normalised
// label and the stated use. Bullets, bold and code marks around the label are
// ignored.
func resourceIntentLine(line string) (label, value string, ok bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*+ \t")
	line = strings.ReplaceAll(strings.ReplaceAll(line, "**", ""), "`", "")
	i, sep := -1, 0
	for pos, r := range line {
		if r == ':' || r == '—' || r == '–' {
			i, sep = pos, len(string(r))
			break
		}
	}
	if i <= 0 {
		return "", "", false
	}
	label = strings.ToLower(strings.Join(strings.Fields(strings.Trim(line[:i], "_ ")), " "))
	value = strings.TrimSpace(strings.Trim(line[i+sep:], "_ "))
	return label, value, label != ""
}

func statesNoUse(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"no use", "none", "not used", "unused", "n/a", "not applicable"} {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}

// referenceKindsNamed reports which resource kinds the description's own
// Inputs/Resources/Guides name. These are the same references that already
// grant read access, so they are objective evidence of use.
func referenceKindsNamed(description string) map[string]bool {
	named := map[string]bool{}
	for _, ref := range parseDescriptionReferences(description) {
		switch {
		case ref.Brain:
			named["brain"] = true
		case strings.HasPrefix(ref.Path, KnowledgebaseFolderName+"/"):
			named["kb"] = true
		case strings.HasPrefix(ref.Path, "learnings/"):
			named["learnings"] = true
		case ref.Path == "db/README.md":
			named["database"] = true
		}
	}
	return named
}

// checkResourceIntent returns a refusal for one step description, or "".
func checkResourceIntent(description string) string {
	section, found := descriptionSectionBody(description, "Resources")
	if !found {
		return "has no ## Resources section stating its knowledge base, database, Brain and learnings use (write \"no use\" for a resource it does not use)"
	}
	stated := map[string]string{}
	for _, line := range strings.Split(section, "\n") {
		label, value, ok := resourceIntentLine(line)
		if !ok {
			continue
		}
		for _, kind := range resourceKinds {
			for _, synonym := range kind.synonyms {
				if label == synonym || strings.HasPrefix(label, synonym+" ") || strings.HasPrefix(label, synonym+"(") {
					if _, seen := stated[kind.key]; !seen {
						stated[kind.key] = value
					}
				}
			}
		}
	}
	var missing []string
	for _, kind := range resourceKinds {
		if strings.TrimSpace(stated[kind.key]) == "" {
			missing = append(missing, kind.label)
		}
	}
	if len(missing) > 0 {
		return "## Resources does not say how it uses: " + strings.Join(missing, ", ") + " (give the use, or \"no use\")"
	}
	named := referenceKindsNamed(description)
	for _, kind := range resourceKinds {
		if named[kind.key] && statesNoUse(stated[kind.key]) {
			return fmt.Sprintf("## Resources says %q is not used, but the description names a %s reference it can already read", kind.label, strings.ToLower(kind.label))
		}
	}
	return ""
}

// ValidateResourceIntentContractStamp proves every live step, route and orphan
// was reviewed for resource intent. It refuses on structure and on a direct
// contradiction with the references the description itself names, never on
// wording, and it checks nothing about grants: the review adds none.
func ValidateResourceIntentContractStamp(content string) error {
	if err := ValidateAgentDescriptionFirstContractStamp(content); err != nil {
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
		if step == nil {
			return nil
		}
		agent, isAgent := step.(*AgentPlanStep)
		// Scripted, routing and Crew steps are driven by code or routes and keep
		// short descriptions (step-description.md); only agents carry the section.
		if isAgent && !agent.AuthoredPrompt {
			if reason := checkResourceIntent(step.GetDescription()); reason != "" {
				return fmt.Errorf("step %q %s before stamping %s", step.GetID(), reason, ResourceIntentContractVersion)
			}
		}
		if isAgent {
			for _, route := range agent.PredefinedRoutes {
				if err := check(route.SubAgentStep); err != nil {
					return err
				}
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

// ValidateResourceIntentAgainstStepConfig refuses a Resources line that claims use of a store the step's own
// configuration explicitly switches off (knowledgebase_access or learnings_access "none"). Unset access defaults to
// read for both stores, so only an explicit "none" is a contradiction; this compares a configured value with the
// line, never its wording. Steps without a config entry, or a missing file, are not checked.
func ValidateResourceIntentAgainstStepConfig(planContent, stepConfigContent string) error {
	if strings.TrimSpace(stepConfigContent) == "" {
		return nil
	}
	configs, err := ParseStepConfigContent(stepConfigContent)
	if err != nil {
		return nil // the config's own validators report a broken file; this check is only for a clear contradiction
	}
	off := map[string]map[string]bool{}
	for _, config := range configs {
		if config.AgentConfigs == nil {
			continue
		}
		kinds := map[string]bool{}
		if config.AgentConfigs.KnowledgebaseAccess == "none" {
			kinds["kb"] = true
		}
		if config.AgentConfigs.LearningsAccess == "none" {
			kinds["learnings"] = true
		}
		if len(kinds) > 0 {
			off[config.ID] = kinds
		}
	}
	if len(off) == 0 {
		return nil
	}
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(planContent), &plan); err != nil {
		return err
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		return err
	}
	var check func(PlanStepInterface) error
	check = func(step PlanStepInterface) error {
		if step == nil {
			return nil
		}
		agent, isAgent := step.(*AgentPlanStep)
		if isAgent && !agent.AuthoredPrompt && off[step.GetID()] != nil {
			section, _ := descriptionSectionBody(step.GetDescription(), "Resources")
			for _, line := range strings.Split(section, "\n") {
				label, value, ok := resourceIntentLine(line)
				if !ok {
					continue
				}
				for _, kind := range resourceKinds {
					if !off[step.GetID()][kind.key] || statesNoUse(value) {
						continue
					}
					for _, synonym := range kind.synonyms {
						if label == synonym || strings.HasPrefix(label, synonym+" ") || strings.HasPrefix(label, synonym+"(") {
							return fmt.Errorf("step %q: ## Resources describes %s use, but its step configuration sets that access to none; say none, or change the access deliberately (the upgrade never does) before stamping %s", step.GetID(), strings.ToLower(kind.label), ResourceIntentContractVersion)
						}
					}
				}
			}
		}
		if isAgent {
			for _, route := range agent.PredefinedRoutes {
				if err := check(route.SubAgentStep); err != nil {
					return err
				}
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
