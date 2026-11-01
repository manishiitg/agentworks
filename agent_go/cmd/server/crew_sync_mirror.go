package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewNativeMirror struct {
	TransferID   string                   `json:"transfer_id"`
	CapturedAt   string                   `json:"captured_at"`
	Conversation *crewHandoffConversation `json:"conversation"`
}

func saveCrewNativeMirror(roots crewroot.Roots, c *crewHandoffConversation) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	mirror := crewNativeMirror{TransferID: "native-" + hex.EncodeToString(hash[:]), CapturedAt: time.Now().UTC().Format(time.RFC3339), Conversation: c}
	return fsutil.WriteJSONAtomic(filepath.Join(roots.Private, "native-mirror.json"), mirror, 0600)
}
func readCrewNativeMirror(roots crewroot.Roots) (crewNativeMirror, error) {
	var out crewNativeMirror
	raw, err := os.ReadFile(filepath.Join(roots.Private, "native-mirror.json"))
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.TransferID == "" || out.Conversation == nil || out.Conversation.Muse == nil {
		return out, fmt.Errorf("native mirror is incomplete")
	}
	return out, validateCrewHandoffConversation(out.Conversation)
}
func (api *StreamingAPI) crewNativeTurnRunning(roots crewroot.Roots) bool {
	api.activeSessionsMux.RLock()
	defer api.activeSessionsMux.RUnlock()
	for _, s := range api.activeSessions {
		if s == nil {
			continue
		}
		at := canonicalCrewWorkspaceRoot(s.WorkspacePath)
		if at != roots.Virtual && !strings.HasPrefix(at, roots.Virtual+"/") {
			continue
		}
		if s.Status == "running" || s.Status == "paused" || s.HasRunningBackgroundAgents {
			return true
		}
	}
	return false
}

// The archive is committed after each native turn. Its fingerprint avoids
// scanning a large, unchanged native journal on every background tick.
var crewMirrorCaptures sync.Map

type crewMirrorCapture struct {
	fingerprint  string
	conversation *crewHandoffConversation
}

func (api *StreamingAPI) captureCrewMirror(ctx context.Context, claims *UserClaims, roots crewroot.Roots, main ProductConversationRecord) (*crewHandoffConversation, error) {
	raw, err := ReadChatHistoryConversation(claims.UserID, main.SessionID, main.WorkspacePath)
	if err != nil {
		return nil, nil
	}
	sum := sha256.Sum256(raw)
	fingerprint := main.SessionID + "/" + hex.EncodeToString(sum[:])
	key := roots.Private
	if old, ok := crewMirrorCaptures.Load(key); ok && old.(crewMirrorCapture).fingerprint == fingerprint {
		return old.(crewMirrorCapture).conversation, nil
	}
	c, err := api.captureCrewHandoffConversation(ctx, claims, roots, main)
	if err != nil {
		return nil, err
	}
	if c.Muse == nil {
		return nil, nil
	}
	crewMirrorCaptures.Store(key, crewMirrorCapture{fingerprint, c})
	return c, nil
}

func (api *StreamingAPI) syncCrewNativeMirror(ctx context.Context, claims *UserClaims, roots crewroot.Roots, b *crewroot.SyncBinding) error {
	if b.Mode == "manual" || b.PendingAction != "" {
		return nil
	}
	connection, err := api.refreshCrewSyncAuthority(ctx, claims, roots, b)
	if err != nil {
		return err
	}
	var c *crewHandoffConversation
	if b.Editing == "local" {
		if api.crewNativeTurnRunning(roots) {
			return nil
		}
		main, e := api.crewHandoffMain(ctx, claims, roots.ID)
		if e != nil {
			return e
		}
		c, err = api.captureCrewMirror(ctx, claims, roots, main)
	} else {
		var remote crewSyncReply
		remote, err = desktopCrewSyncCall(ctx, claims.UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "mirror-get", PairID: b.PairID})
		c = remote.Conversation
	}
	if err != nil {
		return err
	}
	if c == nil || c.Muse == nil {
		return nil
	}
	raw, _ := json.Marshal(c)
	if old, e := readCrewNativeMirror(roots); e == nil {
		previous, _ := json.Marshal(old.Conversation)
		if string(raw) == string(previous) {
			return nil
		}
	}
	if b.Editing == "local" {
		if err = desktopCrewNativeSync(ctx, roots, connection, b, c.Muse, true); err != nil {
			return err
		}
		if _, err = desktopCrewSyncCall(ctx, claims.UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "mirror-put", PairID: b.PairID, Conversation: c}); err != nil {
			return err
		}
	} else {
		if err = desktopCrewNativeSync(ctx, roots, connection, b, c.Muse, false); err != nil {
			return err
		}
	}
	return saveCrewNativeMirror(roots, c)
}

func (api *StreamingAPI) refreshCrewSyncAuthority(ctx context.Context, claims *UserClaims, roots crewroot.Roots, b *crewroot.SyncBinding) (desktopConnection, error) {
	connection, err := desktopConnectionFor(claims.UserID, b.ConnectionID)
	if err != nil {
		return connection, err
	}
	status, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, b.PeerProjectID, crewSyncRequest{Action: "status", PairID: b.PairID})
	if err != nil {
		return connection, err
	}
	if status.Binding == nil || status.Binding.PairID != b.PairID {
		return connection, fmt.Errorf("paired project is unavailable")
	}
	if status.Binding.Editing != b.Editing {
		b.Editing = status.Binding.Editing
		if err = crewroot.WriteSync(roots.ID, *b); err != nil {
			return connection, err
		}
	}
	return connection, nil
}
