package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// A Code session's person, pinned once (docs/design/code_private_mcp.md,
// "Who a session belongs to"). The session owner the event store keeps is
// last-writer-wins, and the in-memory Code mark (common.MarkCodeSession) is
// lost on restart while a retained CLI keeps calling the bridge with its old
// session id. Personal MCP servers and secrets must follow neither: they
// follow this write-once record, kept in the server's state root.

type codeSessionPin struct {
	Person    string `json:"person"`
	CodeRoot  string `json:"code_root"`
	CreatedAt string `json:"created_at"`
}

var errCodeSessionPinnedToAnother = errors.New("this Code chat belongs to another person")

// cleanCodeRoot keeps a Code's full physical path (_users/<owner>/...): unlike
// normalizeConversationWorkspace it never drops the owner, so two people's
// Codes can never share a pin or a switch.
func cleanCodeRoot(root string) string {
	return strings.Trim(path.Clean("/"+filepath.ToSlash(strings.TrimSpace(root))), "/")
}

func codeSessionPinPath(sessionID string) (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("code-session\x00" + strings.TrimSpace(sessionID)))
	return filepath.Join(root, "code-session-pins", hex.EncodeToString(sum[:16])+".json"), nil
}

// codeSessionPinFor returns a session's pin; ok is false for a session that
// is not a Code session (it was never pinned).
func codeSessionPinFor(sessionID string) (codeSessionPin, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return codeSessionPin{}, false, nil
	}
	path, err := codeSessionPinPath(sessionID)
	if err != nil {
		return codeSessionPin{}, false, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path derives from a hash in the state root
	if errors.Is(err, os.ErrNotExist) {
		return codeSessionPin{}, false, nil
	}
	if err != nil {
		return codeSessionPin{}, false, err
	}
	var pin codeSessionPin
	if err := json.Unmarshal(data, &pin); err != nil || pin.Person == "" {
		return codeSessionPin{}, false, fmt.Errorf("unreadable Code session record")
	}
	return pin, true, nil
}

// pinCodeSession records the session's person and Code the first time, and
// afterwards only confirms them: a turn from anyone else, or for another
// Code, is refused (errCodeSessionPinnedToAnother).
func pinCodeSession(sessionID, person, codeRoot string) error {
	person = sanitizeUserIDForPath(strings.TrimSpace(person))
	codeRoot = cleanCodeRoot(codeRoot)
	if strings.TrimSpace(sessionID) == "" || person == "" || codeRoot == "" {
		return fmt.Errorf("a Code chat needs a session, a person and a Code")
	}
	path, err := codeSessionPinPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(codeSessionPin{Person: person, CodeRoot: codeRoot, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path derives from a hash in the state root
	if err == nil {
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(path)
			return fmt.Errorf("record Code session: %w", errors.Join(writeErr, closeErr))
		}
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	existing, ok, err := codeSessionPinFor(sessionID)
	if err != nil || !ok {
		return fmt.Errorf("read Code session record: %w", errors.Join(err, errors.New("no record")))
	}
	if existing.Person != person || existing.CodeRoot != codeRoot {
		return errCodeSessionPinnedToAnother
	}
	return nil
}

// inheritCodeSessionPin gives a sub-agent or background session its parent's
// person and Code, never the person of whoever happens to be active.
func inheritCodeSessionPin(parentSessionID, childSessionID string) error {
	parent, ok, err := codeSessionPinFor(parentSessionID)
	if err != nil || !ok {
		return err
	}
	return pinCodeSession(childSessionID, parent.Person, parent.CodeRoot)
}
