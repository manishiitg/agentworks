package projectinstructions

import (
	"fmt"
	"strings"
)

// FileStatus describes routing, not proof that a CLI read a file.
type FileStatus struct {
	Name   string `json:"name"`
	Bytes  int    `json:"bytes"`
	Method string `json:"method"` // injected, native, or found
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
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		content, found, err := read(name)
		if err != nil {
			return RootResult{}, fmt.Errorf("cannot read project instructions in %s: %w", name, err)
		}
		if !found {
			continue
		}
		native := false
		for _, declared := range nativeFiles {
			if declared == name {
				native = true
				break
			}
		}
		method := "found"
		if native && nativeRoot {
			method = "native"
		} else if name == "AGENTS.md" || native {
			method = "injected"
			if section := SourceSection(name, content); section != "" {
				result.Section += "\n\n" + section
			}
		}
		result.Files = append(result.Files, FileStatus{Name: name, Bytes: len(content), Method: method})
		if method != "found" {
			// Native files also refresh retained sessions, without duplicating
			// their contents in the platform's system prompt.
			fmt.Fprintf(&fingerprint, "%s:%s:%s\n", name, method, Key(content))
		}
	}
	result.Section = strings.TrimSpace(result.Section)
	result.Key = Key(fingerprint.String())
	return result, nil
}
