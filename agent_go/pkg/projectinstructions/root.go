package projectinstructions

import (
	"fmt"
	"strings"
)

// FileStatus describes routing, not proof that a CLI read a file.
type FileStatus struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	Method string `json:"method"` // injected, native, found, or link (only points at AGENTS.md)
}

type RootResult struct {
	Section string
	Key     string
	Files   []FileStatus
}

// Root reads only root files. Nested instructions retain their folder scope.
// nativeFiles is declared by the adapter; nativeRoot is true only when the
// original source is the CLI's actual working directory, not a runtime link.
// The caller supplies its existing authorized workspace reader.
func Root(nativeFiles []string, nativeRoot bool, read func(string) (string, bool, error)) (RootResult, error) {
	var result RootResult
	var fingerprint strings.Builder
	names := []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}
	contents := map[string]string{}
	for _, name := range names {
		content, found, err := read(name)
		if err != nil {
			return RootResult{}, fmt.Errorf("cannot read project instructions in %s: %w", name, err)
		}
		if found {
			contents[name] = content
		}
	}
	declared := map[string]bool{}
	for _, name := range nativeFiles {
		declared[name] = true
	}
	// AGENTS.md is the one shared rules file (owner, 25 Nov). A CLAUDE.md or
	// GEMINI.md that only links to it (`@AGENTS.md`) adds nothing of its own,
	// and a CLI that reads such a link natively at this root reads AGENTS.md too.
	if _, ok := contents["AGENTS.md"]; ok && nativeRoot {
		for _, name := range names[1:] {
			if content, ok := contents[name]; ok && declared[name] && IsAgentsLink(content) {
				declared["AGENTS.md"] = true
			}
		}
	}
	for _, name := range names {
		content, found := contents[name]
		if !found {
			continue
		}
		native := declared[name]
		method := "found"
		if name != "AGENTS.md" && IsAgentsLink(content) {
			method = "link"
		} else if native && nativeRoot {
			method = "native"
		} else if name == "AGENTS.md" || native {
			method = "injected"
			if section := SourceSection(name, content); section != "" {
				result.Section += "\n\n" + section
			}
		}
		result.Files = append(result.Files, FileStatus{Name: name, Bytes: len(content), Method: method})
		if method != "found" && method != "link" {
			// Native files also refresh retained sessions, without duplicating
			// their contents in the platform's system prompt.
			fmt.Fprintf(&fingerprint, "%s:%s:%s\n", name, method, Key(content))
		}
	}
	result.Section = strings.TrimSpace(result.Section)
	result.Key = Key(fingerprint.String())
	return result, nil
}

// IsAgentsLink reports whether content only imports AGENTS.md: every non-blank
// line is `@AGENTS.md` or `@./AGENTS.md`. Claude Code and Gemini-style CLIs
// load the linked file, so such a CLAUDE.md or GEMINI.md has no rules of its own.
func IsAgentsLink(content string) bool {
	seen := false
	for _, line := range strings.Split(content, "\n") {
		switch strings.TrimSpace(line) {
		case "":
		case "@AGENTS.md", "@./AGENTS.md":
			seen = true
		default:
			return false
		}
	}
	return seen
}
