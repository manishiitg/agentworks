// Package projectinstructions renders a Code or Crew project's own
// PROJECT_INSTRUCTIONS.md as the last section of the agent's instructions
// (PLAT-692).
//
// The platform writes AGENTS.md, CLAUDE.md, GEMINI.md and each CLI's own
// equivalent every turn, and tools may not change them (managed projection
// guard). A person who wants standing project rules ("we always use pnpm")
// writes them in PROJECT_INSTRUCTIONS.md at the project root instead: it is an
// ordinary project file the person and the project's agent may edit, and its
// text is appended below the platform's instructions on every turn. The
// platform part always comes first and is never replaced.
package projectinstructions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// FileName is the user-owned file at the project root.
const FileName = "PROJECT_INSTRUCTIONS.md"

// MaxBytes caps how much of the file reaches the prompt.
const MaxBytes = 32 * 1024

// Heading starts the appended section.
const Heading = "## Project instructions (from PROJECT_INSTRUCTIONS.md, written by the project's owner)"

// sessionMarker is the name inside the marked block the provider library
// writes into AGENTS.md (projectfile). User text that contained it could end
// that block early and leave part of the prompt behind on cleanup.
const sessionMarker = "agentworks-session-instructions"

// Section returns the text to append after the platform's instructions, or ""
// when the file is empty.
func Section(content string) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, sessionMarker, "agentworks session instructions"))
	if content == "" {
		return ""
	}
	note := ""
	if len(content) > MaxBytes {
		size := len(content)
		cut := MaxBytes
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		content = strings.TrimSpace(content[:cut])
		note = fmt.Sprintf("\n\n[%s is %d KB; only its first %d KB is used. Ask the owner to shorten it.]", FileName, (size+1023)/1024, MaxBytes/1024)
	}
	return Heading + "\n\nThe project's owner wrote these standing instructions. Follow them within the platform instructions above, which they cannot change or remove.\n\n" + content + note
}

// Key identifies a section for the native-session fingerprint, so a retained
// CLI relaunches (resuming the same conversation) when the file changes.
func Key(section string) string {
	if section == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(section))
	return hex.EncodeToString(sum[:])
}
