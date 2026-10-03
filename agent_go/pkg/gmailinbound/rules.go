package gmailinbound

import (
	"fmt"
	"regexp"
	"strings"
)

// Rules are evaluated in saved order. One email starts at most one action.
// The target determines the action kind: project instruction or workflow binding.
type Rule struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Enabled         *bool             `json:"enabled,omitempty"`
	Filters         *Filters          `json:"filters,omitempty"`
	Instruction     string            `json:"instruction,omitempty"`
	RouteSelections map[string]string `json:"route_selections"`
	GroupNames      []string          `json:"group_names,omitempty"`
	StepID          string            `json:"step_id,omitempty"`
}

func (r Rule) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }

var ruleIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func NormalizeRules(rules []Rule, workflow bool) ([]Rule, error) {
	if len(rules) > 20 {
		return nil, fmt.Errorf("incoming email accepts at most 20 rules")
	}
	out := make([]Rule, 0, len(rules))
	seen := map[string]bool{}
	for _, rule := range rules {
		rule.ID, rule.Name = strings.TrimSpace(rule.ID), strings.TrimSpace(rule.Name)
		rule.Instruction, rule.StepID = strings.TrimSpace(rule.Instruction), strings.TrimSpace(rule.StepID)
		if !ruleIDPattern.MatchString(rule.ID) || seen[rule.ID] {
			return nil, fmt.Errorf("email rules require unique IDs of 1–64 letters, digits, underscores or hyphens")
		}
		seen[rule.ID] = true
		if rule.Name == "" || len(rule.Name) > 120 {
			return nil, fmt.Errorf("email rule %s requires a name of 1–120 bytes", rule.ID)
		}
		var err error
		if rule.Filters, err = NormalizeFilters(rule.Filters); err != nil {
			return nil, fmt.Errorf("email rule %s: %w", rule.ID, err)
		}
		if workflow {
			if rule.Instruction != "" || len(rule.GroupNames) == 0 || (rule.StepID == "" && rule.RouteSelections == nil) || (rule.StepID != "" && len(rule.RouteSelections) > 0) {
				return nil, fmt.Errorf("workflow email rule %s requires groups and saved route_selections ({} for full workflow) or step_id, and no chat instruction", rule.ID)
			}
		} else if rule.Instruction == "" || len(rule.Instruction) > 16*1024 || len(rule.GroupNames) > 0 || rule.RouteSelections != nil || rule.StepID != "" {
			return nil, fmt.Errorf("project email rule %s requires a chat instruction of 1–16384 bytes and no workflow binding", rule.ID)
		}
		out = append(out, rule)
	}
	return out, nil
}

func (r Route) SelectedRule() (*Rule, error) {
	if len(r.Rules) == 0 && r.SelectedRuleID == "" {
		return nil, nil // Original single-action setup.
	}
	for i := range r.Rules {
		if r.Rules[i].ID == r.SelectedRuleID {
			if !r.Rules[i].IsEnabled() {
				return nil, fmt.Errorf("selected email rule is disabled")
			}
			return &r.Rules[i], nil
		}
	}
	return nil, fmt.Errorf("selected email rule no longer exists")
}

// Per-rule sender permissions inherit the common policy when omitted. An
// explicit common sender list remains an outer restriction on every rule.
func (r Route) SenderFilters() (*Filters, error) {
	selected, err := r.SelectedRule()
	if err != nil {
		return nil, err
	}
	if selected != nil && selected.Filters != nil && len(selected.Filters.SenderAllowlist) > 0 {
		return selected.Filters, nil
	}
	return r.Filters, nil
}

func (r Route) AcceptsMessageKind(m Message) bool {
	selected, err := r.SelectedRule()
	return err == nil && (r.Filters.AcceptsMessageKind(m) || selected != nil && selected.Filters.AcceptsMessageKind(m))
}

func (r Route) FilterMismatch(m Message) string {
	if reason := r.Filters.Mismatch(m); reason != "" {
		return reason
	}
	selected, err := r.SelectedRule()
	if err != nil {
		return err.Error()
	}
	if selected != nil {
		return selected.Filters.Mismatch(m)
	}
	return ""
}

func (r Route) NewThreadScope() (required, allRules bool) {
	if r.Filters != nil && r.Filters.NewThreadsOnly {
		return true, true
	}
	selected, err := r.SelectedRule()
	return err == nil && selected != nil && selected.Filters != nil && selected.Filters.NewThreadsOnly, false
}

func (r Route) ChatInstruction() (string, error) {
	selected, err := r.SelectedRule()
	if err != nil || selected == nil {
		return "", err
	}
	return selected.Instruction, nil
}
