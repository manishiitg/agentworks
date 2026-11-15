package agentworksclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const nativeDeltaLimit = 4 << 20

// ReadNativeJournalDelta reads only complete appended JSONL records from the
// exact native UUID. The acknowledged byte offset is never silently reset.
func ReadNativeJournalDelta(a AgentAttachment, offset int64, previous string) ([]byte, string, error) {
	id, err := uuid.Parse(a.SessionID)
	if err != nil || id.String() != a.SessionID || offset < 0 {
		return nil, "", fmt.Errorf("invalid native cursor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	base := filepath.Join(home, ".claude", "projects")
	pattern := filepath.Join(base, "*", a.SessionID+".jsonl")
	if a.Provider == "codex" {
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		if !filepath.IsAbs(root) {
			return nil, "", fmt.Errorf("invalid native root")
		}
		base = filepath.Join(root, "sessions")
		pattern = filepath.Join(base, "*", "*", "*", "rollout-*-"+a.SessionID+".jsonl")
	} else if a.Provider != "claude" {
		return nil, "", fmt.Errorf("unsupported native provider")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return nil, "", fmt.Errorf("exact native journal unavailable")
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(matches[0]))
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(realBase, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("native journal escaped root")
	}
	path := filepath.Join(parent, filepath.Base(matches[0]))
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, "", fmt.Errorf("unsafe native journal")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || info.Size() < offset {
		return nil, "", fmt.Errorf("native journal changed")
	}
	hashAt := func(at int64) (string, error) {
		size := min(at, int64(4096))
		data := make([]byte, size)
		if _, e := f.ReadAt(data, at-size); e != nil && !(size == 0 && e == io.EOF) {
			return "", e
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	}
	old, err := hashAt(offset)
	if err != nil || (previous != "" || offset != 0) && previous != old {
		return nil, "", fmt.Errorf("native journal prefix changed")
	}
	if offset > 0 {
		var last [1]byte
		if _, err = f.ReadAt(last[:], offset-1); err != nil || last[0] != '\n' {
			return nil, "", fmt.Errorf("cursor is not a record boundary")
		}
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, nativeDeltaLimit))
	if err != nil {
		return nil, "", err
	}
	end := bytes.LastIndexByte(data, '\n') + 1
	if end == 0 && len(data) == nativeDeltaLimit {
		return nil, "", fmt.Errorf("native record exceeds delta limit")
	}
	data = data[:end]
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) > 0 && !json.Valid(line) {
			return nil, "", fmt.Errorf("invalid native JSONL record")
		}
	}
	// Recheck the acknowledged prefix after reading; appends are fine, rewrites aren't.
	again, err := hashAt(offset)
	if err != nil || again != old {
		return nil, "", fmt.Errorf("native journal prefix changed")
	}
	next, err := hashAt(offset + int64(len(data)))
	return data, next, err
}
