package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type guardRoot struct{ spelling, real string }

func (c *Client) logUnresolvedGrant(path string, deny bool, err error) {
	c.guardDiagnosticMu.Lock()
	defer c.guardDiagnosticMu.Unlock()
	if time.Since(c.guardDiagnosticWindow) >= time.Minute {
		c.guardDiagnosticWindow = time.Now()
		c.guardDiagnosticSeen = make(map[string]struct{})
	}
	key := fmt.Sprintf("%t:%s", deny, path)
	if _, seen := c.guardDiagnosticSeen[key]; seen {
		return
	}
	c.guardDiagnosticSeen[key] = struct{}{}
	log.Printf("[FOLDER_GUARD_GRANT] path=%q deny=%v reason=%q", path, deny, err)
}

func configuredGuardRoot() (guardRoot, error) {
	spelling, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return guardRoot{}, err
	}
	real, err := canonicalGuardTarget(spelling)
	return guardRoot{spelling, real}, err
}

// Preserve a prospective leaf for create/write operations, but resolve every
// existing ancestor. A dangling link is an error, not a nonexistent plain file.
func canonicalGuardTarget(target string) (string, error) {
	current := filepath.Clean(target)
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			return filepath.Join(append([]string{real}, suffix...)...), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("path has no accessible ancestor")
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

func canonicalWorkspaceGuardPath(root guardRoot, input string, resolved map[string]string) (string, error) {
	target := input
	if filepath.IsAbs(target) {
		// Explicit external roots keep their existing meaning. Never infer a
		// workspace grant from a similar folder name somewhere else.
		if !crewroot.Within(root.spelling, target) && !crewroot.Within(root.real, target) {
			return target, nil
		}
	} else {
		target = filepath.Join(root.spelling, target)
		if !crewroot.Within(root.spelling, target) {
			return "", fmt.Errorf("path leaves the configured workspace")
		}
	}
	real, err := resolveGuardDescendant(root, target, resolved)
	if err != nil {
		return "", err
	}
	if !crewroot.Within(root.real, real) {
		return "", fmt.Errorf("path links outside the configured workspace")
	}
	return real, nil
}

// Each unique ancestor is examined once per operation. Regular components need
// one Lstat; links use EvalSymlinks. No result survives the file operation.
func resolveGuardDescendant(root guardRoot, target string, resolved map[string]string) (string, error) {
	if real, ok := resolved[target]; ok {
		return real, nil
	}
	parent, err := resolveGuardDescendant(root, filepath.Dir(target), resolved)
	if err != nil {
		return "", err
	}
	real := filepath.Join(parent, filepath.Base(target))
	info, err := os.Lstat(real)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		real, err = filepath.EvalSymlinks(real)
		if err != nil {
			return "", err
		}
	}
	if !crewroot.Within(root.real, real) {
		return "", fmt.Errorf("path links outside the configured workspace")
	}
	resolved[target] = real
	return real, nil
}
