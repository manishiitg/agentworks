// Package nativereplies arbitrates publication of a native continuation root.
// It never starts work or changes completion, permission or retry decisions.
package nativereplies

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const Foreground = "foreground"
const Observer = "observer"

var ErrNoActiveForeground = errors.New("no active foreground turn owns this native root")

type ClaimState struct {
	Provider         string `json:"provider"`
	Session          string `json:"session"`
	Root             string `json:"root"`
	Owner            string `json:"owner"`
	Committed        bool   `json:"committed"`
	ForegroundTurnID string `json:"foreground_turn_id,omitempty"`
}

var registry = struct {
	sync.Mutex
	dir     string
	owners  map[string]ClaimState
	failure error
}{owners: map[string]ClaimState{}}

var lookupMu sync.RWMutex
var foregroundLookup func(string, string) string

// SetForegroundLookup binds publication to a real, nonterminal SDK turn.
// Diagnostic and history readers have no such turn and may never claim.
func SetForegroundLookup(lookup func(string, string) string) {
	lookupMu.Lock()
	foregroundLookup = lookup
	lookupMu.Unlock()
}
func ActiveForeground(provider, session string) string {
	lookupMu.RLock()
	lookup := foregroundLookup
	lookupMu.RUnlock()
	if lookup == nil {
		return ""
	}
	return lookup(provider, session)
}

// Configure must run before restoring runtimes. Claims survive process restart
// and are persisted before either producer may publish the root's prose.
func Configure(dir string) (err error) {
	registry.Lock()
	defer registry.Unlock()
	defer func() { registry.failure = err }()
	registry.dir = dir
	registry.owners = map[string]ClaimState{}
	if dir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var claim ClaimState
		if json.Unmarshal(data, &claim) != nil || claim.Provider == "" || claim.Session == "" || claim.Root == "" || (claim.Owner != Foreground && claim.Owner != Observer) {
			return fmt.Errorf("invalid native reply claim")
		}
		// Only unfinished foreground ownership expires on a server restart.
		// Durable replies remain owned by their original publisher.
		if claim.Owner == Foreground && !claim.Committed {
			claim.Owner = Observer
			if err = save(claim); err != nil {
				return err
			}
		}
		registry.owners[keyFor(claim.Provider, claim.Session, claim.Root)] = claim
	}
	return nil
}

func keyFor(provider, session, root string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(provider+"\x00"+session+"\x00"+root)))
}
func save(claim ClaimState) error {
	if registry.dir == "" {
		return nil
	}
	if err := os.MkdirAll(registry.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(registry.dir, keyFor(claim.Provider, claim.Session, claim.Root)+".json")
	data, _ := json.Marshal(claim)
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func State(provider, session, root string) ClaimState {
	registry.Lock()
	defer registry.Unlock()
	return registry.owners[keyFor(provider, session, root)]
}

func Claims(provider, session string) []ClaimState {
	registry.Lock()
	defer registry.Unlock()
	var out []ClaimState
	for _, claim := range registry.owners {
		if claim.Provider == provider && claim.Session == session {
			out = append(out, claim)
		}
	}
	return out
}

// A native runtime serializes foreground turns. Its accepted completion
// commits those roots; a failed turn releases its unfinished roots, allowing
// the observer to save already committed native replies without running a model.
func SettleForeground(provider, session, turnID string, includedRoots []string) error {
	registry.Lock()
	defer registry.Unlock()
	for key, claim := range registry.owners {
		if claim.Provider != provider || claim.Session != session || claim.ForegroundTurnID != turnID || claim.Owner != Foreground || claim.Committed {
			continue
		}
		included := false
		for _, root := range includedRoots {
			if root == claim.Root {
				included = true
				break
			}
		}
		if included {
			claim.Committed = true
		} else {
			claim.Owner = Observer
		}
		if err := save(claim); err != nil {
			return err
		}
		registry.owners[key] = claim
	}
	return nil
}

func Claim(provider, session, root, proposed string) (string, error) {
	if provider == "" || session == "" || root == "" || (proposed != Foreground && proposed != Observer) {
		return "", fmt.Errorf("exact native root and publication owner required")
	}
	key := keyFor(provider, session, root)
	registry.Lock()
	defer registry.Unlock()
	if registry.failure != nil {
		return "", registry.failure
	}
	if claim := registry.owners[key]; claim.Owner != "" {
		return claim.Owner, nil
	}
	claim := ClaimState{Provider: provider, Session: session, Root: root, Owner: proposed}
	if proposed == Foreground {
		claim.ForegroundTurnID = ActiveForeground(provider, session)
		if claim.ForegroundTurnID == "" {
			return "", ErrNoActiveForeground
		}
	}
	if err := save(claim); err != nil {
		return "", err
	}
	registry.owners[key] = claim
	return proposed, nil
}

type ClaudeRoot struct {
	Session string
	Root    string
	Helper  bool
}
type ClaudeLineage struct{ parents map[string]ClaudeRoot }

// Observe follows committed parentUuid links. A transcript-only notification
// changes helper state but never creates a conversational turn.
func (c *ClaudeLineage) Observe(raw []byte) ClaudeRoot {
	var row struct {
		Type           string `json:"type"`
		ID             string `json:"uuid"`
		Parent         string `json:"parentUuid"`
		Session        string `json:"sessionId"`
		TurnOrigin     string `json:"turnOrigin"`
		TranscriptOnly bool   `json:"queueTranscriptOnly"`
		Origin         struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	}
	if json.Unmarshal(raw, &row) != nil {
		return ClaudeRoot{}
	}
	if c.parents == nil {
		c.parents = map[string]ClaudeRoot{}
	}
	root := c.parents[row.Parent]
	if row.Type == "user" {
		if row.Origin.Kind == "human" || row.TurnOrigin == "sdk" {
			root = ClaudeRoot{Session: row.Session, Root: row.ID}
		}
		if row.Origin.Kind == "task-notification" && row.TurnOrigin == "task_notification" && !row.TranscriptOnly {
			root = ClaudeRoot{Session: row.Session, Root: row.ID, Helper: true}
		}
	}
	if row.ID != "" {
		c.parents[row.ID] = root
	}
	return root
}

// ForegroundMayPublish preserves normal query aggregation unless the runtime
// observer already owns this exact continuation root.
func ForegroundMayPublish(root ClaudeRoot) bool {
	if !root.Helper || root.Root == "" || root.Session == "" {
		return true
	}
	if ActiveForeground("claude-code", root.Session) == "" {
		return State("claude-code", root.Session, root.Root).Owner != Observer
	}
	owner, err := Claim("claude-code", root.Session, root.Root, Foreground)
	if errors.Is(err, ErrNoActiveForeground) {
		return false
	}
	return err != nil || owner == Foreground
}
