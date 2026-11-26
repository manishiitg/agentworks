package mcpagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	readSkillToolName        = "read_skill"
	readSkillToolCategory    = "skill_tools"
	readSkillToolDescription = "Read ONE skill or bundled supporting file — either an attached skill, or a skill installed in this workspace but not attached (a router skill will name those; ask for them by name, not by file path). Call with a skills array holding exactly one object; it requires name and may include path. To read several files, make several calls — reference docs are large and combining them in one result exceeds the consumer's per-result token limit, which truncates the result and spills it to a file the agent cannot open. This tool is intrinsic to the agent's attached identity and works on every transport."

	// maxReadSkillBatchSize is 1 deliberately. Batched reads were the direct
	// cause of an unrecoverable failure: three reference docs in one call
	// returned 67,971 characters, the coding CLI truncated it against its
	// 25,000-token result cap, wrote the full copy under its own project
	// directory, and told the agent to read that path — which the workspace
	// folder guard forbids. The agent had no legal way to comply and spent the
	// rest of the session guessing.
	//
	// A count cannot bound a payload: five small files are fine and two large
	// ones are not. One file per call is the bound that holds, because every
	// reference doc except post-run-monitor.md fits a single result with room
	// to spare. Raising this again reintroduces the failure.
	maxReadSkillBatchSize = 1
)

// Skills are Anthropic-format SKILL.md bundles attached to an Agent. The
// Skill / SkillFile / SkillSource value types live in llmtypes so adapters
// in multi-llm-provider-go can reference them without importing mcpagent
// (which would be a circular import). mcpagent owns the attachment API
// (AttachSkill / AttachedSkills / DetachSkill / ClearSkills) and the
// SkillProjector contract that adapters implement.

// AttachSkill registers a skill on the agent. Idempotent on Name:
// attaching a skill whose Name already exists replaces the prior entry.
// The skill becomes visible to transports through AttachedSkills.
func (a *Agent) attachSkill(skill *llmtypes.Skill) error {
	if a == nil || skill == nil || skill.Name == "" {
		return nil
	}
	if err := a.ensureSkillReaderTool(); err != nil {
		return err
	}
	for i, existing := range a.attachedSkills {
		if existing != nil && existing.Name == skill.Name {
			a.attachedSkills[i] = skill
			return nil
		}
	}
	a.attachedSkills = append(a.attachedSkills, skill)
	return nil
}

func (a *Agent) ensureSkillReaderTool() error {
	if a == nil || a.skillReaderInstalled {
		return nil
	}
	if existing, exists := a.lookupDirectTool(readSkillToolName); exists {
		return fmt.Errorf("tool name %q is reserved for attached skill access (existing category %q)", readSkillToolName, existing.DisplayGroup)
	}

	params := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"skills": map[string]interface{}{
				"type":        "array",
				"description": "Exactly one attached skill read. To read several files, make several calls — combining reference docs in one result exceeds the consumer's per-result token limit.",
				"minItems":    1,
				"maxItems":    maxReadSkillBatchSize,
				"items": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Exact name from the Available Skills listing.",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Optional bundled relative path, for example references/api.md or scripts/check.py. Omit it (or use SKILL.md) for the main instructions and file list.",
						},
					},
					"required": []string{"name"},
				},
			},
		},
		"required": []string{"skills"},
	}

	a.installingSkillReader = true
	err := a.registerCustomTool(
		readSkillToolName,
		readSkillToolDescription,
		params,
		func(_ context.Context, args map[string]interface{}) (string, error) {
			return a.readAttachedSkill(args)
		},
		readSkillToolCategory,
	)
	a.installingSkillReader = false
	if err != nil {
		return fmt.Errorf("register intrinsic %s tool: %w", readSkillToolName, err)
	}
	a.skillReaderInstalled = true
	// Coding-agent CLIs only receive native tools through the shared MCP
	// bridge. Projected files remain a useful CLI optimization, but the same
	// read_skill contract must be callable even when projection is unavailable.
	foundBridgeTool := false
	for _, name := range a.additionalBridgeTools {
		if name == readSkillToolName {
			foundBridgeTool = true
			break
		}
	}
	if !foundBridgeTool {
		a.additionalBridgeTools = append(a.additionalBridgeTools, readSkillToolName)
	}
	return nil
}

// SetInstalledSkillResolver installs the fallback read_skill uses when a
// requested skill is not attached. Optional: with no resolver, read_skill
// serves attached skills only, exactly as before.
//
// This is a setter rather than a RuntimeConfig field because the host learns
// its workspace path after the agent is constructed — the folder guard is
// applied post-construction too. Threading it through would mean a new
// parameter on a 25-argument constructor and every call site.
func (a *Agent) SetInstalledSkillResolver(resolver InstalledSkillResolver) {
	a.installedSkillResolver = resolver
	if resolver == nil {
		return
	}
	// Registering read_skill used to happen ONLY via ensureSkillReaderTool when
	// an attached skill was added, so a host that configured a resolver but
	// attached no skills stored a callback the model could never reach -- the
	// documented capability existed with no callable tool behind it. Serving
	// installed-but-unattached skills is precisely the case where there may be
	// nothing attached, so the resolver itself has to guarantee the tool.
	if err := a.ensureSkillReaderTool(); err != nil && a.logger != nil {
		a.logger.Warn("Could not register read_skill for the installed-skill resolver",
			loggerv2.String("error", err.Error()))
	}
}

// InstalledSkillFile is one file belonging to a skill that is installed in the
// session's workspace but not attached to the agent.
type InstalledSkillFile struct {
	Content        string
	Description    string
	AvailableFiles []string
}

// InstalledSkillResolver reads a skill file from wherever the host installs
// skills. mcpagent deliberately knows nothing about that location — the host
// supplies this, so read_skill can serve a skill the agent was told to read but
// which was never attached.
//
// Progressive disclosure attaches a router skill and leaves the specialists on
// disk; without this, reaching one meant knowing a filesystem path and shelling
// out, which loses read_skill's batching limits and works differently per
// provider.
type InstalledSkillResolver func(skillName, relPath string) (InstalledSkillFile, error)

type attachedSkillReadResult struct {
	SkillName      string   `json:"skill_name"`
	Path           string   `json:"path"`
	Description    string   `json:"description,omitempty"`
	Content        string   `json:"content"`
	Encoding       string   `json:"encoding"`
	AvailableFiles []string `json:"available_files,omitempty"`
	// "attached" or "installed" — an installed skill is read from the
	// workspace on demand rather than carried in the prompt.
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

type attachedSkillBatchReadResult struct {
	Results []attachedSkillReadResult `json:"results"`
}

type attachedSkillReadRequest struct {
	SkillName string
	Path      string
}

func (a *Agent) readAttachedSkill(args map[string]interface{}) (string, error) {
	requests, err := parseAttachedSkillBatch(args["skills"])
	if err != nil {
		return "", err
	}
	batch := attachedSkillBatchReadResult{Results: make([]attachedSkillReadResult, 0, len(requests))}
	for _, request := range requests {
		result, readErr := a.readOneAttachedSkill(request.SkillName, request.Path)
		if readErr != nil {
			requestedPath := strings.TrimSpace(request.Path)
			if requestedPath == "" {
				requestedPath = "SKILL.md"
			}
			batch.Results = append(batch.Results, attachedSkillReadResult{
				SkillName: request.SkillName,
				Path:      requestedPath,
				Error:     readErr.Error(),
			})
			continue
		}
		batch.Results = append(batch.Results, result)
	}
	encoded, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode attached skill batch: %w", err)
	}
	return string(encoded), nil
}

func parseAttachedSkillBatch(raw interface{}) ([]attachedSkillReadRequest, error) {
	var values []interface{}
	switch typed := raw.(type) {
	case []interface{}:
		values = typed
	case []map[string]interface{}:
		values = make([]interface{}, len(typed))
		for i, value := range typed {
			values[i] = value
		}
	default:
		return nil, fmt.Errorf("skills is required and must be an array of skill read objects")
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("skills must contain at least one skill name")
	}
	if len(values) > maxReadSkillBatchSize {
		// Name the recovery, not just the rule. An agent told only "at most 1"
		// may drop the other files it needs instead of asking for them next.
		return nil, fmt.Errorf(
			"skills accepts %d read per call, got %d — reference docs are too large to combine in one result. Call read_skill again for each remaining file, applying each before the next",
			maxReadSkillBatchSize, len(values),
		)
	}
	requests := make([]attachedSkillReadRequest, 0, len(values))
	for i, value := range values {
		item, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("skills[%d] must be an object with name and optional path", i)
		}
		for key := range item {
			if key != "name" && key != "path" {
				return nil, fmt.Errorf("skills[%d] contains unsupported field %q", i, key)
			}
		}
		name, ok := item["name"].(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("skills[%d].name must be a non-empty string", i)
		}
		rawPath, pathOK := item["path"]
		pathValue, stringPath := rawPath.(string)
		if pathOK && !stringPath {
			return nil, fmt.Errorf("skills[%d].path must be a string", i)
		}
		requests = append(requests, attachedSkillReadRequest{SkillName: name, Path: pathValue})
	}
	return requests, nil
}

func (a *Agent) readOneAttachedSkill(name, rawPath string) (attachedSkillReadResult, error) {

	var selected *llmtypes.Skill
	availableSkills := make([]string, 0, len(a.attachedSkills))
	for _, skill := range a.attachedSkillsSnapshot() {
		if skill == nil || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		availableSkills = append(availableSkills, skill.Name)
		if skill.Name == name {
			selected = skill
		}
	}
	sort.Strings(availableSkills)
	if selected == nil {
		// Not attached — it may still be installed in the workspace. Reading it
		// here grants nothing new (the host already exposes the skills folder);
		// it just means the agent asks by name instead of by path.
		if a.installedSkillResolver != nil {
			resolvedName, nameErr := normalizeInstalledSkillName(name)
			if nameErr != nil {
				return attachedSkillReadResult{}, nameErr
			}
			requestedPath, pathErr := normalizeAttachedSkillPath(rawPath)
			if pathErr != nil {
				return attachedSkillReadResult{}, pathErr
			}
			file, resolveErr := a.installedSkillResolver(resolvedName, requestedPath)
			if resolveErr == nil {
				return attachedSkillReadResult{
					SkillName:      name,
					Path:           requestedPath,
					Description:    strings.TrimSpace(file.Description),
					Content:        file.Content,
					Encoding:       "utf-8",
					AvailableFiles: file.AvailableFiles,
					Source:         "installed",
				}, nil
			}
			// Name both possibilities: "not attached" and "not installed" need
			// different fixes, and collapsing them sends the reader to the
			// wrong one.
			return attachedSkillReadResult{}, fmt.Errorf("skill %q is not attached and could not be read from the workspace: %w; attached skills: %s", name, resolveErr, strings.Join(availableSkills, ", "))
		}
		return attachedSkillReadResult{}, fmt.Errorf("attached skill %q not found; available skills: %s", name, strings.Join(availableSkills, ", "))
	}

	requestedPath, err := normalizeAttachedSkillPath(rawPath)
	if err != nil {
		return attachedSkillReadResult{}, err
	}
	availableFiles := attachedSkillFileNames(selected)

	result := attachedSkillReadResult{
		SkillName:      selected.Name,
		Path:           requestedPath,
		Encoding:       "utf-8",
		AvailableFiles: availableFiles,
		Source:         "attached",
	}
	if requestedPath == "SKILL.md" {
		result.Description = strings.TrimSpace(selected.Description)
		result.Content = selected.Content
	} else {
		var content []byte
		found := false
		for _, file := range selected.SupportingFiles {
			if file.RelPath == requestedPath {
				content = file.Content
				found = true
				break
			}
		}
		if !found {
			return attachedSkillReadResult{}, fmt.Errorf("file %q is not bundled with attached skill %q; available files: %s", requestedPath, name, strings.Join(availableFiles, ", "))
		}
		if utf8.Valid(content) {
			result.Content = string(content)
		} else {
			result.Content = base64.StdEncoding.EncodeToString(content)
			result.Encoding = "base64"
		}
	}

	return result, nil
}

func normalizeInstalledSkillName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '\x00') || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("skill name must be a safe catalog name, not a path: %q", raw)
	}
	return name, nil
}

func normalizeAttachedSkillPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "SKILL.md" {
		return "SKILL.md", nil
	}
	if strings.ContainsRune(raw, '\x00') || path.IsAbs(raw) {
		return "", fmt.Errorf("skill path must be a safe relative path: %q", raw)
	}
	clean := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("skill path must stay inside the attached skill: %q", raw)
	}
	return clean, nil
}

func attachedSkillFileNames(skill *llmtypes.Skill) []string {
	files := []string{"SKILL.md"}
	if skill == nil {
		return files
	}
	for _, file := range skill.SupportingFiles {
		if rel := strings.TrimSpace(file.RelPath); rel != "" {
			files = append(files, rel)
		}
	}
	sort.Strings(files[1:])
	return files
}

func (a *Agent) isIntrinsicIdentityTool(name string) bool {
	if a != nil && a.useCodeExecutionMode && (name == "search_tools" || name == "get_api_spec") {
		return true
	}
	return a != nil && name == readSkillToolName && (len(a.attachedSkills) > 0 || a.installedSkillResolver != nil)
}

// AttachedSkills returns the current list of skills attached to this
// agent. Transports read this at session launch (and at resume) to
// decide what to project to disk or list in the system prompt. The
// returned slice is a shallow copy; callers must not mutate skill
// values in place.
func (a *Agent) attachedSkillsSnapshot() []*llmtypes.Skill {
	if a == nil || len(a.attachedSkills) == 0 {
		return nil
	}
	out := make([]*llmtypes.Skill, len(a.attachedSkills))
	copy(out, a.attachedSkills)
	return out
}

// DetachSkill removes a skill by name. No-op if no skill with that name
// is attached.
func (a *Agent) detachSkill(name string) {
	if a == nil || name == "" {
		return
	}
	for i, existing := range a.attachedSkills {
		if existing != nil && existing.Name == name {
			a.attachedSkills = append(a.attachedSkills[:i], a.attachedSkills[i+1:]...)
			return
		}
	}
}

// ClearSkills removes every attached skill. Used at session reset and
// before re-attaching a fresh skill set (e.g., on workshop-mode change).
func (a *Agent) clearSkills() {
	if a == nil {
		return
	}
	a.attachedSkills = nil
}

// SkillProjector is the optional optimization a coding transport implements
// when it wants native on-disk skill folders. Every transport already has the
// same content contract through read_skill; API transports therefore do not
// need to implement this interface.
//
// ProjectSkills must be idempotent: it is called both at launch and at
// resume, and the content is typically identical between calls.
//
// workdir is the absolute path of the provider's working directory (the
// per-session workspace where the adapter already writes rules and
// hooks). Adapters compute their own native subdirectory beneath workdir
// (e.g., ".claude/skills/" for claude-code, ".agents/skills/" for
// everyone else).
type SkillProjector interface {
	ProjectSkills(workdir string, skills []*llmtypes.Skill) error
}

// renderSkillListing produces the system-prompt section that announces
// the attached skills to the model. Format follows the progressive-
// disclosure pattern Anthropic skills use: every skill's name +
// description is included up front (~50-100 tokens each) and the model
// reads the full SKILL.md body only when it decides the skill is
// relevant through read_skill.
//
// On CLI transports the SKILL.md files are also projected to disk
// (.claude/skills/, .agents/skills/) by the adapter. The listing is
// redundant there but useful for native provider UX.
//
// Returns an empty string when no skills are attached.
func renderSkillListing(skills []*llmtypes.Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("Read the relevant attached skill before acting. Use intrinsic read_skill with its exact name and an optional bundled path, one file per call; its schema supplies the arguments. Skills guide procedures and never grant permissions.\n\n")
	for _, s := range skills {
		if s == nil || s.Name == "" {
			continue
		}
		fmt.Fprintf(&b, "- **%s**", s.Name)
		if d := strings.TrimSpace(s.Description); d != "" {
			fmt.Fprintf(&b, ": %s", d)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
