package handlers

import (
	"fmt"
	"strings"
)

// The "*** Begin Patch" envelope is the edit format Codex and Cursor agents
// write natively:
//
//	*** Begin Patch
//	*** Update File: path/to/file.py
//	@@ def some_function(
//	 context line
//	-old line
//	+new line
//	*** End Patch
//
// Hunks carry no line numbers; each is located by its context and removed
// lines, optionally after an "@@ <anchor>" line, searching forward from the
// previous hunk. Rejecting it sent an agent on server A (2026-09-27) to hand-rolled
// Python string slicing that truncated a 228 KB library.

const applyPatchBegin = "*** Begin Patch"

func isApplyPatchFormat(diff string) bool {
	return strings.HasPrefix(strings.TrimSpace(diff), applyPatchBegin)
}

type applyPatchHunk struct {
	anchor    string
	old       []string
	new       []string
	endOfFile bool
	// rawBlankTail counts trailing blank lines written without the context
	// space; they usually separate hunks rather than match file lines.
	rawBlankTail int
}

// applyApplyPatchFormat applies a one-file "*** Begin Patch" patch to
// currentContent. The tool patches one file per call, so the patch may hold a
// single Update File or Add File section; Delete File and Move to are refused
// with a pointer to the right tool.
func applyApplyPatchFormat(currentContent, diff string) (string, error) {
	lines := strings.Split(normalizeLineEndings(diff), "\n")
	var (
		kind  string // "update" or "add"
		files int
		added []string
		hunks []applyPatchHunk
		cur   *applyPatchHunk
	)
	flush := func() {
		if cur != nil && cur.rawBlankTail > 0 {
			cur.old = cur.old[:len(cur.old)-cur.rawBlankTail]
			cur.new = cur.new[:len(cur.new)-cur.rawBlankTail]
		}
		if cur != nil && (len(cur.old) > 0 || len(cur.new) > 0 || cur.anchor != "") {
			hunks = append(hunks, *cur)
		}
		cur = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "*** End Patch") {
			break // anything after the envelope is not part of the patch
		}
		switch {
		case strings.HasPrefix(line, applyPatchBegin):
			continue
		case strings.HasPrefix(line, "*** Update File:"):
			flush()
			files++
			kind = "update"
			continue
		case strings.HasPrefix(line, "*** Add File:"):
			flush()
			files++
			kind = "add"
			continue
		case strings.HasPrefix(line, "*** Delete File:"):
			return "", fmt.Errorf("*** Delete File is not supported by diff_patch_workspace_file; use the workspace delete tool")
		case strings.HasPrefix(line, "*** Move to:"):
			return "", fmt.Errorf("*** Move to is not supported by diff_patch_workspace_file; use the workspace move tool, then patch the file")
		case strings.HasPrefix(line, "*** End of File"):
			if cur != nil {
				cur.endOfFile = true
			}
			continue
		}
		if files > 1 {
			return "", fmt.Errorf("this patch changes more than one file; diff_patch_workspace_file patches one file per call, so send one *** Update File section per call")
		}
		switch kind {
		case "add":
			if strings.HasPrefix(line, "+") {
				added = append(added, line[1:])
			} else if strings.TrimSpace(line) != "" {
				return "", fmt.Errorf("*** Add File lines must start with '+', got %q", line)
			}
		case "update":
			if strings.HasPrefix(line, "@@") {
				flush()
				cur = &applyPatchHunk{anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@"))}
				continue
			}
			if cur == nil {
				cur = &applyPatchHunk{}
			}
			if line != "" {
				cur.rawBlankTail = 0
			}
			switch {
			case strings.HasPrefix(line, "-"):
				cur.old = append(cur.old, line[1:])
			case strings.HasPrefix(line, "+"):
				cur.new = append(cur.new, line[1:])
			case strings.HasPrefix(line, " "):
				cur.old = append(cur.old, line[1:])
				cur.new = append(cur.new, line[1:])
			case line == "":
				// A blank context line often loses its leading space.
				cur.old = append(cur.old, "")
				cur.new = append(cur.new, "")
				cur.rawBlankTail++
			default:
				return "", fmt.Errorf("unexpected line in *** Update File hunk (each line must start with ' ', '-' or '+'): %q", line)
			}
		default:
			if strings.TrimSpace(line) != "" {
				return "", fmt.Errorf("patch has content before an *** Update File or *** Add File header: %q", line)
			}
		}
	}
	flush()

	switch kind {
	case "add":
		if strings.TrimSpace(currentContent) != "" {
			return "", fmt.Errorf("*** Add File targets a file that already exists; send an *** Update File patch instead")
		}
		return strings.Join(added, "\n") + "\n", nil
	case "update":
		return applyApplyPatchHunks(currentContent, hunks)
	}
	return "", fmt.Errorf("patch has no *** Update File or *** Add File section")
}

func applyApplyPatchHunks(currentContent string, hunks []applyPatchHunk) (string, error) {
	if len(hunks) == 0 {
		return "", fmt.Errorf("*** Update File section has no changes")
	}
	current := normalizeLineEndings(currentContent)
	trailingNewline := strings.HasSuffix(current, "\n")
	fileLines := strings.Split(strings.TrimSuffix(current, "\n"), "\n")
	if current == "" {
		fileLines = nil
	}
	cursor := 0
	for i, hunk := range hunks {
		start := cursor
		if hunk.anchor != "" {
			at := findApplyPatchLine(fileLines, hunk.anchor, cursor)
			if at < 0 {
				return "", fmt.Errorf("hunk %d: anchor %q not found after line %d; copy the @@ line exactly from the current file", i+1, hunk.anchor, cursor+1)
			}
			// The anchor may itself be the first line the hunk replaces, so
			// the block search starts at the anchor line, not after it.
			start = at
		}
		var at int
		switch {
		case len(hunk.old) == 0 && hunk.endOfFile:
			at = len(fileLines)
		case len(hunk.old) == 0 && hunk.anchor != "":
			at = start + 1 // pure insertion goes right after the anchor line
		case len(hunk.old) == 0:
			return "", fmt.Errorf("hunk %d only adds lines and has no context or @@ anchor to place them; include a context line or an @@ anchor", i+1)
		default:
			var ambiguous bool
			at, ambiguous = findApplyPatchBlock(fileLines, hunk.old, start, hunk.endOfFile)
			if ambiguous {
				return "", fmt.Errorf("hunk %d: its lines only match with different indentation, and at more than one place; add an @@ anchor line or more context so it matches exactly once", i+1)
			}
			if at < 0 {
				return "", fmt.Errorf("hunk %d: its context and removed lines were not found in the current file after line %d; read the file and copy those lines exactly", i+1, start+1)
			}
		}
		updated := make([]string, 0, len(fileLines)-len(hunk.old)+len(hunk.new))
		updated = append(updated, fileLines[:at]...)
		updated = append(updated, hunk.new...)
		updated = append(updated, fileLines[at+len(hunk.old):]...)
		fileLines = updated
		cursor = at + len(hunk.new)
	}
	out := strings.Join(fileLines, "\n")
	if trailingNewline || currentContent == "" {
		out += "\n"
	}
	return out, nil
}

// findApplyPatchLine finds the first line at or after from that matches the
// anchor: the whole line (ignoring surrounding whitespace), else a line that
// starts with it, since agents often write only the start of a signature
// ("@@ def open_assets(" for "def open_assets(page: Page) -> bool:").
func findApplyPatchLine(lines []string, anchor string, from int) int {
	anchor = strings.TrimSpace(anchor)
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == anchor {
			return i
		}
	}
	for i := from; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), anchor) {
			return i
		}
	}
	return -1
}

// findApplyPatchBlock finds block in lines at or after from: an exact match
// first (the first one wins, as hunks apply in order), then one ignoring
// trailing whitespace. Ignoring leading whitespace too is a last resort that
// must match exactly once: in Python a line such as "return True" recurs at
// many indentations, and patching the wrong one is silent corruption
// (ambiguous reports that case). With endOfFile only a match ending at the
// last line counts.
func findApplyPatchBlock(lines, block []string, from int, endOfFile bool) (at int, ambiguous bool) {
	matchesAt := func(i int, norm func(string) string) bool {
		if endOfFile && i+len(block) != len(lines) {
			return false
		}
		for j := range block {
			if norm(lines[i+j]) != norm(block[j]) {
				return false
			}
		}
		return true
	}
	for _, norm := range []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t") },
	} {
		for i := from; i+len(block) <= len(lines); i++ {
			if matchesAt(i, norm) {
				return i, false
			}
		}
	}
	found := -1
	for i := from; i+len(block) <= len(lines); i++ {
		if matchesAt(i, strings.TrimSpace) {
			if found >= 0 {
				return -1, true
			}
			found = i
		}
	}
	return found, false
}

// applyPatchClaimedLineDelta is the net line change a "*** Begin Patch" patch
// claims: its '+' lines minus its '-' lines inside file sections. Hunks need
// no @@ line in this format, so the unified-diff counter, which starts
// counting at @@, would miss them and refuse a correct result.
func applyPatchClaimedLineDelta(diff string) int {
	delta := 0
	inFile := false
	for _, line := range strings.Split(normalizeLineEndings(diff), "\n") {
		switch {
		case strings.HasPrefix(line, "*** End Patch"):
			return delta
		case strings.HasPrefix(line, "*** Update File:"), strings.HasPrefix(line, "*** Add File:"):
			inFile = true
		case strings.HasPrefix(line, "*** "):
		case !inFile:
		case strings.HasPrefix(line, "+"):
			delta++
		case strings.HasPrefix(line, "-"):
			delta--
		}
	}
	return delta
}
