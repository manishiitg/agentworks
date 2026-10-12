package agentprofiles

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkingTip is display metadata, never an instruction or executable condition.
// A product may reference a shared definition with only its id.
type WorkingTip struct {
	ID         string         `yaml:"id" json:"id"`
	Text       string         `yaml:"text,omitempty" json:"text"`
	SkipIfUsed string         `yaml:"skip_if_used,omitempty" json:"skip_if_used,omitempty"`
	When       *TipConditions `yaml:"when,omitempty" json:"when,omitempty"`
}

type TipConditions struct {
	CodeTabs *TipTabRange `yaml:"code_tabs,omitempty" json:"code_tabs,omitempty"`
}

type TipTabRange struct {
	Min *int `yaml:"min,omitempty" json:"min,omitempty"`
	Max *int `yaml:"max,omitempty" json:"max,omitempty"`
}

//go:embed working_tips.yaml
var sharedWorkingTipsYAML []byte

func resolveWorkingTips(tips []WorkingTip) ([]WorkingTip, error) {
	var shared []WorkingTip
	decoder := yaml.NewDecoder(bytes.NewReader(sharedWorkingTipsYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&shared); err != nil {
		return nil, fmt.Errorf("shared working tips: %w", err)
	}
	byID := make(map[string]WorkingTip, len(shared))
	for _, tip := range shared {
		byID[tip.ID] = tip
	}
	resolved := make([]WorkingTip, 0, len(tips))
	seen := make(map[string]bool, len(tips))
	for _, tip := range tips {
		if tip.ID == "" || strings.TrimSpace(tip.ID) != tip.ID || seen[tip.ID] {
			return nil, fmt.Errorf("ui.tips: invalid or duplicate id %q", tip.ID)
		}
		seen[tip.ID] = true
		if tip.Text == "" {
			definition, ok := byID[tip.ID]
			if !ok || tip.SkipIfUsed != "" || tip.When != nil {
				return nil, fmt.Errorf("ui.tips %q: a shared reference needs only an existing id", tip.ID)
			}
			tip = definition
		}
		if strings.TrimSpace(tip.Text) == "" {
			return nil, fmt.Errorf("ui.tips %q: text is required", tip.ID)
		}
		switch tip.SkipIfUsed {
		case "", "quick-switcher", "panel-switcher", "toolbar-hide", "chat-tab-keys", "chat-tab-new":
		default:
			return nil, fmt.Errorf("ui.tips %q: unknown used feature %q", tip.ID, tip.SkipIfUsed)
		}
		if tip.When != nil && tip.When.CodeTabs != nil {
			r := tip.When.CodeTabs
			if (r.Min != nil && *r.Min < 0) || (r.Max != nil && *r.Max < 0) || (r.Min != nil && r.Max != nil && *r.Min > *r.Max) {
				return nil, fmt.Errorf("ui.tips %q: invalid code_tabs range", tip.ID)
			}
		}
		resolved = append(resolved, tip)
	}
	return resolved, nil
}
