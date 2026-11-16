// Package skillproject writes Anthropic-format SKILL.md folders into a
// provider's working directory. Every native-skill CLI (claude-code,
// cursor-cli, codex-cli, pi-cli) reads the
// same file layout — SKILL.md with YAML frontmatter plus optional
// scripts/, references/, assets/ subdirectories — so adapters share
// this single writer and differ only in which subdirectory they project
// into (.claude/skills/, .cursor/skills/, .agents/skills/).
//
// The helper is intentionally simple: idempotent overwrite. Callers can
// invoke it at both session launch and resume; if the skill content is
// unchanged on disk the write is a (cheap) no-op rewrite. Folders an
// earlier write projected (they carry the marker) for skills that are no
// longer in the set are removed, so a skill dropped from the product stops
// being listed to the model; folders without the marker are never touched.
package skillproject

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

// Write projects every skill into targetDir as <targetDir>/<skill.Name>/
// containing SKILL.md (with synthesized YAML frontmatter) plus any
// SupportingFiles. targetDir is created if it does not exist.
//
// Skill names are sanitized to a safe folder name (lowercase letters,
// digits, hyphens). Skills with empty names are skipped silently —
// adapters should not pass them in but the helper is defensive.
func Write(targetDir string, skills []*llmtypes.Skill) error {
	if len(skills) == 0 {
		return nil
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("skillproject: create %s: %w", targetDir, err)
	}
	written := map[string]bool{}
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		folder := sanitizeFolderName(skill.Name)
		if folder == "" {
			continue
		}
		written[folder] = true
		skillDir := filepath.Join(targetDir, folder)
		markerPath := filepath.Join(skillDir, projectfile.SkillMarkerFile)
		// A folder of this name with no marker was written by an earlier
		// version (before markers) or is the project's own skill. The skills
		// we project have platform-specific names, and leaving old ones
		// unmarked would keep them stale forever, so a same-named folder is
		// taken over and marked. Anything with a different name is untouched.
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return fmt.Errorf("skillproject: create %s: %w", skillDir, err)
		}
		if err := os.WriteFile(markerPath, []byte("written by AgentWorks for a session; safe to remove\n"), 0o644); err != nil {
			return fmt.Errorf("skillproject: write %s: %w", markerPath, err)
		}
		body := renderSkillMarkdown(skill, folder)
		skillPath := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(skillPath, []byte(body), 0o644); err != nil {
			return fmt.Errorf("skillproject: write %s: %w", skillPath, err)
		}
		for _, sf := range skill.SupportingFiles {
			if sf.RelPath == "" {
				continue
			}
			// Defense in depth: refuse anything that would escape the
			// skill folder via traversal. Callers should not send such
			// paths, but a malicious imported skill could.
			clean := filepath.Clean(sf.RelPath)
			if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
				continue
			}
			fullPath := filepath.Join(skillDir, clean)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				return fmt.Errorf("skillproject: mkdir for %s: %w", fullPath, err)
			}
			if err := os.WriteFile(fullPath, sf.Content, 0o644); err != nil {
				return fmt.Errorf("skillproject: write %s: %w", fullPath, err)
			}
		}
	}
	pruneStale(targetDir, written)
	return nil
}

// Prune removes every skill folder an earlier Write projected into targetDir
// (marked folders only; the project's own skills stay). Used when the list of
// attached skills became empty, so a kept process stops seeing them.
func Prune(targetDir string) {
	pruneStale(targetDir, map[string]bool{})
}

// pruneStale removes marked folders of skills this write did not project. A
// chat's runtime kept six skills for days after they left the product, and the
// CLI listed them to the model on every turn (5 Nov).
func pruneStale(targetDir string, written map[string]bool) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || written[entry.Name()] {
			continue
		}
		dir := filepath.Join(targetDir, entry.Name())
		if info, err := os.Lstat(filepath.Join(dir, projectfile.SkillMarkerFile)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

// renderSkillMarkdown builds the SKILL.md file content: YAML frontmatter
// synthesized from the Skill fields plus the body.
func renderSkillMarkdown(skill *llmtypes.Skill, folderName string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(folderName)
	b.WriteString("\n")
	if d := strings.TrimSpace(skill.Description); d != "" {
		b.WriteString("description: ")
		b.WriteString(yamlScalar(d))
		b.WriteString("\n")
	}
	if len(skill.Paths) > 0 {
		b.WriteString("paths:\n")
		for _, p := range skill.Paths {
			b.WriteString("  - ")
			b.WriteString(yamlScalar(p))
			b.WriteString("\n")
		}
	}
	if skill.DisableModelInvocation {
		b.WriteString("disable-model-invocation: true\n")
	}
	if len(skill.Metadata) > 0 {
		// Sort keys for deterministic output (matters for tests and for
		// idempotent writes — same input must produce byte-identical file).
		keys := make([]string, 0, len(skill.Metadata))
		for k := range skill.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("metadata:\n")
		for _, k := range keys {
			b.WriteString("  ")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(yamlScalar(skill.Metadata[k]))
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n")
	if body := strings.TrimRight(skill.Content, "\n"); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// yamlScalar quotes a scalar value when it contains characters that
// would otherwise need YAML escaping. Keeps the unquoted form for
// simple single-line values to match how skill authors typically write
// them by hand.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\n\"'\\[]{},&*?|<>=!%@`") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

// sanitizeFolderName lowercases, trims, and replaces unsafe characters
// with hyphens so the folder name is portable across filesystems.
func sanitizeFolderName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
