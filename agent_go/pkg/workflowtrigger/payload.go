package workflowtrigger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ValueMapping bounds external scalar values to owner-configured choices.
type ValueMapping struct {
	Source  string            `json:"source"`
	Values  map[string]string `json:"values"`
	Default string            `json:"default,omitempty"`
}
type PayloadMappings struct {
	Group  *ValueMapping           `json:"group,omitempty"`
	Routes map[string]ValueMapping `json:"routes,omitempty"`
	Step   *ValueMapping           `json:"step,omitempty"`
	Inputs map[string]InputMapping `json:"inputs,omitempty"`
}

// InputMapping selects data, never execution authority or a preset. Without
// values, the JSON value at source is passed through with its original type.
type InputMapping struct {
	Source  string                     `json:"source"`
	Values  map[string]json.RawMessage `json:"values,omitempty"`
	Default json.RawMessage            `json:"default,omitempty"`
}

// Scalar reads a fixed dotted path, including numeric array segments. It never
// evaluates expressions or accepts a target chosen directly by event content.
func Scalar(payload json.RawMessage, path string) (string, error) {
	current, err := Value(payload, path)
	if err != nil {
		return "", err
	}
	switch value := current.(type) {
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case bool:
		return strconv.FormatBool(value), nil
	default:
		return "", fmt.Errorf("payload source %q must be a string, number, or boolean", path)
	}
}

// Value reads the same fixed path as Scalar, retaining objects, arrays and
// exact JSON numbers for the script's declared input validator.
func Value(payload json.RawMessage, path string) (interface{}, error) {
	var root interface{}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("cannot read payload source %q", path)
	}
	original := path
	path = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(path), "$."), ".")
	if path == "" {
		return nil, fmt.Errorf("payload source is required")
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch value := current.(type) {
		case map[string]interface{}:
			next, ok := value[segment]
			if !ok {
				return nil, fmt.Errorf("payload source %q was not found", original)
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(value) {
				return nil, fmt.Errorf("payload source %q was not found", original)
			}
			current = value[index]
		default:
			return nil, fmt.Errorf("payload source %q was not found", original)
		}
	}
	return current, nil
}

type Condition struct {
	Source          string `json:"source"`
	Operator        string `json:"operator"`
	Value           string `json:"value"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}
type Match struct {
	All []Condition `json:"all,omitempty"`
	Any []Condition `json:"any,omitempty"`
}

func (m *Match) Validate() error {
	if m == nil {
		return nil
	}
	if len(m.All)+len(m.Any) == 0 || len(m.All)+len(m.Any) > 20 {
		return fmt.Errorf("match requires 1–20 conditions")
	}
	for _, conditions := range [][]Condition{m.All, m.Any} {
		for _, c := range conditions {
			if strings.TrimSpace(c.Source) == "" || len(c.Source) > 256 || len(c.Value) > 4096 {
				return fmt.Errorf("invalid match source or value length")
			}
			if c.Operator != "equals" && c.Operator != "contains" {
				return fmt.Errorf("match operator must be equals or contains")
			}
			if c.Operator == "contains" && c.Value == "" {
				return fmt.Errorf("contains match requires a nonempty value")
			}
		}
	}
	return nil
}
func (m *Match) Matches(payload json.RawMessage) bool {
	if m == nil {
		return true
	}
	if m.Validate() != nil {
		return false
	}
	check := func(c Condition) bool {
		source, err := Scalar(payload, c.Source)
		if err != nil {
			return false
		}
		value := c.Value
		if c.CaseInsensitive {
			source = strings.ToLower(source)
			value = strings.ToLower(value)
		}
		if c.Operator == "equals" {
			return source == value
		}
		return strings.Contains(source, value)
	}
	for _, c := range m.All {
		if !check(c) {
			return false
		}
	}
	if len(m.Any) == 0 {
		return true
	}
	for _, c := range m.Any {
		if check(c) {
			return true
		}
	}
	return false
}
