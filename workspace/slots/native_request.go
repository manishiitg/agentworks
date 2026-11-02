package slots

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
)

// NativeSessionRequest is internal owner/session routing. No journal path or
// destination home can be supplied by a peer. Bundle carries a descriptor only.
type NativeSessionRequest struct {
	OwnerID   string          `json:"owner_id"`
	RuntimeID string          `json:"runtime_id"`
	SessionID string          `json:"session_id"`
	Operation string          `json:"operation"`
	Transport string          `json:"transport"`
	Bundle    json.RawMessage `json:"bundle,omitempty"`
}

var nativeRuntimeID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var nativeSessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NativeRuntime checks the root-owned assignment, not a slot chosen by caller.
// It derives a path without opening any session files; the slot helper opens it.
func (r NativeSessionRequest) NativeRuntime(cfg ExecConfig, actualSlot string) (string, error) {
	if !ValidSlot(actualSlot) || !nativeRuntimeID.MatchString(r.RuntimeID) || !nativeSessionID.MatchString(r.SessionID) || (r.Operation != "export" && r.Operation != "import") {
		return "", fmt.Errorf("invalid native session operation")
	}
	if r.Transport != "sdk" && r.Transport != "tmux" && r.Transport != "structured" {
		return "", fmt.Errorf("unsupported slot native program")
	}
	if r.OwnerID == "" || cfg.NativeOwners[actualSlot] != r.OwnerID {
		return "", fmt.Errorf("native session owner does not hold this account")
	}
	root := cfg.SlotStateDir(actualSlot)
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("slot state is unavailable")
	}
	return filepath.Join(root, "cli-runtimes", "v1", r.RuntimeID), nil
}
