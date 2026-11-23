package claudecode

import (
	"os"
	"path/filepath"
	"strings"
)

func claudeUserConfigPath(accountHome ...string) (string, error) {
	if len(accountHome) > 0 && accountHome[0] != "" {
		return filepath.Join(accountHome[0], ".claude", ".claude.json"), nil
	}
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); root != "" {
		return filepath.Join(root, ".claude.json"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".claude.json"), err
}
