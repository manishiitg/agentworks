package security

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:runtime/agentworks
var pythonAutomationRuntime embed.FS

// These modules share the existing immutable, per-command Python helper root.
// A slot gets the same group-readable staging as agentworks_db, not an install
// into its account or a new filesystem grant.
func stagePythonAutomationRuntime(directory string, mode os.FileMode) error {
	directoryMode := os.FileMode(0700)
	if mode&0040 != 0 {
		directoryMode = 0750 | os.ModeSetgid
	}
	return fs.WalkDir(pythonAutomationRuntime, "runtime/agentworks", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel("runtime", path)
		if err != nil {
			return err
		}
		target := filepath.Join(directory, relative)
		if entry.IsDir() {
			return os.Mkdir(target, directoryMode)
		}
		content, err := pythonAutomationRuntime.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, mode)
	})
}
