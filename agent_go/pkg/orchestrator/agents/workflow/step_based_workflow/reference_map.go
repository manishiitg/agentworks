package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PLAT-561: the edit-time reference map.
//
// About a third of the issues QA filed on a large workflow were changes not
// carried through: a step's Inputs naming a removed producer step, an eval
// asserting a file that moved, a note naming a retired path. Plan Drift checks
// only the changed step, and description-only or non-plan edits never flag it.
//
// The map is computed on read from the workflow on disk: plan steps (ids,
// context_output, context_dependencies, the files their validation schemas
// declare, and the paths and step ids their text names), step_config.json,
// the evaluation plan, KB notes, soul.md and learnings SKILL.md/references.
// Its checks are deterministic and it only REPORTS: edit responses append the
// dependents of what changed, and get_plan_prompt_health and Pulse evidence
// carry the open breaks. It never blocks an edit or a run. Business-rule
// contradictions (a rate in a note vs soul.md) need judgment and are not here.

const (
	refSeverityBreak = "break"
	refSeverityWarn  = "warn"
	refSeverityInfo  = "info"

	refMapMaxIssues = 150
)

// RefMapIssue is one reference that does not resolve (or, for info, an output
// nothing reads).
type RefMapIssue struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Source is where the reference is written: step:<id>, eval:<id>,
	// step_config:<id>, or file:<workflow-relative path>.
	Source string `json:"source"`
	Ref    string `json:"ref"`
	Detail string `json:"detail"`
}

// ReferenceMapReport is the workflow-wide result.
type ReferenceMapReport struct {
	Breaks         int           `json:"breaks"`
	Warnings       int           `json:"warnings"`
	Infos          int           `json:"infos"`
	Issues         []RefMapIssue `json:"issues,omitempty"`
	Truncated      bool          `json:"truncated,omitempty"`
	RetiredStepIDs []string      `json:"retired_step_ids,omitempty"`
	ScannedFiles   int           `json:"scanned_files"`
	Note           string        `json:"note"`
}

const referenceMapNote = "Deterministic reference map (PLAT-561). break = a reference that no longer resolves: a context dependency no step produces, or one a step writes but no context_output lists (it resolves to the consumer's own folder), a step folder or retired step id that no longer exists, a workflow file that is missing, an eval route that is gone. warn = the same unlisted dependency on a message_sequence step (it gets only the bare name), or a file read from a step folder that the step does not declare. info = an output nothing reads, or a config for a step not in the plan. A report, never a gate: fix the dependents in the same change, or say why the reference is right."

type refStep struct {
	id       string
	typ      string
	outputs  []string        // context_output files (staged for consumers)
	declared map[string]bool // outputs + validation_schema files
	deps     []string
	text     string // description, items, system prompt, route text
	inputs   string // the ## Inputs and ## Guides sections
	routes   map[string]bool
}

type refText struct {
	source string
	rel    string // workflow-relative path for files
	text   string
	isLog  bool
}

type referenceMap struct {
	root     string
	order    []*refStep
	steps    map[string]*refStep
	evals    []refText
	docs     []refText
	retired  map[string]bool
	evalIDs  map[string]bool
	index    map[string]bool // basenames of workflow files (code, learnings, knowledgebase, soul, db, evaluation, reports)
	issues   []RefMapIssue
	seen     map[string]bool
	scanned  int
	producer map[string][]string // context_output file -> step ids
	declarer map[string][]string // declared file -> step ids
}

var (
	// Template parts of a name: YYYY-MM-DD, NNN, XXXX.
	refPlaceholderRe = regexp.MustCompile(`YYYY|MM-DD|NNN|XXXX|<|>|\*`)
	// A workflow-relative path: code/..., learnings/..., knowledgebase/...
	refWorkflowPathRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./$~{}<>-])((?:code|learnings|knowledgebase|soul|db|evaluation|reports|variables)/[A-Za-z0-9_./-]*[A-Za-z0-9_/])`)
	// A step folder: <step-id>/<file>, optionally after $VAR_TARGET_RUN_PATH/ or ../
	refStepFolderRe = regexp.MustCompile(`(\$\{?VAR_[A-Z_]*RUN_PATH\}?/|\.\./|^|[^A-Za-z0-9_./$-])([a-z0-9]+(?:-[a-z0-9]+)+)/([A-Za-z0-9_][A-Za-z0-9_.-]*\.[A-Za-z0-9]+)?`)
	// A bare file name in an Inputs/Guides section.
	refBareFileRe   = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./$-])([A-Za-z0-9_][A-Za-z0-9_.-]*\.(?:json|jsonl|md|py|csv|txt|html|sql|yaml|yml))\b`)
	refWorkflowDirs = map[string]bool{"code": true, "learnings": true, "knowledgebase": true, "soul": true, "db": true, "evaluation": true, "reports": true, "variables": true, "planning": true, "config": true, "memory": true, "publish": true, "runs": true}
)

// CollectReferenceMap builds the map for a workspace-relative workflow path.
// A workflow with no plan returns an empty report.
func CollectReferenceMap(workspacePath string) (ReferenceMapReport, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return ReferenceMapReport{}, nil
	}
	return BuildReferenceMap(refMapRoot(workspacePath))
}

// refMapRoot resolves a workflow path: absolute when it already is one,
// otherwise under the workspace docs root.
func refMapRoot(workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if filepath.IsAbs(workspacePath) {
		if _, err := os.Stat(filepath.Join(workspacePath, PlanningFolderName)); err == nil {
			return workspacePath
		}
	}
	return workflowAbsPath(strings.Trim(workspacePath, "/"))
}

// BuildReferenceMap builds the map for an absolute workflow folder.
func BuildReferenceMap(root string) (ReferenceMapReport, error) {
	m, err := loadReferenceMap(root)
	if err != nil || m == nil {
		return ReferenceMapReport{}, err
	}
	return m.report(), nil
}

func loadReferenceMap(root string) (*referenceMap, error) {
	planRaw, err := os.ReadFile(filepath.Join(root, PlanningFolderName, "plan.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read planning/plan.json: %w", err)
	}
	m := &referenceMap{
		root: root, steps: map[string]*refStep{}, retired: map[string]bool{}, evalIDs: map[string]bool{},
		index: map[string]bool{}, seen: map[string]bool{}, producer: map[string][]string{}, declarer: map[string][]string{},
	}
	if err := m.loadPlan(planRaw); err != nil {
		return nil, err
	}
	m.loadEvaluation()
	m.loadRetired()
	m.loadDocs()
	m.buildIndex()
	m.check()
	return m, nil
}

func (m *referenceMap) loadPlan(raw []byte) error {
	var plan struct {
		Steps       []map[string]interface{} `json:"steps"`
		OrphanSteps []map[string]interface{} `json:"orphan_steps"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("parse planning/plan.json: %w", err)
	}
	var add func(step map[string]interface{})
	add = func(step map[string]interface{}) {
		id := strings.TrimSpace(refString(step["id"]))
		if id == "" || m.steps[id] != nil {
			return
		}
		s := &refStep{id: id, typ: refString(step["type"]), declared: map[string]bool{}, routes: map[string]bool{}}
		s.outputs = refOutputs(step["context_output"])
		for _, o := range s.outputs {
			s.declared[o] = true
			m.producer[o] = append(m.producer[o], id)
		}
		for _, d := range refStrings(step["context_dependencies"]) {
			if d = strings.TrimSpace(d); d != "" {
				s.deps = append(s.deps, d)
			}
		}
		var text []string
		description := refString(step["description"])
		text = append(text, description, refString(step["system_prompt"]))
		s.inputs = strings.Join(descriptionReferenceSections(description), "\n")
		refSchemaFiles(step["validation_schema"], s.declared)
		for _, item := range refMaps(step["items"]) {
			text = append(text, refString(item["message"]), refString(item["title"]))
			refSchemaFiles(item["validation_schema"], s.declared)
		}
		for _, route := range refMaps(step["routes"]) {
			s.routes[refString(route["route_id"])] = true
			text = append(text, refString(route["condition"]))
		}
		for file := range s.declared {
			if !refContains(s.outputs, file) {
				m.declarer[file] = append(m.declarer[file], id)
			}
		}
		s.text = strings.Join(text, "\n")
		m.steps[id] = s
		m.order = append(m.order, s)
		for _, route := range refMaps(step["predefined_routes"]) {
			if nested, ok := route["sub_agent_step"].(map[string]interface{}); ok {
				add(nested)
			}
		}
	}
	for _, step := range plan.Steps {
		add(step)
	}
	for _, step := range plan.OrphanSteps {
		add(step)
	}
	return nil
}

func (m *referenceMap) loadEvaluation() {
	raw, err := os.ReadFile(filepath.Join(m.root, "evaluation", "evaluation_plan.json"))
	if err != nil {
		return
	}
	var plan struct {
		Steps []map[string]interface{} `json:"steps"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return
	}
	m.scanned++
	for _, step := range plan.Steps {
		id := refString(step["id"])
		if id == "" {
			continue
		}
		m.evalIDs[id] = true
		source := "eval:" + id
		m.evals = append(m.evals, refText{source: source, rel: "evaluation/evaluation_plan.json", text: refString(step["description"])})
		for _, applies := range refMaps(step["applies_to_routes"]) {
			routing := refString(applies["routing_step_id"])
			if routing == "" {
				continue
			}
			target := m.steps[routing]
			if target == nil {
				m.add(RefMapIssue{Kind: "eval_route_step_missing", Severity: refSeverityBreak, Source: source, Ref: routing,
					Detail: fmt.Sprintf("applies_to_routes names routing step %q, which is not in the plan", routing)})
				continue
			}
			for _, routeID := range refStrings(applies["route_ids"]) {
				if !target.routes[routeID] {
					m.add(RefMapIssue{Kind: "eval_route_missing", Severity: refSeverityBreak, Source: source, Ref: routing + ":" + routeID,
						Detail: fmt.Sprintf("applies_to_routes names route %q, which %s no longer has", routeID, routing)})
				}
			}
		}
	}
}

// retired step ids: every step id a plan-mod changelog entry ever named that
// is no longer in the plan. Parsed changelog files are cached by size+mtime.
var refChangelogCache = struct {
	sync.Mutex
	files map[string]refChangelogFile
}{files: map[string]refChangelogFile{}}

type refChangelogFile struct {
	size  int64
	mtime time.Time
	ids   []string
}

func (m *referenceMap) loadRetired() {
	dir := filepath.Join(m.root, PlanningFolderName, "changelog")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	all := map[string]bool{}
	refChangelogCache.Lock()
	defer refChangelogCache.Unlock()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		cached, ok := refChangelogCache.files[path]
		if !ok || cached.size != info.Size() || !cached.mtime.Equal(info.ModTime()) {
			cached = refChangelogFile{size: info.Size(), mtime: info.ModTime(), ids: readChangelogStepIDs(path)}
			refChangelogCache.files[path] = cached
		}
		for _, id := range cached.ids {
			all[id] = true
		}
	}
	for id := range all {
		if m.steps[id] == nil && !m.evalIDs[id] && !strings.HasPrefix(id, "__") && strings.Contains(id, "-") {
			m.retired[id] = true
		}
	}
}

func readChangelogStepIDs(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file struct {
		Entries []struct {
			StepIDs []string `json:"step_ids"`
		} `json:"entries"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, entry := range file.Entries {
		for _, id := range entry.StepIDs {
			if id = strings.TrimSpace(id); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// loadDocs reads the prose that names steps and paths: KB notes, rules and
// context, soul.md, and learnings SKILL.md files and _global references.
func (m *referenceMap) loadDocs() {
	var files []string
	_ = filepath.WalkDir(filepath.Join(m.root, "knowledgebase"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	files = append(files, filepath.Join(m.root, "soul", "soul.md"))
	_ = filepath.WalkDir(filepath.Join(m.root, "learnings", "_global"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if entries, err := os.ReadDir(filepath.Join(m.root, "learnings")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "_global" && !strings.HasPrefix(entry.Name(), ".") {
				files = append(files, filepath.Join(m.root, "learnings", entry.Name(), "SKILL.md"))
			}
		}
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(m.root, path)
		rel = filepath.ToSlash(rel)
		name := strings.ToLower(filepath.Base(rel))
		m.docs = append(m.docs, refText{source: "file:" + rel, rel: rel, text: string(raw),
			isLog: strings.Contains(name, "change-log") || strings.Contains(name, "changelog") || strings.Contains(name, "history")})
		m.scanned++
	}
}

var refIndexSkip = map[string]bool{"runs": true, ".cache": true, ".local": true, ".tmp": true, "node_modules": true, "__pycache__": true, ".git": true, "diffs": true, "changelog": true, "revisions": true, "backup": true, "archive": true, ".venv": true}

func (m *referenceMap) buildIndex() {
	count := 0
	for _, top := range []string{"code", "learnings", "knowledgebase", "soul", "db", "evaluation", "reports", "variables"} {
		_ = filepath.WalkDir(filepath.Join(m.root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if refIndexSkip[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			count++
			if count > 20000 {
				return filepath.SkipAll
			}
			m.index[d.Name()] = true
			return nil
		})
	}
}

func (m *referenceMap) add(issue RefMapIssue) {
	key := issue.Kind + "|" + issue.Source + "|" + issue.Ref
	if m.seen[key] {
		return
	}
	m.seen[key] = true
	m.issues = append(m.issues, issue)
}

func (m *referenceMap) check() {
	m.checkDependencies()
	for _, s := range m.order {
		m.checkText("step:"+s.id, s.text, false, s.id)
		m.checkInputs("step:"+s.id, s.inputs)
	}
	for _, e := range m.evals {
		m.checkText(e.source, e.text, false, "")
	}
	for _, d := range m.docs {
		m.checkText(d.source, d.text, d.isLog, "")
	}
	m.checkStepConfig()
	m.checkUnconsumed()
}

func (m *referenceMap) checkDependencies() {
	for _, s := range m.order {
		for _, dep := range s.deps {
			if isRouteSelectionDependency(dep) {
				continue
			}
			if strings.Contains(dep, "/") {
				m.checkRelativeScriptDependency(s, dep)
				m.checkPathDependency(s, dep)
				continue
			}
			if len(m.producer[dep]) > 0 {
				continue
			}
			if declarers := m.declarer[dep]; len(declarers) > 0 {
				// Dependencies resolve only against a producer's context_output.
				// A scripted/agent step then gets a path in its own folder
				// (staging reports "input file not found"; main.py gets a missing
				// argv path); a message_sequence step gets only the bare name.
				severity, effect := refSeverityBreak, "it resolves to this step's own folder, where the file is not"
				if s.typ == string(StepTypeMessageSeq) {
					severity, effect = refSeverityWarn, "the agent gets only the bare name and must find the file itself"
				}
				m.add(RefMapIssue{Kind: "dependency_not_staged", Severity: severity, Source: "step:" + s.id, Ref: dep,
					Detail: fmt.Sprintf("context_dependencies names %s; %s writes it (validation_schema) but no context_output lists it, so %s. Fix: list %s in %s's context_output (comma-separated); do not switch the dependency to a ../ path unless main.py resolves it", dep, strings.Join(declarers, ", "), effect, dep, declarers[0])})
				continue
			}
			m.add(RefMapIssue{Kind: "dependency_unproduced", Severity: refSeverityBreak, Source: "step:" + s.id, Ref: dep,
				Detail: fmt.Sprintf("context_dependencies names %s, which no step produces", dep)})
		}
	}
}

// checkRelativeScriptDependency flags a ../step/file dependency on a scripted
// step whose main.py never reads STEP_OUTPUT_DIR. The platform passes a
// dependency containing a slash through unchanged and runs the script from
// code/<step>, so such a script opens the path relative to its own code folder
// and the file is not there (bid-record, 2026-10-06). outreach-record resolves
// relative inputs against STEP_OUTPUT_DIR, which is what makes the form valid.
func (m *referenceMap) checkRelativeScriptDependency(s *refStep, dep string) {
	if s.typ != string(StepTypeRegular) || filepath.IsAbs(dep) || strings.HasPrefix(dep, "$") {
		return
	}
	raw, err := os.ReadFile(filepath.Join(m.root, "code", s.id, "main.py"))
	src := string(raw)
	if err != nil || strings.Contains(src, "STEP_OUTPUT_DIR") && (strings.Contains(src, "is_absolute") || strings.Contains(src, "isabs")) {
		return
	}
	m.add(RefMapIssue{Kind: "relative_dependency_unresolved", Severity: refSeverityBreak, Source: "step:" + s.id, Ref: dep,
		Detail: fmt.Sprintf("context_dependencies passes %s unchanged and code/%s/main.py does not resolve paths against STEP_OUTPUT_DIR, so it opens code/%s/%s. List the file in its producer's context_output and use the bare name, or resolve the argument against STEP_OUTPUT_DIR in main.py", dep, s.id, s.id, dep)})
}

func (m *referenceMap) checkPathDependency(s *refStep, dep string) {
	parts := strings.Split(filepath.ToSlash(dep), "/")
	file := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		seg := parts[i]
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if p := m.steps[seg]; p != nil {
			if file != "" && !p.declared[file] {
				m.add(RefMapIssue{Kind: "undeclared_step_file", Severity: refSeverityWarn, Source: "step:" + s.id, Ref: dep,
					Detail: fmt.Sprintf("context_dependencies reads %s from %s, which does not declare it", file, seg)})
			}
			return
		}
		if refStepIDShape(seg) {
			m.add(RefMapIssue{Kind: "missing_step_ref", Severity: refSeverityBreak, Source: "step:" + s.id, Ref: dep,
				Detail: fmt.Sprintf("context_dependencies reads from step folder %s, which is not in the plan", seg)})
			return
		}
		return
	}
}

// checkText finds workflow paths, step folders and retired step ids.
func (m *referenceMap) checkText(source, text string, isLog bool, selfID string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for _, match := range refWorkflowPathRe.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[2], match[3]
		if end < len(text) && strings.ContainsRune("{<*$", rune(text[end])) {
			continue
		}
		path := strings.TrimRight(text[start:end], ".")
		if strings.Contains(path, "..") || strings.HasPrefix(path, "evaluation/runs") || strings.HasSuffix(path, "/") && strings.Count(path, "/") == 1 {
			continue
		}
		// Prose like "soul/plan" is not a path: require a file extension or an
		// explicit trailing slash for a folder.
		if !strings.HasSuffix(path, "/") && !strings.Contains(filepath.Base(path), ".") {
			continue
		}
		// A template name (db/posts/YYYY-MM-DD-x.json) is a pattern, not a file.
		if refPlaceholderRe.MatchString(path) {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(path))); err == nil {
			continue
		}
		// Runs create files under db/ and reports/, so a missing one is not
		// clearly a break: it stays visible but does not make Plan Drift due.
		if strings.HasPrefix(path, "db/") || strings.HasPrefix(path, "reports/") {
			m.add(RefMapIssue{Kind: "missing_data_file", Severity: refSeverityWarn, Source: source, Ref: path,
				Detail: fmt.Sprintf("names %s, a runtime data path that does not exist now; a run may create it, or the name is stale", path)})
			continue
		}
		kind, detail := "missing_file", fmt.Sprintf("names %s, which does not exist in the workflow", path)
		parts := strings.Split(path, "/")
		if len(parts) > 1 && (parts[0] == "learnings" || parts[0] == "code") && m.retired[parts[1]] {
			kind, detail = "retired_step_path", fmt.Sprintf("names %s, a folder of retired step %s", path, parts[1])
		}
		m.add(RefMapIssue{Kind: kind, Severity: refSeverityBreak, Source: source, Ref: path, Detail: detail})
	}
	for _, match := range refStepFolderRe.FindAllStringSubmatchIndex(text, -1) {
		prefix := text[match[2]:match[3]]
		id := text[match[4]:match[5]]
		file := ""
		if match[6] >= 0 {
			file = text[match[6]:match[7]]
		}
		if refWorkflowDirs[id] {
			continue
		}
		// $VAR_TARGET_RUN_PATH/<id>/ names a step folder; ../<id>/<file> does
		// only when it names a file in it (../workspace-docs/... is not a step).
		explicit := strings.Contains(prefix, "RUN_PATH") || strings.HasPrefix(prefix, "..") && file != ""
		if s := m.steps[id]; s != nil {
			if file != "" && explicit && len(s.declared) > 0 && !s.declared[file] {
				m.add(RefMapIssue{Kind: "undeclared_step_file", Severity: refSeverityWarn, Source: source, Ref: id + "/" + file,
					Detail: fmt.Sprintf("reads %s from %s, which does not declare it (context_output or validation_schema)", file, id)})
			}
			continue
		}
		if m.retired[id] {
			m.add(RefMapIssue{Kind: "retired_step", Severity: refSeverityBreak, Source: source, Ref: id,
				Detail: fmt.Sprintf("names step folder %s/, a step no longer in the plan", id)})
			continue
		}
		if explicit && !m.evalIDs[id] {
			ref := id + "/"
			if file != "" {
				ref += file
			}
			m.add(RefMapIssue{Kind: "missing_step_ref", Severity: refSeverityBreak, Source: source, Ref: ref,
				Detail: fmt.Sprintf("reads from step folder %s/, which is not in the plan", id)})
		}
	}
	if isLog {
		return
	}
	for _, id := range m.retiredSorted() {
		if id != selfID && refHasToken(text, id) {
			m.add(RefMapIssue{Kind: "retired_step", Severity: refSeverityBreak, Source: source, Ref: id,
				Detail: fmt.Sprintf("names %s, a step no longer in the plan", id)})
		}
	}
}

// checkInputs checks bare file names in a step's ## Inputs / ## Guides.
func (m *referenceMap) checkInputs(source, inputs string) {
	for _, match := range refBareFileRe.FindAllStringSubmatch(inputs, -1) {
		name := match[1]
		if len(m.producer[name]) > 0 || len(m.declarer[name]) > 0 || m.index[name] || isRouteSelectionDependency(name) {
			continue
		}
		m.add(RefMapIssue{Kind: "missing_input", Severity: refSeverityBreak, Source: source, Ref: name,
			Detail: fmt.Sprintf("Inputs/Guides name %s, which no step produces and no workflow file has", name)})
	}
}

func (m *referenceMap) checkStepConfig() {
	raw, err := os.ReadFile(filepath.Join(m.root, PlanningFolderName, "step_config.json"))
	if err != nil {
		return
	}
	configs, err := ParseStepConfigContent(string(raw))
	if err != nil {
		return
	}
	for _, cfg := range configs {
		id := strings.TrimSpace(cfg.ID)
		if id == "" || strings.HasPrefix(id, "__") {
			continue
		}
		if m.steps[id] == nil {
			m.add(RefMapIssue{Kind: "config_without_step", Severity: refSeverityInfo, Source: "step_config:" + id, Ref: id,
				Detail: "step_config.json has an entry for a step not in the plan (maintain_plan cleanup_orphan_configs removes it)"})
			continue
		}
		if cfg.AgentConfigs == nil {
			continue
		}
		for _, path := range cfg.AgentConfigs.AdditionalReadPaths {
			path = strings.Trim(strings.TrimSpace(path), "/")
			if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "*{$") {
				continue
			}
			if _, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(path))); err != nil {
				m.add(RefMapIssue{Kind: "missing_file", Severity: refSeverityBreak, Source: "step_config:" + id, Ref: path,
					Detail: fmt.Sprintf("additional_read_paths names %s, which does not exist in the workflow", path)})
			}
		}
	}
}

func (m *referenceMap) checkUnconsumed() {
	for _, s := range m.order {
		for _, out := range s.outputs {
			if isRouteSelectionDependency(out) || m.consumed(s.id, out) {
				continue
			}
			m.add(RefMapIssue{Kind: "output_unconsumed", Severity: refSeverityInfo, Source: "step:" + s.id, Ref: out,
				Detail: fmt.Sprintf("%s is read by no step dependency, eval, note or guide", out)})
		}
	}
}

func (m *referenceMap) consumed(producer, out string) bool {
	for _, s := range m.order {
		for _, dep := range s.deps {
			if dep == out || strings.HasSuffix(dep, "/"+out) {
				return true
			}
		}
		if s.id != producer && refHasToken(s.text, out) {
			return true
		}
	}
	for _, e := range m.evals {
		if refHasToken(e.text, out) {
			return true
		}
	}
	for _, d := range m.docs {
		if refHasToken(d.text, out) {
			return true
		}
	}
	return false
}

func (m *referenceMap) report() ReferenceMapReport {
	r := ReferenceMapReport{ScannedFiles: m.scanned, Note: referenceMapNote}
	rank := map[string]int{refSeverityBreak: 0, refSeverityWarn: 1, refSeverityInfo: 2}
	issues := append([]RefMapIssue(nil), m.issues...)
	sort.SliceStable(issues, func(i, j int) bool {
		if rank[issues[i].Severity] != rank[issues[j].Severity] {
			return rank[issues[i].Severity] < rank[issues[j].Severity]
		}
		if issues[i].Source != issues[j].Source {
			return issues[i].Source < issues[j].Source
		}
		return issues[i].Ref < issues[j].Ref
	})
	for _, issue := range issues {
		switch issue.Severity {
		case refSeverityBreak:
			r.Breaks++
		case refSeverityWarn:
			r.Warnings++
		default:
			r.Infos++
		}
	}
	if len(issues) > refMapMaxIssues {
		issues = issues[:refMapMaxIssues]
		r.Truncated = true
	}
	r.Issues = issues
	r.RetiredStepIDs = m.retiredSorted()
	return r
}

func (m *referenceMap) retiredSorted() []string {
	ids := make([]string, 0, len(m.retired))
	for id := range m.retired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Compact keeps the counts and the first max issues (breaks sort first), for
// payloads with a size budget such as Pulse state.
func (r ReferenceMapReport) Compact(max int) ReferenceMapReport {
	if max >= 0 && len(r.Issues) > max {
		r.Issues = append([]RefMapIssue(nil), r.Issues[:max]...)
		r.Truncated = true
	}
	return r
}

// ---- edit-time notes ----

// ReferenceNotesForSteps is appended to a plan edit's response: the dependents
// of each changed step (consumers of its outputs, evals, notes and guides
// naming it or its outputs) and the open breaks that involve it. previous
// holds output files the steps had before the edit, so consumers of a renamed
// or deleted output are still listed. Empty when nothing depends on it.
func ReferenceNotesForSteps(workspacePath string, stepIDs []string, previous map[string][]string) string {
	if len(stepIDs) == 0 {
		return ""
	}
	m, err := loadReferenceMap(refMapRoot(workspacePath))
	if err != nil || m == nil {
		return ""
	}
	var b strings.Builder
	for _, id := range refUnique(stepIDs) {
		// Everything the step writes: a consumer may (wrongly) depend on a
		// declared file that is not in context_output, and that is a dependent.
		outputs := append([]string(nil), previous[id]...)
		if s := m.steps[id]; s != nil {
			outputs = append(outputs, s.outputs...)
			for file := range s.declared {
				outputs = append(outputs, file)
			}
			sort.Strings(outputs[len(previous[id])+len(s.outputs):])
		}
		outputs = refUnique(outputs)
		var consumers, evals, docs, steps, breaks []string
		for _, s := range m.order {
			if s.id == id {
				continue
			}
			for _, dep := range s.deps {
				for _, out := range outputs {
					if dep == out || strings.HasSuffix(dep, "/"+out) {
						consumers = append(consumers, fmt.Sprintf("%s (%s)", s.id, out))
					}
				}
			}
			if refHasToken(s.text, id) {
				steps = append(steps, s.id)
			}
		}
		for _, e := range m.evals {
			if refNames(e.text, id, outputs) {
				evals = append(evals, strings.TrimPrefix(e.source, "eval:"))
			}
		}
		for _, d := range m.docs {
			if refNames(d.text, id, outputs) {
				docs = append(docs, d.rel)
			}
		}
		for _, issue := range m.issues {
			if issue.Severity == refSeverityInfo {
				continue
			}
			if issue.Source == "step:"+id || refHasToken(issue.Ref, id) || refContains(outputs, issue.Ref) {
				breaks = append(breaks, fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Source, issue.Detail))
			}
		}
		if len(consumers)+len(evals)+len(docs)+len(steps)+len(breaks) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:", id)
		refLine(&b, "consumers of its outputs", consumers)
		refLine(&b, "steps naming it", steps)
		refLine(&b, "evals naming it or its outputs", evals)
		refLine(&b, "notes/guides naming it or its outputs", docs)
		refLine(&b, "open breaks", breaks)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nReference map (PLAT-561): dependents of this change. If the edit changed what they rely on (an output, a path, a rule), update them in this same change; this is a report, not a gate." + b.String() + m.totalsLine()
}

// ReferenceNotesForFiles is appended to a workspace file write of a KB note,
// soul.md, the evaluation plan or a learnings file: the breaks that file now
// carries. relPaths are workflow-relative.
func ReferenceNotesForFiles(workspacePath string, relPaths []string) string {
	var tracked []string
	for _, rel := range relPaths {
		if ReferenceMapTracksFile(rel) {
			tracked = append(tracked, filepath.ToSlash(rel))
		}
	}
	if len(tracked) == 0 {
		return ""
	}
	m, err := loadReferenceMap(refMapRoot(workspacePath))
	if err != nil || m == nil {
		return ""
	}
	var lines []string
	for _, issue := range m.issues {
		if issue.Severity == refSeverityInfo {
			continue
		}
		for _, rel := range tracked {
			evalFile := rel == "evaluation/evaluation_plan.json" && strings.HasPrefix(issue.Source, "eval:")
			if issue.Source == "file:"+rel || evalFile {
				lines = append(lines, fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.Source, issue.Detail))
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nReference map (PLAT-561): the file you wrote names things that do not resolve. Fix them in this change if they are stale; this is a report, not a gate.")
	refLine(&b, "", lines)
	b.WriteString(m.totalsLine())
	return b.String()
}

// ReferenceMapTracksFile reports whether a workflow-relative path is prose the
// map reads (KB, soul, evaluation plan, learnings markdown).
func ReferenceMapTracksFile(rel string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(rel)), "./")
	switch {
	case rel == "evaluation/evaluation_plan.json", rel == "soul/soul.md":
		return true
	case strings.HasPrefix(rel, "knowledgebase/") && strings.HasSuffix(rel, ".md"):
		return true
	case strings.HasPrefix(rel, "learnings/") && strings.HasSuffix(rel, ".md"):
		return true
	}
	return false
}

func (m *referenceMap) totalsLine() string {
	breaks, warns := 0, 0
	for _, issue := range m.issues {
		switch issue.Severity {
		case refSeverityBreak:
			breaks++
		case refSeverityWarn:
			warns++
		}
	}
	return fmt.Sprintf("\nWorkflow-wide: %d breaks, %d warnings (get_plan_prompt_health reference_map lists them).", breaks, warns)
}

// ReferenceMapStepOutputs reads the current context_output files of the given
// steps, so a plan edit can pass them as previous outputs.
func ReferenceMapStepOutputs(workspacePath string, stepIDs []string) map[string][]string {
	// context_output and validation_schema files, before the edit.
	if len(stepIDs) == 0 {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(refMapRoot(workspacePath), PlanningFolderName, "plan.json"))
	if err != nil {
		return nil
	}
	m := &referenceMap{steps: map[string]*refStep{}, producer: map[string][]string{}, declarer: map[string][]string{}}
	if m.loadPlan(raw) != nil {
		return nil
	}
	out := map[string][]string{}
	for _, id := range stepIDs {
		if s := m.steps[id]; s != nil {
			files := append([]string(nil), s.outputs...)
			for file := range s.declared {
				files = append(files, file)
			}
			out[id] = refUnique(files)
		}
	}
	return out
}

// referenceMapPlanEditTool names the native plan-mod operations whose
// responses carry the dependents note.
func referenceMapPlanEditTool(name string) bool {
	switch name {
	case "delete_plan_steps", "update_step_config", "update_validation_schema", "change_step_type",
		"convert_routing_branch_step_type", "restore_step_from_changelog":
		return true
	}
	if strings.HasPrefix(name, "add_") || strings.HasPrefix(name, "update_") || strings.HasPrefix(name, "delete_") {
		return strings.HasSuffix(name, "_step") || strings.HasSuffix(name, "_route")
	}
	return false
}

// withReferenceMapNotes appends the dependents of the edited steps to a plan
// edit's response when the edit actually changed plan.json or step_config.json.
// It never changes the edit's outcome.
func withReferenceMapNotes(workspacePath string, execute func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		ids := referenceMapArgStepIDs(args)
		if len(ids) == 0 || strings.TrimSpace(workspacePath) == "" {
			return execute(ctx, args)
		}
		before := referenceMapPlanFingerprint(workspacePath)
		previous := ReferenceMapStepOutputs(workspacePath, ids)
		out, err := execute(ctx, args)
		if err != nil || before == "" || referenceMapPlanFingerprint(workspacePath) == before {
			return out, err
		}
		return out + ReferenceNotesForSteps(workspacePath, ids, previous), nil
	}
}

func referenceMapArgStepIDs(args map[string]interface{}) []string {
	var ids []string
	for _, key := range []string{"existing_step_id", "step_id", "parent_step_id", "id"} {
		if id := strings.TrimSpace(refString(args[key])); id != "" {
			ids = append(ids, id)
		}
	}
	for _, key := range []string{"deleted_step_ids", "step_ids"} {
		ids = append(ids, refStrings(args[key])...)
	}
	if nested, ok := args["sub_agent_step"].(map[string]interface{}); ok {
		if id := strings.TrimSpace(refString(nested["id"])); id != "" {
			ids = append(ids, id)
		}
	}
	return refUnique(ids)
}

func referenceMapPlanFingerprint(workspacePath string) string {
	root := refMapRoot(workspacePath)
	h := sha256.New()
	found := false
	for _, name := range []string{"plan.json", "step_config.json"} {
		raw, err := os.ReadFile(filepath.Join(root, PlanningFolderName, name))
		if err == nil {
			found = true
		}
		h.Write(raw)
		h.Write([]byte{0})
	}
	if !found {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// ---- helpers ----

func refLine(b *strings.Builder, label string, items []string) {
	items = refUnique(items)
	if len(items) == 0 {
		return
	}
	const max = 8
	more := ""
	if len(items) > max {
		more = fmt.Sprintf(" (+%d more)", len(items)-max)
		items = items[:max]
	}
	if label == "" {
		for _, item := range items {
			b.WriteString("\n- " + item)
		}
		if more != "" {
			b.WriteString("\n-" + more)
		}
		return
	}
	fmt.Fprintf(b, "\n- %s: %s%s", label, strings.Join(items, "; "), more)
}

func refNames(text, id string, outputs []string) bool {
	if refHasToken(text, id) {
		return true
	}
	for _, out := range outputs {
		if refHasToken(text, out) {
			return true
		}
	}
	return false
}

// refHasToken matches tok not embedded in a longer id or file name.
func refHasToken(text, tok string) bool {
	if tok == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(text[from:], tok)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(tok)
		before := start == 0 || !refTokenChar(text[start-1])
		after := end == len(text) || !refTokenChar(text[end]) || (text[end] == '.' && (end+1 == len(text) || !refTokenChar(text[end+1])))
		if before && after {
			return true
		}
		from = start + 1
	}
}

func refTokenChar(c byte) bool {
	return c == '-' || c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

var refStepIDShapeRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)

func refStepIDShape(s string) bool { return refStepIDShapeRe.MatchString(s) }

func refOutputs(v interface{}) []string {
	var out []string
	for _, s := range refStrings(v) {
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func refSchemaFiles(v interface{}, into map[string]bool) {
	switch schema := v.(type) {
	case string:
		var parsed interface{}
		if json.Unmarshal([]byte(schema), &parsed) == nil {
			refSchemaFiles(parsed, into)
		}
	case map[string]interface{}:
		for _, f := range refMaps(schema["files"]) {
			if name := strings.TrimSpace(refString(f["file_name"])); name != "" {
				into[filepath.Base(name)] = true
			}
		}
	}
}

func refString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func refStrings(v interface{}) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []interface{}:
		var out []string
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func refMaps(v interface{}) []map[string]interface{} {
	list, _ := v.([]interface{})
	var out []map[string]interface{}
	for _, item := range list {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func refContains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func refUnique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range list {
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
