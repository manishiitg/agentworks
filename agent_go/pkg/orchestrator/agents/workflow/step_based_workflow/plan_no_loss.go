package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-556 decision 3: check_plan_no_loss is the deterministic half of the
// consolidator's authority. Architecture may apply a pure text-moving
// consolidation only when every rule-bearing token of the old step text still
// appears in the new description, items, or a file the new description names
// under Inputs/Guides. Judgment about whether the restructure is good stays
// with the agent; this only proves nothing binding was dropped.

// NoLossToken is one rule-bearing token from the old text.
type NoLossToken struct {
	Kind    string `json:"kind"`
	Token   string `json:"token"`
	Context string `json:"context,omitempty"`
	// HistoryOnly marks a token whose every occurrence sits in a dated or
	// incident sentence; it may be dropped only when the caller acknowledges
	// it in dropped_history (history belongs in the changelog reason).
	HistoryOnly bool `json:"history_only,omitempty"`
}

// NoLossReport is check_plan_no_loss's result.
type NoLossReport struct {
	Pass               bool          `json:"pass"`
	StepID             string        `json:"step_id,omitempty"`
	TokensChecked      int           `json:"tokens_checked"`
	Missing            []NoLossToken `json:"missing,omitempty"`
	AcknowledgedDrops  []NoLossToken `json:"acknowledged_history_drops,omitempty"`
	RejectedDrops      []string      `json:"rejected_dropped_history,omitempty"`
	ResolvedFiles      []string      `json:"resolved_files,omitempty"`
	UnresolvedFiles    []string      `json:"unresolved_files,omitempty"`
	BrainReferences    []string      `json:"brain_references_not_checked,omitempty"`
	OldChars           int           `json:"old_chars"`
	NewDescriptionChar int           `json:"new_description_chars"`
	Note               string        `json:"note"`
}

var (
	noLossISODateRe    = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ][0-9:.]+Z?)?\b`)
	noLossIdentifierRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+`)
	noLossPathRe       = regexp.MustCompile(`(?:https?://[^\s)\]"'` + "`" + `]+|(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+|[A-Za-z0-9_-]+\.(?:md|json|py|js|ts|sql|csv|html|txt|yaml|yml|sh|sqlite))`)
	noLossThresholdRe  = regexp.MustCompile(`(?i)(>=|<=|≥|≤|>|<|\bat least|\bat most|\bno more than|\bup to)\s*\$?(\d+(?:,\d{3})*(?:\.\d+)?)(%?)`)
	noLossNumberRe     = regexp.MustCompile(`\d+(?:,\d{3})*(?:\.\d+)?(?:\s*[–—-]\s*\d+(?:,\d{3})*(?:\.\d+)?)?%?`)
	noLossQuotedRe     = regexp.MustCompile("\"([^\"\\n]{1,120})\"|“([^”\\n]{1,120})”|`([^`\\n]{1,120})`|(?:^|[^A-Za-z0-9])'([^'\\s][^'\\n]{0,118}[^'\\s]|[^'\\s])'")
	noLossListMarkerRe = regexp.MustCompile(`(?m)^\s*\d+[.)]\s`)
	noLossThousandsRe  = regexp.MustCompile(`(\d),(\d{3})`)
	noLossSectionRe    = regexp.MustCompile(`(?im)^#{1,3}[ \t]+(.+?)[ \t]*:?[ \t]*$`)
	noLossBrainRe      = regexp.MustCompile(`brain:[A-Za-z0-9_./-]+`)
)

// noLossNamedRoots are the workflow paths a step's Inputs/Guides may name
// (design note decision 1).
var noLossNamedRoots = []string{"learnings/", "knowledgebase/", "code/shared/", "soul/", "db/README.md"}

// NoLossNamedFiles returns the workflow-relative files the description names
// under its Inputs and Guides sections, plus brain: references.
func NoLossNamedFiles(description string) (files []string, brain []string) {
	seen := map[string]bool{}
	for _, section := range descriptionSections(description, "inputs", "input", "guides") {
		for _, ref := range noLossBrainRe.FindAllString(section, -1) {
			if !seen[ref] {
				seen[ref] = true
				brain = append(brain, ref)
			}
		}
		for _, raw := range noLossPathRe.FindAllString(section, -1) {
			p := strings.Trim(raw, ".,;:)")
			if strings.HasPrefix(p, "http") || strings.Contains(p, "..") {
				continue
			}
			for _, root := range noLossNamedRoots {
				if strings.HasPrefix(p, root) || p == strings.TrimSuffix(root, "/") {
					if !seen[p] {
						seen[p] = true
						files = append(files, path.Clean(p))
					}
					break
				}
			}
		}
	}
	return files, brain
}

// descriptionSections returns the bodies of the headings whose lowercase name
// is in names.
func descriptionSections(description string, names ...string) []string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	locs := noLossSectionRe.FindAllStringSubmatchIndex(description, -1)
	var out []string
	for i, loc := range locs {
		name := strings.ToLower(strings.TrimSpace(description[loc[2]:loc[3]]))
		if !want[name] {
			continue
		}
		end := len(description)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, description[loc[1]:end])
	}
	return out
}

// noLossCompact is the matching form for numbers and thresholds: no
// whitespace, dashes unified, thousands separators removed, lowercase.
func noLossCompact(s string) string {
	s = strings.NewReplacer("–", "-", "—", "-", "≥", ">=", "≤", "<=", "$", "").Replace(s)
	s = noLossThousandsRe.ReplaceAllString(s, "$1$2")
	s = noLossThousandsRe.ReplaceAllString(s, "$1$2")
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

func noLossNormalizedSpace(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// historySpans returns byte ranges of sentences that carry dated or incident
// text in the old description.
func historySpans(text string) [][2]int {
	var spans [][2]int
	start := 0
	flush := func(end int) {
		if end > start && promptDatedTextRe.MatchString(text[start:end]) {
			spans = append(spans, [2]int{start, end})
		}
		start = end
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\n' || ((c == '.' || c == '!' || c == '?' || c == ';') && i+1 < len(text) && (text[i+1] == ' ' || text[i+1] == '\n')) {
			flush(i + 1)
		}
	}
	flush(len(text))
	return spans
}

func inSpans(spans [][2]int, at int) bool {
	for _, s := range spans {
		if at >= s[0] && at < s[1] {
			return true
		}
	}
	return false
}

type noLossCandidate struct {
	kind, token, key string
	positions        []int
}

// ExtractNoLossTokens returns the rule-bearing tokens of text with the
// positions where each occurs. key is the normalized form searched for.
func extractNoLossTokens(text string) []noLossCandidate {
	byKey := map[string]*noLossCandidate{}
	var order []string
	add := func(kind, token, key string, at int) {
		if strings.TrimSpace(key) == "" {
			return
		}
		full := kind + "\x00" + key
		c := byKey[full]
		if c == nil {
			c = &noLossCandidate{kind: kind, token: token, key: key}
			byKey[full] = c
			order = append(order, full)
		}
		c.positions = append(c.positions, at)
	}
	// Mask spans already claimed by a more specific kind so their digits are
	// not re-extracted as bare numbers.
	masked := []byte(text)
	mask := func(a, b int) {
		for i := a; i < b && i < len(masked); i++ {
			if masked[i] != '\n' {
				masked[i] = ' '
			}
		}
	}
	for _, m := range noLossISODateRe.FindAllStringIndex(text, -1) {
		add("date", text[m[0]:m[1]], text[m[0]:m[1]], m[0])
		mask(m[0], m[1])
	}
	for _, m := range noLossListMarkerRe.FindAllStringIndex(text, -1) {
		mask(m[0], m[1])
	}
	for _, m := range noLossQuotedRe.FindAllStringSubmatchIndex(text, -1) {
		for g := 2; g+1 < len(m); g += 2 {
			if m[g] < 0 {
				continue
			}
			inner := text[m[g]:m[g+1]]
			add("quoted_literal", inner, noLossNormalizedSpace(inner), m[g])
			break
		}
	}
	for _, m := range noLossPathRe.FindAllStringIndex(text, -1) {
		p := strings.TrimRight(text[m[0]:m[1]], ".,;:")
		if !strings.Contains(p, ".") && !strings.HasPrefix(p, "http") {
			// A slash list such as "closed/unavailable/applied" is prose, not
			// a path; identifiers inside it are still extracted below.
			continue
		}
		add("file_path", p, p, m[0])
	}
	for _, m := range noLossIdentifierRe.FindAllStringIndex(string(masked), -1) {
		add("identifier", text[m[0]:m[1]], text[m[0]:m[1]], m[0])
	}
	for _, m := range noLossThresholdRe.FindAllStringSubmatchIndex(string(masked), -1) {
		op := strings.ToLower(strings.TrimSpace(text[m[2]:m[3]]))
		switch op {
		case "≥", "at least":
			op = ">="
		case "≤", "at most", "no more than", "up to":
			op = "<="
		}
		num := noLossCompact(text[m[4]:m[5]]) + text[m[6]:m[7]]
		add("threshold", strings.TrimSpace(text[m[0]:m[1]]), op+num, m[0])
	}
	for _, m := range noLossNumberRe.FindAllStringIndex(string(masked), -1) {
		raw := strings.TrimSpace(text[m[0]:m[1]])
		key := noLossCompact(raw)
		if strings.Contains(key, "-") {
			add("range", raw, key, m[0])
			continue
		}
		add("number", raw, key, m[0])
	}
	out := make([]noLossCandidate, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// noLossPresent reports whether token appears in the new corpus.
func noLossPresent(c noLossCandidate, raw, compact, spaced string) bool {
	switch c.kind {
	case "identifier", "file_path", "date":
		return strings.Contains(raw, c.key)
	case "quoted_literal":
		return strings.Contains(spaced, c.key)
	case "threshold":
		if strings.Contains(compact, c.key) {
			return true
		}
		// Accept the verbal form of the same bound.
		num := strings.TrimLeft(c.key, "<>=")
		switch {
		case strings.HasPrefix(c.key, ">="):
			return noLossNumberIn(compact, "atleast"+num) || noLossNumberIn(compact, "≥"+num)
		case strings.HasPrefix(c.key, "<="):
			return noLossNumberIn(compact, "atmost"+num) || noLossNumberIn(compact, "nomorethan"+num) || noLossNumberIn(compact, "upto"+num)
		}
		return false
	case "number", "range":
		return noLossNumberIn(compact, c.key)
	}
	return strings.Contains(raw, c.token)
}

// noLossNumberIn finds needle in haystack where the match is not part of a
// longer number (a digit or decimal point on either side).
func noLossNumberIn(haystack, needle string) bool {
	for from := 0; ; {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(needle)
		leftOK := i == 0 || !isNoLossDigit(haystack[i-1])
		rightOK := end >= len(haystack) || !((haystack[end] >= '0' && haystack[end] <= '9') || (haystack[end] == '.' && end+1 < len(haystack) && isNoLossDigit(haystack[end+1])))
		if needle[0] < '0' || needle[0] > '9' {
			leftOK = true
		}
		if leftOK && rightOK {
			return true
		}
		from = i + 1
	}
}

func isNoLossDigit(b byte) bool { return (b >= '0' && b <= '9') || b == '.' }

// CheckPlanNoLoss compares the old step text against the new description,
// items text and the content of the files the new description names.
// droppedHistory lists tokens the caller deliberately removes as history;
// each must be history-only in the old text.
func CheckPlanNoLoss(oldText, newDescription, newItemsText string, guides map[string]string, droppedHistory []string) NoLossReport {
	report := NoLossReport{OldChars: len([]rune(oldText)), NewDescriptionChar: len([]rune(newDescription))}
	var corpus strings.Builder
	corpus.WriteString(newDescription)
	corpus.WriteString("\n")
	corpus.WriteString(newItemsText)
	names := make([]string, 0, len(guides))
	for name := range guides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		corpus.WriteString("\n")
		corpus.WriteString(guides[name])
	}
	raw := corpus.String()
	compact := noLossCompact(raw)
	spaced := noLossNormalizedSpace(raw)

	acknowledged := map[string]bool{}
	for _, t := range droppedHistory {
		if t = strings.TrimSpace(t); t != "" {
			acknowledged[t] = false
		}
	}
	spans := historySpans(oldText)
	for _, c := range extractNoLossTokens(oldText) {
		report.TokensChecked++
		if noLossPresent(c, raw, compact, spaced) {
			continue
		}
		historyOnly := true
		for _, at := range c.positions {
			if !inSpans(spans, at) {
				historyOnly = false
				break
			}
		}
		tok := NoLossToken{Kind: c.kind, Token: c.token, HistoryOnly: historyOnly}
		if len(c.positions) > 0 {
			tok.Context = promptBudgetSnippet(oldText, c.positions[0], c.positions[0]+len(c.token))
		}
		if c.kind == "date" && historyOnly {
			// A date inside a dated sentence is the history itself.
			report.AcknowledgedDrops = append(report.AcknowledgedDrops, tok)
			continue
		}
		if _, ok := acknowledged[c.token]; ok && historyOnly {
			acknowledged[c.token] = true
			report.AcknowledgedDrops = append(report.AcknowledgedDrops, tok)
			continue
		}
		report.Missing = append(report.Missing, tok)
	}
	for t, used := range acknowledged {
		if !used {
			report.RejectedDrops = append(report.RejectedDrops, t)
		}
	}
	sort.Strings(report.RejectedDrops)
	report.Pass = len(report.Missing) == 0
	if report.Pass {
		report.Note = "No rule-bearing token was lost. This proves only that nothing binding was dropped; run the step once and require validation to pass before keeping the change."
	} else {
		report.Note = "Not a pure text move: put every missing token back in the description, an item, or a guide/note named under Inputs or Guides (and re-check), or keep the old text and raise an owner proposal. Tokens that only described history may be listed in dropped_history."
	}
	return report
}

// noLossItemsText extracts the authored text of message_sequence items:
// title, message and source_sql. Ids and types are structure, not rules.
func noLossItemsText(raw interface{}) string {
	var parts []string
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case map[string]interface{}:
			for _, key := range []string{"title", "message", "source_sql"} {
				if s, ok := t[key].(string); ok {
					parts = append(parts, s)
				}
			}
			for key, child := range t {
				if key == "title" || key == "message" || key == "source_sql" {
					continue
				}
				if _, nested := child.([]interface{}); nested {
					walk(child)
				}
			}
		case []interface{}:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(raw)
	return strings.Join(parts, "\n")
}

func stepItemsText(step PlanStepInterface) string {
	seq, ok := step.(*AgentPlanStep)
	if !ok || len(seq.Items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(seq.Items)
	if err != nil {
		return ""
	}
	var generic interface{}
	if json.Unmarshal(encoded, &generic) != nil {
		return ""
	}
	return noLossItemsText(generic)
}

// checkPlanNoLossForStep runs the check for one current plan step against a
// proposed description/items. readFile reads a workflow-relative path.
func checkPlanNoLossForStep(ctx context.Context, plan *PlanningResponse, args map[string]interface{}, readFile func(context.Context, string) (string, error)) (NoLossReport, error) {
	stepID := strings.TrimSpace(asString(args["step_id"]))
	if stepID == "" {
		return NoLossReport{}, fmt.Errorf("step_id is required")
	}
	var step PlanStepInterface
	for _, info := range collectAllSteps(append(append([]PlanStepInterface{}, plan.Steps...), plan.OrphanSteps...)) {
		if info.Step != nil && info.Step.GetID() == stepID {
			step = info.Step
			break
		}
	}
	if step == nil {
		return NoLossReport{}, fmt.Errorf("step %q not found in the current plan", stepID)
	}
	oldItems := stepItemsText(step)
	oldText := step.GetDescription()
	if oldItems != "" {
		oldText += "\n" + oldItems
	}
	newDescription := step.GetDescription()
	if v, ok := args["proposed_description"].(string); ok && strings.TrimSpace(v) != "" {
		newDescription = v
	}
	newItems := oldItems
	if raw, ok := args["proposed_items"]; ok && raw != nil {
		if s, isString := raw.(string); isString {
			var decoded interface{}
			if err := json.Unmarshal([]byte(s), &decoded); err != nil {
				return NoLossReport{}, fmt.Errorf("proposed_items must be the items array (JSON): %w", err)
			}
			raw = decoded
		}
		newItems = noLossItemsText(raw)
	}
	files, brain := NoLossNamedFiles(newDescription)
	guides := map[string]string{}
	var resolved, unresolved []string
	for _, f := range files {
		content, err := readFile(ctx, f)
		if err != nil || strings.TrimSpace(content) == "" {
			unresolved = append(unresolved, f)
			continue
		}
		guides[f] = content
		resolved = append(resolved, f)
	}
	report := CheckPlanNoLoss(oldText, newDescription, newItems, guides, stringSliceArg(args["dropped_history"]))
	report.StepID = stepID
	report.ResolvedFiles = resolved
	report.UnresolvedFiles = unresolved
	report.BrainReferences = brain
	return report, nil
}

func stringSliceArg(raw interface{}) []string {
	var out []string
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, v...)
	}
	return out
}

const checkPlanNoLossDescription = "PLAT-556 consolidator check. Compare a step's current description and items with a proposed rewrite: every rule-bearing token of the old text (identifiers with underscores, VAR_ names, numbers and ranges, thresholds, file paths, quoted literals) must appear in the proposed description, the proposed items, or a workflow file the proposed description names under ## Inputs or ## Guides (learnings/, knowledgebase/, code/shared/, soul/, db/README.md; write moved guides first, then check). pass=true only when nothing is missing. Read-only: it changes nothing. brain: references are listed but not read."

func checkPlanNoLossParameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"step_id":              map[string]interface{}{"type": "string", "description": "The step whose current text is the 'old' side."},
			"proposed_description": map[string]interface{}{"type": "string", "description": "The full new description. Omit to keep the current description."},
			"proposed_items": map[string]interface{}{
				"type": "array", "description": "The full new message_sequence items array (title/message/source_sql are compared). Omit to keep the current items.",
				"items": map[string]interface{}{"type": "object"},
			},
			"dropped_history": map[string]interface{}{
				"type": "array", "items": map[string]interface{}{"type": "string"},
				"description": "Tokens deliberately removed because they only described history (dated observations, incidents). Accepted only when every occurrence is in a dated/incident sentence; put the history in the change reason instead.",
			},
		},
		"required": []string{"step_id"},
	}
}

func createCheckPlanNoLossExecutor(workspacePath string, readFile func(context.Context, string) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}
		report, err := checkPlanNoLossForStep(ctx, plan, args, func(ctx context.Context, rel string) (string, error) {
			return readFile(ctx, normalizePathForWorkspaceAPI(rel, workspacePath))
		})
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

const restoreStepFromChangelogDescription = "PLAT-556 consolidator rollback. Restore one agent step's description and items to the values recorded before a plan change (the changelog entry's old_value for those fields, the content behind its before_ref). Use it when a consolidation's comparison run fails validation, instead of retyping the old text. Provide change_id (from the edit's changelog entry), step_id when the entry touched several steps, and reason. The restore is itself a logged plan change."

func restoreStepFromChangelogParameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"change_id": map[string]interface{}{"type": "string", "description": "change_id of the planning/changelog entry to undo."},
			"step_id":   map[string]interface{}{"type": "string", "description": "Step to restore; required when the entry touched more than one step."},
			"reason":    map[string]interface{}{"type": "string", "description": "Why the change is being rolled back (for example the failed validation)."},
		},
		"required": []string{"change_id", "reason"},
	}
}

// findPlanChangelogEntry scans planning/changelog/*.json for change_id.
func findPlanChangelogEntry(workspacePath, changeID string) (*PlanChangelogEntry, error) {
	dir := workflowAbsPath(workspacePath, PlanningFolderName, "changelog")
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read planning/changelog: %w", err)
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var file PlanChangelog
		if json.Unmarshal(raw, &file) != nil {
			continue
		}
		for i := range file.Entries {
			if file.Entries[i].ChangeID == changeID {
				return &file.Entries[i], nil
			}
		}
	}
	return nil, fmt.Errorf("change_id %q not found in planning/changelog", changeID)
}

func createRestoreStepFromChangelogExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	update := createUpdateAgentStepExecutor(workspacePath, logger, readFile, writeFile)
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		changeID := strings.TrimSpace(asString(args["change_id"]))
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if changeID == "" {
			return "", fmt.Errorf("change_id is required")
		}
		entry, err := findPlanChangelogEntry(workspacePath, changeID)
		if err != nil {
			return "", err
		}
		stepID := strings.TrimSpace(asString(args["step_id"]))
		restore := map[string]interface{}{}
		for _, change := range entry.Changes {
			if change.Field != "description" && change.Field != "items" {
				continue
			}
			if stepID == "" {
				stepID = change.StepID
			}
			if change.StepID != stepID {
				if asString(args["step_id"]) == "" {
					return "", fmt.Errorf("change %s touched several steps; pass step_id", changeID)
				}
				continue
			}
			switch change.Field {
			case "description":
				old := asString(change.OldValue)
				if old == "" {
					restore["clear_description"] = true
				} else {
					restore["description"] = old
				}
			case "items":
				var items []interface{}
				if err := json.Unmarshal([]byte(asString(change.OldValue)), &items); err != nil {
					return "", fmt.Errorf("change %s has no restorable items: %w", changeID, err)
				}
				restore["items"] = items
			}
		}
		if len(restore) == 0 {
			return "", fmt.Errorf("change %s recorded no description/items old values for a step; restore other fields with the matching update tool", changeID)
		}
		restore["existing_step_id"] = stepID
		restore["reason"] = fmt.Sprintf("Restore %s to its state before %s (before_ref %s): %s", stepID, changeID, entry.BeforeRef, reason)
		return update(ctx, restore)
	}
}
