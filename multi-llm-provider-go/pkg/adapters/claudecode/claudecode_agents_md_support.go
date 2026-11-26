package claudecode

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// claudeMinAgentsMDVersion is the oldest Claude Code known to read AGENTS.md as
// project instructions (verified 2.1.284 and 2.1.285; 2.1.233 does not).
//
// In project-instruction-only mode the session prompt is carried solely by the
// AGENTS.md block, so a Claude that ignores that file would run every session
// with no system prompt and no error. A binary older than this, or one whose
// version cannot be read, gets the prompt through --system-prompt-file instead
// (a duplicate at worst, never a gap).
const claudeMinAgentsMDVersion = "2.1.284"

var claudeVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// claudeBinaryVersion reports the version of the claude binary on PATH. A var so
// tests can stub it.
var claudeBinaryVersion = func() (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output() // #nosec G204 - the resolved claude binary
	if err != nil {
		return "", err
	}
	return path + " " + strings.TrimSpace(string(out)), nil
}

var claudeAgentsMDSupport sync.Map // "path version" -> bool

// claudeReadsAgentsMD reports whether the claude binary on PATH is new enough to
// read AGENTS.md. The answer is cached per binary and version.
func claudeReadsAgentsMD() bool {
	identity, err := claudeBinaryVersion()
	if err != nil {
		return false
	}
	if cached, ok := claudeAgentsMDSupport.Load(identity); ok {
		return cached.(bool)
	}
	supported := claudeVersionAtLeast(identity, claudeMinAgentsMDVersion)
	claudeAgentsMDSupport.Store(identity, supported)
	return supported
}

// claudeVersionAtLeast reports whether the first x.y.z found in text is >= min.
func claudeVersionAtLeast(text, min string) bool {
	have := claudeVersionPattern.FindStringSubmatch(text)
	want := claudeVersionPattern.FindStringSubmatch(min)
	if have == nil || want == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		h, _ := strconv.Atoi(have[i])
		w, _ := strconv.Atoi(want[i])
		if h != w {
			return h > w
		}
	}
	return true
}
