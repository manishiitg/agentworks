package localfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// FolderSelection is configured by the laptop owner's website settings, per Crew.
// The laptop still applies its non-overridable private-path boundary.
type FolderSelection struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Access string `json:"access"`
}
type Directory struct {
	Path    string   `json:"path"`
	Parent  string   `json:"parent"`
	Folders []string `json:"folders"`
}

func OpenSettings(device, state string, private []string) (*Executor, error) {
	e := &Executor{Hello: Hello{Version: Version, DeviceID: device, SettingsManaged: true}, settingsState: state, privatePaths: private}
	return e, e.Hello.Validate()
}

func (e *Executor) privateBoundary() []string { return append([]string{}, e.privatePaths...) }

func canonicalLocal(input string) (string, error) {
	if !filepath.IsAbs(input) || strings.ContainsRune(input, 0) {
		return "", fmt.Errorf("choose an absolute folder path")
	}
	return filepath.EvalSymlinks(filepath.Clean(input))
}

func (e *Executor) allowedPath(input string, grant bool) (string, error) {
	resolved, err := canonicalLocal(input)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	home, _ = canonicalLocal(home)
	if grant && containsPath(resolved, home) {
		return "", fmt.Errorf("choose a folder inside your home, or a project on another drive; the whole home or drive cannot be granted")
	}
	for _, hidden := range e.privateBoundary() {
		p := filepath.Clean(hidden)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if containsPath(p, resolved) || grant && containsPath(resolved, p) {
			return "", fmt.Errorf("this folder contains private login or system files and cannot be shared")
		}
	}
	return resolved, nil
}

func (e *Executor) selectionGrants(r Request) ([]Grant, error) {
	if r.CrewID == "" || len(r.CrewID) > 256 || len(r.Folders) == 0 || len(r.Folders) > 32 {
		return nil, fmt.Errorf("choose a Crew and between 1 and 32 folders in Settings")
	}
	seen := map[string]bool{}
	var grants []Grant
	for _, f := range r.Folders {
		if !ValidID(f.ID) || seen[f.ID] || f.Access != "read" && f.Access != "write" {
			return nil, fmt.Errorf("invalid folder permissions")
		}
		seen[f.ID] = true
		root, err := e.allowedPath(f.Path, true)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("selected folder is unavailable")
		}
		hash := sha256.Sum256([]byte(r.CrewID + "\x00" + f.ID))
		guard := wf.FolderGuard{ReadPaths: []string{"."}}
		if f.Access == "write" {
			guard.WritePaths = []string{"."}
		}
		grants = append(grants, Grant{Resource: Resource{ID: f.ID, Writable: f.Access == "write", Guard: guard}, Root: root, State: filepath.Join(e.settingsState, hex.EncodeToString(hash[:])), PrivatePaths: append(e.privateBoundary(), e.settingsState)})
	}
	// A read-only nested grant must not be widened by a writable parent.
	for i := range grants {
		for _, other := range grants {
			if !other.Writable && grants[i].Writable && containsPath(grants[i].Root, other.Root) {
				rel, _ := filepath.Rel(grants[i].Root, other.Root)
				grants[i].Guard.ReadOnlyPaths = append(grants[i].Guard.ReadOnlyPaths, filepath.ToSlash(rel))
			}
		}
	}
	return grants, nil
}

func (e *Executor) executeSettings(ctx context.Context, r Request) Response {
	fail := func(err error) Response { return Response{ID: r.ID, Status: 403, Error: err.Error()} }
	if r.Operation == "browse_folders" {
		input := r.Path
		if input == "" {
			input, _ = os.UserHomeDir()
		}
		root, err := e.allowedPath(input, false)
		if err != nil {
			return fail(err)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return fail(fmt.Errorf("this folder cannot be opened"))
		}
		folders := []string{}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, err := e.allowedPath(filepath.Join(root, entry.Name()), false); err == nil {
				folders = append(folders, entry.Name())
			}
			if len(folders) == 1000 {
				break
			}
		}
		sort.Strings(folders)
		return Response{ID: r.ID, Status: 200, Directory: &Directory{Path: root, Parent: filepath.Dir(root), Folders: folders}}
	}
	grants, err := e.selectionGrants(r)
	if err != nil {
		return fail(err)
	}
	if r.Operation == "validate_folders" {
		return Response{ID: r.ID, Status: 200, Folders: r.Folders}
	}
	if r.Operation == "shell" {
		for _, grant := range grants {
			if grant.ID == r.ResourceID && !grant.Writable {
				return fail(fmt.Errorf("Read access does not allow commands; choose Read & write in Crew Settings"))
			}
		}
	}
	child, err := Open(e.Hello.DeviceID, grants)
	if err != nil {
		return fail(err)
	}
	defer child.Close()
	return child.Execute(ctx, r)
}
