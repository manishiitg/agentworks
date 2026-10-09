//go:build windows

package workflowfiles

import (
	"regexp"
	"strings"
)

var shortNamePart = regexp.MustCompile(`^[^~]{1,6}~[0-9]+(\.[^.]{0,3})?$`)

// windowsRefusedPathPart reports path components that Windows resolves differently from how they are spelled, so a textual
// blocked-path or read-only rule would not recognise them: a trailing dot or space is dropped ("blocked." is "blocked"), 8.3
// short names ("BLOCKE~1") name the same folder, ":" opens an alternate data stream, and device names (CON, NUL, COM1 ...) are
// not files at all.
func windowsRefusedPathPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
		return true
	}
	if strings.ContainsAny(part, `<>:"|?*`) {
		return true
	}
	for _, r := range part {
		if r < 0x20 {
			return true
		}
	}
	if shortNamePart.MatchString(part) {
		return true
	}
	base := part
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(strings.ToUpper(base), " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
