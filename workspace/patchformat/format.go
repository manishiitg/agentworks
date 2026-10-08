package patchformat

import (
	"fmt"
	"strings"
)

const applyPatchBeginMarker = "*** Begin Patch"

// ApplyPatchSection is one file of a "*** Begin Patch" patch, rewrapped as a
// single-file patch.
type ApplyPatchSection struct {
	Path  string
	IsAdd bool
	Patch string
}

// IsApplyPatchFormat reports whether diff is a "*** Begin Patch" patch.
func IsApplyPatchFormat(diff string) bool {
	return strings.HasPrefix(strings.TrimSpace(diff), applyPatchBeginMarker)
}

// SplitApplyPatch splits a "*** Begin Patch" patch into one patch per file.
// Delete File and Move to are refused, as the single-file tool refuses them.
func SplitApplyPatch(diff string) ([]ApplyPatchSection, error) {
	if !IsApplyPatchFormat(diff) {
		return nil, nil
	}
	var sections []ApplyPatchSection
	var body []string
	var current *ApplyPatchSection
	flush := func() {
		if current == nil {
			return
		}
		current.Patch = applyPatchBeginMarker + "\n" + strings.Join(body, "\n") + "\n*** End Patch\n"
		sections = append(sections, *current)
		current, body = nil, nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, applyPatchBeginMarker):
			continue
		case strings.HasPrefix(line, "*** End Patch"):
			flush()
			return sections, nil
		case strings.HasPrefix(line, "*** Update File:"), strings.HasPrefix(line, "*** Add File:"):
			flush()
			isAdd := strings.HasPrefix(line, "*** Add File:")
			path := strings.TrimSpace(line[strings.Index(line, ":")+1:])
			if path == "" {
				return nil, fmt.Errorf("%q names no file", line)
			}
			current = &ApplyPatchSection{Path: path, IsAdd: isAdd}
			body = []string{line}
		case strings.HasPrefix(line, "*** Delete File:"):
			return nil, fmt.Errorf("*** Delete File is not supported by diff_patch_workspace_file; use the workspace delete tool")
		case strings.HasPrefix(line, "*** Move to:"):
			return nil, fmt.Errorf("*** Move to is not supported by diff_patch_workspace_file; use the workspace move tool, then patch the file")
		default:
			if current == nil {
				if strings.TrimSpace(line) != "" {
					return nil, fmt.Errorf("patch has content before an *** Update File or *** Add File header: %q", line)
				}
				continue
			}
			body = append(body, line)
		}
	}
	flush()
	return sections, nil
}
