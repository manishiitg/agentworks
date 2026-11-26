package agycli

import (
	"fmt"
	"os"
	"path/filepath"
)

// agyWriteSettingsAtomic keeps AGY readers from observing a truncated JSON
// document while trust, key-mode, or permission settings are updated.
func agyWriteSettingsAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".agentworks-agy-settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create agy settings replacement: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
