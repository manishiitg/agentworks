package cursorcli

import (
	"path/filepath"
	"strings"
)

func cursorChatsRoots(home string) []string {
	var roots []string
	// The given home's own folders come first. A confined or account-scoped Cursor keeps its chats
	// under its own home; the server's XDG folder can hold an older copy of the same chat (one
	// started before the lock), and preferring it read a stale store and streamed nothing
	// in an older process.
	if strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".config", "cursor", "chats"))
	}
	legacy := filepath.Join(home, ".cursor", "chats")
	for _, root := range roots {
		if filepath.Clean(root) == filepath.Clean(legacy) {
			return roots
		}
	}
	return append(roots, legacy)
}

// An issued runtime config is authoritative; never search another account's stores.
func cursorTmuxChatsRoots(home string, configDir ...string) []string {
	if len(configDir) > 0 && configDir[0] != "" {
		return []string{filepath.Join(configDir[0], "chats")}
	}
	return cursorChatsRoots(home)
}
func cursorTmuxConfigArg(accountHome []string) []string {
	if len(accountHome) > 1 {
		return accountHome[1:2]
	}
	return nil
}
