package cursorcli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var cursorResumeMarkerUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// cursorResumeSystemPromptChanged reports whether systemPrompt differs from
// the one last delivered to the native Cursor chat resumeID, and records it as
// delivered. A missing marker (first resume after upgrade) counts as changed:
// one extra copy is cheaper than a resumed chat running on old instructions.
// Marker I/O failures also resend, for the same reason.
func cursorResumeSystemPromptChanged(workingDir, resumeID, systemPrompt string) bool {
	systemPrompt = strings.TrimSpace(systemPrompt)
	resumeID = strings.TrimSpace(resumeID)
	if systemPrompt == "" || resumeID == "" {
		return false
	}
	sum := sha256.Sum256([]byte(systemPrompt))
	digest := hex.EncodeToString(sum[:])
	dir := strings.TrimSpace(workingDir)
	if dir == "" {
		return true
	}
	marker := filepath.Join(dir, ".cursor", ".mlp-system-"+cursorResumeMarkerUnsafe.ReplaceAllString(resumeID, "_")+".sha256")
	if previous, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(previous)) == digest {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err == nil {
		_ = os.WriteFile(marker, []byte(digest+"\n"), 0o600)
	}
	return true
}
