// Package access defines the data-only access packages configured by the
// setup agent. The MCP execution path never calls a model.
package access

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Condition struct {
	Path  string `json:"path"` // JSON pointer into tool arguments
	Op    string `json:"op"`   // equals or matches
	Value string `json:"value"`
}

type ToolRule struct {
	PublicName  string      `json:"public_name"`
	Fingerprint string      `json:"fingerprint"`
	Conditions  []Condition `json:"conditions"`
}

type Package struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	GroupID     string     `json:"group_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Version     int        `json:"version"`
	Rules       []ToolRule `json:"rules"`
}

func Clone(p Package) Package {
	p.Rules = append([]ToolRule(nil), p.Rules...)
	for i := range p.Rules {
		p.Rules[i].Conditions = append([]Condition(nil), p.Rules[i].Conditions...)
	}
	return p
}

func ValidateCondition(c Condition) error {
	if c.Path == "" || !strings.HasPrefix(c.Path, "/") || len(c.Path) > 256 {
		return errors.New("condition path must be a JSON pointer")
	}
	for _, part := range strings.Split(c.Path[1:], "/") {
		if part == "" || strings.Contains(part, "~") {
			return errors.New("condition path contains an unsupported segment")
		}
	}
	if len(c.Value) > 1024 {
		return errors.New("condition value is too long")
	}
	switch c.Op {
	case "equals":
		if c.Value == "" {
			return errors.New("empty equality value is unsupported")
		}
	case "matches":
		if c.Value == "" {
			return errors.New("empty regex is unsupported")
		}
		if _, err := regexp.Compile(c.Value); err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
	default:
		return errors.New("condition op must be equals or matches")
	}
	return nil
}

// Match evaluates the entire argument value. Missing paths and non-string
// values always fail. RE2 keeps regular expression evaluation bounded.
func Match(c Condition, args map[string]any) bool {
	if ValidateCondition(c) != nil {
		return false
	}
	var current any = args
	for _, segment := range strings.Split(c.Path[1:], "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[segment]
		if !ok {
			return false
		}
	}
	value, ok := current.(string)
	if !ok {
		return false
	}
	if c.Op == "equals" {
		return value == c.Value
	}
	re, err := regexp.Compile(c.Value)
	if err != nil {
		return false
	}
	loc := re.FindStringIndex(value)
	return loc != nil && loc[0] == 0 && loc[1] == len(value)
}

// SchemaHasStringPath checks the approved schema, not a model's description.
// Combinators and opaque schemas are deliberately unsupported for scoped rules.
func SchemaHasStringPath(schema map[string]any, pointer string) bool {
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	current := schema
	for _, segment := range strings.Split(pointer[1:], "/") {
		props, ok := current["properties"].(map[string]any)
		if !ok {
			return false
		}
		next, ok := props[segment].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return current["type"] == "string"
}
