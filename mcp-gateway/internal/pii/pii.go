// Package pii applies bounded, deterministic text inspection to MCP payloads.
// It does not claim to identify every sensitive value and never uses a model.
package pii

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

const MaxPayloadBytes = 256 << 10

const (
	Allow  = "allow"
	Mask   = "mask"
	Block  = "block"
	Review = "require_review"
	Input  = "input"
	Output = "output"
	Both   = "both"
)

type Rule struct {
	ID          string
	WorkspaceID string
	GroupID     string
	ConnectorID string
	PublicName  string
	DataType    string
	Direction   string
	Action      string
}

type Scope struct {
	WorkspaceID string
	GroupIDs    []string
	ConnectorID string
	PublicName  string
	Direction   string
}

type Decision struct {
	Action     string   `json:"action"`
	DataTypes  []string `json:"data_types"`
	MatchCount int      `json:"match_count"`
}

var ErrPayloadTooLarge = errors.New("payload exceeds PII inspection limit")

type detector struct {
	kind     string
	pattern  *regexp.Regexp
	validate func(string) bool
}

var detectors = []detector{
	{"email", regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`), nil},
	{"phone", regexp.MustCompile(`\b(?:\+?1[ .-]?)?\(?[2-9][0-9]{2}\)?[ .-]?[2-9][0-9]{2}[ .-]?[0-9]{4}\b`), nil},
	{"ssn", regexp.MustCompile(`\b[0-9]{3}[- ]?[0-9]{2}[- ]?[0-9]{4}\b`), validSSN},
	{"credit_card", regexp.MustCompile(`\b(?:[0-9][ -]?){12,18}[0-9]\b`), validCard},
	{"api_key", regexp.MustCompile(`\b(?:AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,}|sk-[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9-]{10,})\b`), nil},
}

type match struct {
	start, end int
	kind       string
}

func digits(value string) string {
	var b strings.Builder
	for _, c := range value {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func validSSN(value string) bool {
	d := digits(value)
	return len(d) == 9 && d[:3] != "000" && d[:3] != "666" && d[0] != '9' && d[3:5] != "00" && d[5:] != "0000"
}

func validCard(value string) bool {
	d := digits(value)
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	sum, double := 0, false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

func severity(action string) int {
	switch action {
	case Block:
		return 4
	case Review:
		return 3
	case Mask:
		return 2
	default:
		return 1
	}
}

func defaultAction(kind string) string {
	if kind == "email" || kind == "phone" {
		return Mask
	}
	return Block
}

func selectedAction(kind string, scope Scope, rules []Rule) string {
	bestScore, best := -1, defaultAction(kind)
	for _, rule := range rules {
		if rule.WorkspaceID != scope.WorkspaceID || rule.DataType != kind ||
			(rule.Direction != "" && rule.Direction != Both && rule.Direction != scope.Direction) ||
			(rule.PublicName != "" && rule.PublicName != scope.PublicName) ||
			(rule.ConnectorID != "" && rule.ConnectorID != scope.ConnectorID) {
			continue
		}
		if rule.GroupID != "" {
			found := false
			for _, group := range scope.GroupIDs {
				if group == rule.GroupID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		score := 0
		if rule.GroupID != "" {
			score += 1
		}
		if rule.ConnectorID != "" {
			score += 2
		}
		if rule.PublicName != "" {
			score += 4
		}
		if rule.Direction != "" && rule.Direction != Both {
			score += 1
		}
		if score > bestScore || (score == bestScore && severity(rule.Action) > severity(best)) {
			bestScore, best = score, rule.Action
		}
	}
	return best
}

// ScanText returns a masked copy and only metadata about findings. On block
// or review the caller must discard the copy and stop the call.
func ScanText(value string, scope Scope, rules []Rule) (string, Decision, error) {
	if len(value) > MaxPayloadBytes {
		return "", Decision{Action: Block}, ErrPayloadTooLarge
	}
	matches := []match{}
	for _, d := range detectors {
		for _, loc := range d.pattern.FindAllStringIndex(value, -1) {
			if d.validate != nil && !d.validate(value[loc[0]:loc[1]]) {
				continue
			}
			matches = append(matches, match{loc[0], loc[1], d.kind})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start == matches[j].start {
			return matches[i].end > matches[j].end
		}
		return matches[i].start < matches[j].start
	})
	decision := Decision{Action: Allow, DataTypes: []string{}}
	kinds := map[string]bool{}
	var out strings.Builder
	last := 0
	for _, m := range matches {
		if m.start < last {
			continue
		}
		action := selectedAction(m.kind, scope, rules)
		if severity(action) > severity(decision.Action) {
			decision.Action = action
		}
		decision.MatchCount++
		kinds[m.kind] = true
		out.WriteString(value[last:m.start])
		if action == Mask {
			out.WriteString("[REDACTED:")
			out.WriteString(m.kind)
			out.WriteByte(']')
		} else {
			out.WriteString(value[m.start:m.end])
		}
		last = m.end
	}
	out.WriteString(value[last:])
	for kind := range kinds {
		decision.DataTypes = append(decision.DataTypes, kind)
	}
	sort.Strings(decision.DataTypes)
	return out.String(), decision, nil
}

// ScanJSON inspects string leaves in JSON-compatible arguments or structured
// results without changing their shape. Input size is bounded before walking.
func ScanJSON(value any, scope Scope, rules []Rule) (any, Decision, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, Decision{Action: Block}, err
	}
	if len(encoded) > MaxPayloadBytes {
		return nil, Decision{Action: Block}, ErrPayloadTooLarge
	}
	decision := Decision{Action: Allow, DataTypes: []string{}}
	kinds := map[string]bool{}
	var walk func(any) (any, error)
	walk = func(item any) (any, error) {
		switch v := item.(type) {
		case string:
			masked, result, err := ScanText(v, scope, rules)
			if err != nil {
				return nil, err
			}
			if severity(result.Action) > severity(decision.Action) {
				decision.Action = result.Action
			}
			decision.MatchCount += result.MatchCount
			for _, kind := range result.DataTypes {
				kinds[kind] = true
			}
			return masked, nil
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, child := range v {
				transformed, err := walk(child)
				if err != nil {
					return nil, err
				}
				out[key] = transformed
			}
			return out, nil
		case []any:
			out := make([]any, len(v))
			for i, child := range v {
				transformed, err := walk(child)
				if err != nil {
					return nil, err
				}
				out[i] = transformed
			}
			return out, nil
		default:
			return item, nil
		}
	}
	transformed, err := walk(value)
	if err != nil {
		return nil, Decision{Action: Block}, err
	}
	for kind := range kinds {
		decision.DataTypes = append(decision.DataTypes, kind)
	}
	sort.Strings(decision.DataTypes)
	return transformed, decision, nil
}
