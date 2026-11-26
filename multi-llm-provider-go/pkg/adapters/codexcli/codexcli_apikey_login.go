package codexcli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
)

// seedCodexAPIKeyLogin saves an API key as Codex's own login before the terminal UI starts. The UI does
// not use CODEX_API_KEY / OPENAI_API_KEY from the environment: with no saved login it opens its "paste
// your API key" screen and waits there for someone to press Enter, so a user who configured a key never
// got a working session. The file is the one `codex login --with-api-key` writes.
func seedCodexAPIKeyLogin(env []string, accountRoot, hint string) error {
	key, home := "", strings.TrimSpace(accountRoot)
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, "CODEX_API_KEY="); ok && strings.TrimSpace(v) != "" {
			key = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(entry, "CODEX_HOME="); ok && strings.TrimSpace(v) != "" {
			home = strings.TrimSpace(v)
		}
	}
	if home == "" {
		return nil
	}
	path := filepath.Join(home, "auth.json")
	marker := path + ".agentworks"
	// The login may be LINKED in from an account's own home (a shared or a personal connection). It belongs to that
	// account: writing through the link would replace its key with this session's, for everyone using the account
	// (Excellence 2026-10-15). A linked login is never written, replaced or removed here.
	if reason := codexAuthLinkedReason(home, path); reason != "" {
		log.Printf("[CODEX_AUTH] skipped_linked home_hash=%s reason=%s", codexAuthPathHash(home), reason)
		return nil
	}
	if key == "" {
		// No key is configured any more: a key login saved here earlier must not stay valid. Only a login
		// this function wrote (it leaves a marker beside it) is removed; a key the user pasted into
		// Codex themselves, or a browser login, is theirs and is left alone.
		if _, err := os.Stat(marker); err == nil {
			_ = os.Remove(marker)
			return os.Remove(path)
		}
		return nil
	}
	if raw, err := os.ReadFile(path); err == nil {
		var current struct {
			Mode   string          `json:"auth_mode"`
			Key    string          `json:"OPENAI_API_KEY"`
			Tokens json.RawMessage `json:"tokens"`
		}
		if json.Unmarshal(raw, &current) == nil {
			if current.Key == key && current.Mode == "apikey" {
				return nil
			}
			// A sign-in the person made themselves (a ChatGPT login with tokens, not written by this
			// function) is never replaced by a configured key.
			if _, ours := os.Stat(marker); ours != nil && current.Mode != "apikey" && (len(current.Tokens) > 0 || current.Mode != "") {
				return nil
			}
		}
	}
	body, err := json.MarshalIndent(map[string]string{"auth_mode": "apikey", "OPENAI_API_KEY": key}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, slotfs.Mode(hint, 0o700)); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(body, '\n'), slotfs.Mode(hint, 0o600)); err != nil {
		return err
	}
	log.Printf("[CODEX_AUTH] seeded home_hash=%s", codexAuthPathHash(home))
	return os.WriteFile(marker, nil, slotfs.Mode(hint, 0o600))
}

// codexLoginEnv adds the adapter's configured API key to the environment the login is seeded from, when
// that environment carries none. A key that reaches the adapter through the app's provider configuration
// (not an account environment) was otherwise never saved as Codex's login, so the terminal UI opened its
// sign-in screen and the turn waited there for ever.
func codexLoginEnv(env []string, configuredKey string) []string {
	configuredKey = strings.TrimSpace(configuredKey)
	if configuredKey == "" {
		return env
	}
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, "CODEX_API_KEY="); ok && strings.TrimSpace(v) != "" {
			return env
		}
	}
	return append(append([]string(nil), env...), "CODEX_API_KEY="+configuredKey)
}

// codexAuthLinkedReason says why auth.json in home is not this session's own file: it is a symlink, or the .codex
// folder itself is one. "" means it is the session's own (or does not exist yet).
func codexAuthLinkedReason(home, path string) string {
	if info, err := os.Lstat(home); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "home_is_symlink"
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "auth_is_symlink"
	}
	return ""
}

// codexAuthPathHash names a home in the log without printing a path.
func codexAuthPathHash(home string) string {
	sum := sha256.Sum256([]byte(home))
	return fmt.Sprintf("%x", sum[:4])
}
