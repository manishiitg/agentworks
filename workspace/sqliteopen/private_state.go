package sqliteopen

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

func PathHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// CanonicalPath also resolves ancestors of a missing path. Otherwise a new
// private-state folder could evade the docs boundary through a parent symlink.
func CanonicalPath(value string) (string, error) {
	full, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	parent := full
	suffix := []string{}
	for {
		real, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if info, e := os.Lstat(parent); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = next
	}
}

// StateDirectory selects one service-owned SQLite area, physically outside
// workspace documents. It performs no writes and creates no directory.
func StateDirectory(docs, area string) (string, error) {
	if area == "" || area == "." || area == ".." || filepath.Base(area) != area || strings.ContainsAny(area, "/\\") {
		return "", errors.New("invalid private database area")
	}
	docs, err := CanonicalPath(docs)
	if err != nil {
		return "", err
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	state, err = CanonicalPath(state)
	if err != nil {
		return "", err
	}
	if crewroot.Within(docs, state) {
		return "", errors.New("the app's private data folder must be outside the workspace documents")
	}
	return filepath.Join(state, area, PathHash(docs)), nil
}
