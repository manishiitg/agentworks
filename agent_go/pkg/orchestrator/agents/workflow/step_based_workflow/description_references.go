package step_based_workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"
)

// Naming a workflow path in Inputs, Resources or Guides grants read access
// (never write). The system lists where it can be read, without promoting the
// contents to system instructions. Brain notes retain project access checks.

// BrainNoteReader reads one Brain note as the run's person, limited by the
// project's Brain access (off/read/write/folders) and that person's folder
// roles, and never writes. Set by the server; nil means Brain is unavailable.
var BrainNoteReader func(ctx context.Context, workspacePath, notePath string) (string, error)

type descriptionReference struct {
	// Path is workflow-relative (slash form) for workflow files, or the
	// Brain organization-relative note path when Brain is true.
	Path  string
	Brain bool
}

var (
	descriptionSectionHeading = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)
	brainReferencePattern     = regexp.MustCompile(`brain:([^\s` + "`" + `'"<>()\[\],;]+)`)
	// A workflow path starts at a token boundary, optionally after
	// Workflow/<name>/ (or an absolute docs-root prefix ending in it).
	workflowReferencePattern = regexp.MustCompile(`(?:^|[\s` + "`" + `'"(\[<])(?:[^\s` + "`" + `'"()\[\]<>]*?Workflow/[^/\s` + "`" + `'"]+/)?((?:learnings|knowledgebase|code/shared|soul)/[^\s` + "`" + `'"<>()\[\],;]+|db/README\.md)`)
)

// descriptionReferenceSections returns the bodies of the description's
// `## Inputs`, `## Resources` and `## Guides` sections, in document order.
func descriptionReferenceSections(description string) []string {
	var sections []string
	var current *strings.Builder
	currentLevel := 0
	for _, line := range strings.Split(description, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := descriptionSectionHeading.FindStringSubmatch(trimmed); m != nil {
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			if current != nil && level <= currentLevel {
				sections = append(sections, current.String())
				current = nil
			}
			if current == nil && level == 2 {
				switch strings.ToLower(strings.TrimSpace(m[1])) {
				case "inputs", "resources", "guides":
					current, currentLevel = &strings.Builder{}, level
					continue
				}
			}
		}
		if current != nil {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	if current != nil {
		sections = append(sections, current.String())
	}
	return sections
}

// parseDescriptionReferences extracts the workflow paths and Brain notes the
// description's Inputs, Resources and Guides sections name, deduplicated, in order.
// Paths that would leave the workflow, globs and unresolved placeholders are
// skipped: they name nothing deliverable.
func parseDescriptionReferences(description string) []descriptionReference {
	var refs []descriptionReference
	seen := map[string]bool{}
	add := func(ref descriptionReference) {
		key := fmt.Sprintf("%t:%s", ref.Brain, ref.Path)
		if ref.Path == "" || seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	for _, section := range descriptionReferenceSections(description) {
		type hit struct {
			at  int
			ref descriptionReference
		}
		var hits []hit
		for _, m := range brainReferencePattern.FindAllStringSubmatchIndex(section, -1) {
			if path := cleanBrainReference(section[m[2]:m[3]]); path != "" {
				hits = append(hits, hit{m[2], descriptionReference{Path: path, Brain: true}})
			}
		}
		for _, m := range workflowReferencePattern.FindAllStringSubmatchIndex(section, -1) {
			if path := cleanWorkflowReference(section[m[2]:m[3]]); path != "" {
				hits = append(hits, hit{m[2], descriptionReference{Path: path}})
			}
		}
		// Keep the order in which the description names them.
		for i := 1; i < len(hits); i++ {
			for j := i; j > 0 && hits[j].at < hits[j-1].at; j-- {
				hits[j], hits[j-1] = hits[j-1], hits[j]
			}
		}
		for _, h := range hits {
			add(h.ref)
		}
	}
	return refs
}

func trimReferencePunctuation(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ".,:;!?)]}*_'\"")
}

func cleanWorkflowReference(raw string) string {
	value := trimReferencePunctuation(raw)
	if value == "" || strings.ContainsAny(value, "{}*?") {
		return ""
	}
	normalized, err := normalizeAdditionalReadPaths([]string{value})
	if err != nil || len(normalized) != 1 {
		return ""
	}
	clean := normalized[0]
	if clean == "db/README.md" {
		return clean
	}
	for _, root := range []string{"learnings/", "knowledgebase/", "code/shared/", "soul/"} {
		if strings.HasPrefix(clean, root) && len(clean) > len(root) {
			return clean
		}
	}
	return ""
}

func cleanBrainReference(raw string) string {
	value := trimReferencePunctuation(raw)
	if value == "" || strings.ContainsAny(value, "{}*?") {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if strings.HasPrefix(clean, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !strings.Contains(clean, "/") {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		clean += ".md"
	}
	return clean
}

// resolvedWorkflowReference locates a named workflow path on disk. It reports
// the absolute path and whether it exists inside the workflow root (a symlink
// leading out of the workflow counts as missing and is never granted).
func resolvedWorkflowReference(workspacePath, relativePath string) (abs string, info os.FileInfo, ok bool) {
	root := filepath.Join(GetPromptDocsRoot(), workspacePath)
	abs = filepath.Join(root, filepath.FromSlash(relativePath))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return abs, nil, false
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil, false
	}
	if rel, relErr := filepath.Rel(realRoot, realPath); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs, nil, false
	}
	info, err = os.Stat(realPath)
	if err != nil {
		return abs, nil, false
	}
	return abs, info, true
}

// localKnowledgebaseRetired reports a project cut over to shared Brain: its
// local knowledgebase/ is denied to steps, and naming a path there must not
// bring it back.
func localKnowledgebaseRetired(workspacePath string) bool {
	shared, _ := workflowkb.SharedConfig(GetPromptDocsRoot(), workspacePath)
	return shared
}

// descriptionReferenceReadPaths returns the folder-guard read grants for the
// existing workflow paths a description names. Read only: callers never add
// these to write paths.
func descriptionReferenceReadPaths(workspacePath, description string) []string {
	var paths []string
	for _, ref := range parseDescriptionReferences(description) {
		if ref.Brain {
			continue
		}
		if strings.HasPrefix(ref.Path, KnowledgebaseFolderName+"/") && localKnowledgebaseRetired(workspacePath) {
			continue
		}
		if _, _, ok := resolvedWorkflowReference(workspacePath, ref.Path); ok {
			paths = append(paths, filepath.Join(workspacePath, filepath.FromSlash(ref.Path)))
		}
	}
	return paths
}

// appendDescriptionReferenceReadPaths adds the description's named paths to
// a step's read paths.
func appendDescriptionReferenceReadPaths(readPaths []string, workspacePath, description string) []string {
	if extra := descriptionReferenceReadPaths(workspacePath, description); len(extra) > 0 {
		return append(readPaths, extra...)
	}
	return readPaths
}

// missingDescriptionReferences lists the workflow paths a description names
// that do not exist. Brain notes are not checked here (that needs the run's
// person); the step prompt reports an unreadable note at run time.
func missingDescriptionReferences(workspacePath, description string) []string {
	var missing []string
	for _, ref := range parseDescriptionReferences(description) {
		if ref.Brain {
			continue
		}
		if _, _, ok := resolvedWorkflowReference(workspacePath, ref.Path); !ok {
			missing = append(missing, ref.Path)
		}
	}
	return missing
}

// descriptionReferencesEditNotice is the short warning a plan edit returns
// when the saved description names a workflow path that does not exist.
func descriptionReferencesEditNotice(workspacePath, description string) string {
	missing := missingDescriptionReferences(workspacePath, description)
	if len(missing) == 0 {
		return ""
	}
	return "\n\nWarning: the description's Inputs/Resources/Guides name paths that do not exist in this workflow: " + strings.Join(missing, ", ") +
		". The step is told they are missing at run time; create them or fix the names."
}

// buildReferencedGuidesSection renders the "Referenced guides" block for a
// step's system prompt. Empty when the description names nothing.
func buildReferencedGuidesSection(ctx context.Context, workspacePath, description string, warn func(string)) string {
	refs := parseDescriptionReferences(description)
	if len(refs) == 0 {
		return ""
	}
	docsRoot := GetPromptDocsRoot()
	kbRetired := false
	kbRetiredChecked := false
	var listed []string
	for _, ref := range refs {
		if ref.Brain {
			label := "brain:" + ref.Path
			if BrainNoteReader == nil {
				listed = append(listed, fmt.Sprintf("- `%s`: Brain is not available on this server; this note was not delivered.", label))
				warn(fmt.Sprintf("referenced Brain note %s not delivered: Brain is not available", label))
				continue
			}
			_, err := BrainNoteReader(ctx, workspacePath, ref.Path)
			if err != nil {
				listed = append(listed, fmt.Sprintf("- `%s`: this project cannot read this Brain note (%v); it was not delivered.", label, err))
				warn(fmt.Sprintf("referenced Brain note %s not delivered: %v", label, err))
				continue
			}
			listed = append(listed, fmt.Sprintf("- `%s`: read with brain_read path=%s", label, ref.Path))
			continue
		}
		if strings.HasPrefix(ref.Path, KnowledgebaseFolderName+"/") {
			if !kbRetiredChecked {
				kbRetired, kbRetiredChecked = localKnowledgebaseRetired(workspacePath), true
			}
			if kbRetired {
				listed = append(listed, fmt.Sprintf("- `%s`: this project uses shared Brain instead of its local knowledgebase/; this path was not delivered.", ref.Path))
				continue
			}
		}
		abs, info, ok := resolvedWorkflowReference(workspacePath, ref.Path)
		promptPath := abs
		if docsRoot == "" {
			promptPath = filepath.Join(workspacePath, filepath.FromSlash(ref.Path))
		}
		if !ok {
			listed = append(listed, fmt.Sprintf("- `%s`: does not exist in this workflow.", ref.Path))
			warn(fmt.Sprintf("referenced path %s does not exist", ref.Path))
			continue
		}
		if info.IsDir() {
			listed = append(listed, fmt.Sprintf("- `%s/`: folder, readable for this step; read the files you need: %s", ref.Path, promptPath))
			continue
		}
		listed = append(listed, fmt.Sprintf("- `%s`: readable at %s", ref.Path, promptPath))
	}
	var b strings.Builder
	b.WriteString("## Referenced guides\n\n")
	b.WriteString("These references are named by the task. Read their content when needed.\n")
	if len(listed) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(listed, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// referencedGuidesForStep builds the section for a step and logs a warning
// for every reference it could not deliver.
func (hcpo *StepBasedWorkflowOrchestrator) referencedGuidesForStep(ctx context.Context, stepID, description string) string {
	return buildReferencedGuidesSection(ctx, hcpo.GetWorkspacePath(), description, func(message string) {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [REFERENCED_GUIDES] step %s: %s", stepID, message))
	})
}
