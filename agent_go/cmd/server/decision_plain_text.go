package server

import (
	"fmt"
	"regexp"
	"strings"
)

type plainTextField struct {
	name, text string
	maxLen     int
	// idsOnly checks only for internal IDs, for a longer proof field that may
	// name tables or files the user needs to verify a claim.
	idsOnly bool
}

var (
	decisionInternalIDPattern = regexp.MustCompile(`\b(?:PUL|GW)-[A-Za-z0-9]{4,}\b`)
	decisionCodeTermPattern   = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)
	// URLs and paths may legitimately contain underscores; they are removed
	// before looking for code-style terms.
	decisionPathLikePattern = regexp.MustCompile(`https?://\S+|\S*\.(?:md|json|html|py|csv|sqlite|txt|png|pdf)\b`)
)

func plainTextFieldProblems(field plainTextField) []string {
	text := strings.TrimSpace(field.text)
	if text == "" {
		return nil
	}
	var problems []string
	if ids := uniqueMatches(decisionInternalIDPattern, text); len(ids) > 0 {
		problems = append(problems, fmt.Sprintf("%s contains internal IDs (%s)", field.name, strings.Join(ids, ", ")))
	}
	if !field.idsOnly {
		stripped := decisionPathLikePattern.ReplaceAllString(text, " ")
		if terms := uniqueMatches(decisionCodeTermPattern, stripped); len(terms) > 0 {
			problems = append(problems, fmt.Sprintf("%s contains code-style terms (%s)", field.name, strings.Join(terms, ", ")))
		}
	}
	if field.maxLen > 0 && len([]rune(text)) > field.maxLen {
		problems = append(problems, fmt.Sprintf("%s is %d characters (keep it under %d)", field.name, len([]rune(text)), field.maxLen))
	}
	return problems
}

func uniqueMatches(pattern *regexp.Regexp, text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, match := range pattern.FindAllString(text, -1) {
		if !seen[match] && len(out) < 5 {
			seen[match] = true
			out = append(out, match)
		}
	}
	return out
}

// checkDecisionPlainText returns one error naming every problem, what to do, and
// where the technical detail belongs.
func checkDecisionPlainText(detailField string, fields ...plainTextField) error {
	var problems []string
	for _, field := range fields {
		problems = append(problems, plainTextFieldProblems(field)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the user reads this text, so it must be plain language: %s. Rewrite it: lead with what changed or what the user needs to do, use short sentences and everyday words, and leave out issue IDs, state names and code terms. Put IDs, evidence and technical detail in %s", strings.Join(problems, "; "), detailField)
}

func isGoalDecisionSource(source string) bool {
	switch strings.TrimSpace(source) {
	case "strategic_review", "technical_review", "architecture_review", "plan_drift_review", "goal_work":
		return true
	}
	return false
}
