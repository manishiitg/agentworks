package agycli

import (
	"os/exec"
	"path/filepath"
)

// SessionStoreDir is the folder agy keeps its conversations in (one <id>.db
// each) for an account home: the user's home, or a slot's private home. The
// native session transfer reads and writes conversation files there.
func SessionStoreDir(accountHome ...string) (string, error) {
	home, err := agyHome(accountHome...)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversations"), nil
}

// CommandAvailable reports whether this computer can start agy, without
// starting it.
func CommandAvailable() error {
	_, err := exec.LookPath("agy")
	return err
}
