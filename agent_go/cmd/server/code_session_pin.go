package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// codeSessionTurnRefusal is why a request may not add a turn to sessionID,
// or "" when it may: a pinned Code chat accepts turns only from its own
// person, and never from a channel-route (bot_route) principal, which acts
// for arbitrary channel members. Every entry that adds a turn to an existing
// session checks it (handleQuery before live-input delivery, /live-input).
func codeSessionTurnRefusal(sessionID string, claims *UserClaims) string {
	pin, pinned, err := codeSessionPinFor(sessionID)
	if err != nil {
		return "This Code chat is unavailable."
	}
	if !pinned {
		return ""
	}
	if claims == nil || claims.Provider == "bot_route" || sanitizeUserIDForPath(strings.TrimSpace(claims.UserID)) != pin.Person {
		return "This Code chat belongs to another person. Open your own chat of this Code."
	}
	return ""
}

// deleteCodeSessionPin removes a deleted session's pin.
func deleteCodeSessionPin(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if path, err := codeSessionPinPath(sessionID); err == nil {
		_ = os.Remove(path)
	}
}

// sweepOrphanCodeSessionPins removes pins whose Code no longer exists (a
// session deleted outside the paths above, or a pin left by a crash). It runs
// once, a few minutes after start, when the workspace service is up.
func (api *StreamingAPI) sweepOrphanCodeSessionPins(ctx context.Context) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	dir := filepath.Join(root, "code-session-pins")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	gone := map[string]bool{}
	removed := 0
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path) //nolint:gosec // G304: entries of the pin directory
		if err != nil {
			continue
		}
		var pin codeSessionPin
		if json.Unmarshal(data, &pin) != nil || pin.CodeRoot == "" {
			_ = os.Remove(path)
			removed++
			continue
		}
		missing, seen := gone[pin.CodeRoot]
		if !seen {
			_, found, err := ReadWorkflowManifest(ctx, pin.CodeRoot)
			missing = err == nil && !found
			gone[pin.CodeRoot] = missing
		}
		if missing {
			_ = os.Remove(path)
			removed++
		}
	}
	if removed > 0 {
		log.Printf("[CODE_SESSION] removed %d pin(s) of deleted Codes", removed)
	}
}
