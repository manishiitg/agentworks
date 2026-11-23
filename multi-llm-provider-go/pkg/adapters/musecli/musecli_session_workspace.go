package musecli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// NativeSessionWorkingDir reads the workspace identity from Muse's own session
// metadata. This repairs legacy handles that omitted WorkingDir; callers must
// still compare it against their expected isolated directory before resuming.
// Conversation text and tool outputs are never accepted as identity evidence.
func NativeSessionWorkingDir(sessionID string) string {
	path := museSessionLogPath(sessionID)
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 1024*1024))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if museLogString(record, "payload_type") != "runtime.session.metadata" {
			continue
		}
		dir := museLogString(record, "payload", "record", "workspace_root")
		if !filepath.IsAbs(dir) {
			return ""
		}
		return filepath.Clean(dir)
	}
	return ""
}
