// Package projectfile writes per-session instructions into a project folder
// without destroying what the project already has there.
//
// A CLI reads its instructions from a file in the working directory
// (AGENTS.md, .pi/APPEND_SYSTEM.md, ...). Several sessions can share one
// folder, and the folder can hold the user's own file of the same name. So a
// session never replaces the file: it adds one marked block, sessions are
// counted per path, and the last one to finish removes only the block (and the
// file too when this package created it and nothing else is left in it).
package projectfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	beginPrefix = "<!-- BEGIN agentworks-session-instructions"
	endMarker   = "<!-- END agentworks-session-instructions -->"

	// SkillMarkerFile is written inside every skill folder a session projects,
	// so cleanup removes only folders it created and never a user's own skill.
	SkillMarkerFile = ".agentworks-managed"
)

const leasePrefix = "agentworks-lease:"

var (
	mu      sync.Mutex
	holders = map[string]*held{}
	leases  = map[string]string{} // token -> path
	leaseN  int
)

type held struct {
	count int
	block string
}

// Acquire adds this session's block to path (creating the file when absent)
// and counts the session. Sessions holding the same path share one block: the
// most recent body wins while several are active. Pair every Acquire with one
// Release.
func Acquire(path, body string) error {
	return acquire(path, body, 0)
}

func acquire(path, body string, maxBytes int) error {
	path = filepath.Clean(path)
	mu.Lock()
	defer mu.Unlock()
	h := holders[path]
	if h == nil {
		h = &held{}
		holders[path] = h
	}
	if err := writeBlock(path, body, h.count == 0, maxBytes); err != nil {
		if h.count == 0 {
			delete(holders, path)
		}
		return err
	}
	h.block = body
	h.count++
	return nil
}

// Release ends one session's hold on path. It reports whether path was held.
// The last release removes the block, and the file when it was created here
// and holds nothing else.
func Release(path string) bool {
	path = filepath.Clean(path)
	mu.Lock()
	defer mu.Unlock()
	h := holders[path]
	if h == nil {
		return false
	}
	h.count--
	if h.count > 0 {
		return true
	}
	delete(holders, path)
	_ = stripBlock(path)
	return true
}

// Held reports whether a live session holds path.
func Held(path string) bool {
	mu.Lock()
	defer mu.Unlock()
	return holders[filepath.Clean(path)] != nil
}

// StripStale removes a leftover block from path, for a crashed or restarted
// process. It does nothing while a live session holds the path.
func StripStale(path string) {
	path = filepath.Clean(path)
	mu.Lock()
	defer mu.Unlock()
	if holders[path] != nil {
		return
	}
	_ = stripBlock(path)
}

func writeBlock(path, body string, first bool, maxBytes int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("projectfile: create %s: %w", filepath.Dir(path), err)
	}
	existing, err := os.ReadFile(path)
	created := false
	mode := os.FileMode(0o644)
	switch {
	case err == nil:
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	case os.IsNotExist(err):
		created = true
	default:
		return fmt.Errorf("projectfile: read %s: %w", path, err)
	}
	text := string(existing)
	// A block left by a crashed session tells us whether the file was ours.
	if stripped, wasCreated, had := removeBlock(text); had {
		text = stripped
		created = created || (wasCreated && strings.TrimSpace(stripped) == "")
	} else if !first {
		// Another live session's block was deleted underneath us; re-add it.
		created = strings.TrimSpace(text) == ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(text, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "%s created=%t -->\n%s\n%s\n", beginPrefix, created, strings.TrimRight(body, "\n"), endMarker)
	if maxBytes > 0 && b.Len() > maxBytes {
		return fmt.Errorf("projectfile: %s requires %d bytes, exceeding startup limit %d", path, b.Len(), maxBytes)
	}
	return os.WriteFile(path, []byte(b.String()), mode)
}

func stripBlock(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	stripped, created, had := removeBlock(string(data))
	if !had {
		return nil
	}
	if created && strings.TrimSpace(stripped) == "" {
		return os.Remove(path)
	}
	info, statErr := os.Stat(path)
	mode := os.FileMode(0o644)
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(strings.TrimRight(stripped, "\n")+"\n"), mode)
}

// removeBlock cuts our marked block out of text. created reports whether the
// block says the file did not exist before it.
func removeBlock(text string) (stripped string, created, had bool) {
	start := strings.Index(text, beginPrefix)
	if start < 0 {
		return text, false, false
	}
	endRel := strings.Index(text[start:], endMarker)
	if endRel < 0 {
		return text, false, false
	}
	end := start + endRel + len(endMarker)
	head := text[start:]
	if line, _, ok := strings.Cut(head, "\n"); ok {
		created = strings.Contains(line, "created=true")
	}
	before := strings.TrimRight(text[:start], "\n")
	after := strings.TrimLeft(text[end:], "\n")
	switch {
	case before == "":
		stripped = after
	case after == "":
		stripped = before + "\n"
	default:
		stripped = before + "\n\n" + after
	}
	return stripped, created, true
}

// AcquireLease is Acquire for callers that keep a list of cleanup strings: it
// returns a token to put in that list. ReleaseToken on the same token
// releases the hold exactly once, however many times cleanup runs.
func AcquireLease(path, body string) (string, error) {
	return AcquireLeaseLimit(path, body, 0)
}

// AcquireLeaseLimit rejects a projection whose complete UTF-8 file, including
// operator content and managed markers, exceeds maxBytes. Zero means unlimited.
// Rejection leaves the existing file and live leases untouched.
func AcquireLeaseLimit(path, body string, maxBytes int) (string, error) {
	if err := acquire(path, body, maxBytes); err != nil {
		return "", err
	}
	mu.Lock()
	leaseN++
	token := leasePrefix + strconv.Itoa(leaseN)
	leases[token] = filepath.Clean(path)
	mu.Unlock()
	return token, nil
}

// IsLease reports whether s is a lease token.
func IsLease(s string) bool { return strings.HasPrefix(s, leasePrefix) }

// ReleaseToken releases the hold behind a lease token. A token already
// released, or not a token, does nothing.
func ReleaseToken(token string) {
	mu.Lock()
	path, ok := leases[token]
	delete(leases, token)
	if ok && strings.HasPrefix(path, "owned:") {
		releaseOwned(strings.TrimPrefix(path, "owned:"))
		mu.Unlock()
		return
	}
	mu.Unlock()
	if ok {
		Release(path)
	}
}

type ownedHold struct {
	count    int
	prior    []byte
	hadPrior bool
	mode     os.FileMode
}

var owned = map[string]*ownedHold{}

// AcquireOwnedLease is AcquireLease for a file that is entirely ours (a
// uniquely named rules file, not a shared convention like AGENTS.md). Sessions
// are counted; the first writes content, later ones rewrite it, and the last
// release restores whatever was there before, or removes the file.
func AcquireOwnedLease(path string, content []byte) (string, error) {
	return AcquireOwnedLeaseMode(path, content, 0o600)
}

// AcquireOwnedLeaseMode is AcquireOwnedLease with the file mode of the written
// file (for example an executable hook script).
func AcquireOwnedLeaseMode(path string, content []byte, mode os.FileMode) (string, error) {
	path = filepath.Clean(path)
	mu.Lock()
	defer mu.Unlock()
	h := owned[path]
	if h == nil {
		h = &ownedHold{mode: mode}
		if data, err := os.ReadFile(path); err == nil {
			h.prior, h.hadPrior = data, true
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("projectfile: create %s: %w", filepath.Dir(path), err)
		}
	}
	if err := os.WriteFile(path, content, h.mode); err != nil {
		return "", fmt.Errorf("projectfile: write %s: %w", path, err)
	}
	owned[path] = h
	h.count++
	leaseN++
	token := leasePrefix + "o" + strconv.Itoa(leaseN)
	leases[token] = "owned:" + path
	return token, nil
}

func releaseOwned(path string) {
	h := owned[path]
	if h == nil {
		return
	}
	h.count--
	if h.count > 0 {
		return
	}
	delete(owned, path)
	if h.hadPrior {
		_ = os.WriteFile(path, h.prior, h.mode)
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path)) // only succeeds when now empty
}

// OwnedHeld reports whether a live session holds an owned-file lease on path.
func OwnedHeld(path string) bool {
	mu.Lock()
	defer mu.Unlock()
	return owned[filepath.Clean(path)] != nil
}
