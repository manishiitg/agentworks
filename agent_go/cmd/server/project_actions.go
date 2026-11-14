package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const projectActionsFile = ".agentworks/actions.json"
const projectActionsMaxBytes = 64 << 10

// The saved project owns the command and relative folder. A run request will
// contain only an action ID and the revision shown to the person, never a
// browser-supplied command, executable, shell ID or filesystem path.
type projectAction struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Command      string `json:"command"`
	Folder       string `json:"folder"`
	Shortcut     int    `json:"shortcut,omitempty"`
	StopWhenIdle bool   `json:"stop_when_idle"`
}
type projectActionsConfig struct {
	Version int             `json:"version"`
	Actions []projectAction `json:"actions"`
}

var projectActionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func parseProjectActions(raw []byte) (projectActionsConfig, error) {
	var config projectActionsConfig
	if len(raw) > projectActionsMaxBytes {
		return config, errors.New("Project actions are too large.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Version != 1 || len(config.Actions) > 4 {
		return config, errors.New("Use version 1 with at most four project actions.")
	}
	ids, keys := map[string]bool{}, map[int]bool{}
	for i := range config.Actions {
		action := &config.Actions[i]
		action.Name = strings.TrimSpace(action.Name)
		action.Command = strings.TrimSpace(action.Command)
		if !projectActionID.MatchString(action.ID) || ids[action.ID] || action.Name == "" || len(action.Name) > 128 || strings.ContainsAny(action.Name, "\r\n\x00") || action.Command == "" || len(action.Command) > 16000 || strings.ContainsRune(action.Command, 0) || !utf8.ValidString(action.Command) {
			return config, errors.New("Each action needs a unique simple ID, name and command.")
		}
		if action.Shortcut < 0 || action.Shortcut > 4 || action.Shortcut != 0 && keys[action.Shortcut] {
			return config, errors.New("Choose distinct shortcut numbers from 1 to 4, or 0 for none.")
		}
		if action.Folder == "" {
			action.Folder = "."
		}
		if action.Folder != "." {
			clean, ok := workspaceGitRelFile(action.Folder)
			if !ok || wf.Private(clean) {
				return config, errors.New("An action folder must be an ordinary folder inside this project.")
			}
		}
		ids[action.ID] = true
		if action.Shortcut != 0 {
			keys[action.Shortcut] = true
		}
	}
	return config, nil
}

func projectActionsRevision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Opening through os.Root confines all subsequent fixed-path I/O even if the
// project changes concurrently. Managed metadata may not alias another folder
// or file. No generic file write or arbitrary-path endpoint is added.
func projectActionsOpen(root string) (*os.Root, error) {
	disk, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("The project folder is unavailable.")
	}
	info, err := disk.Lstat(".agentworks")
	if err != nil && !os.IsNotExist(err) || err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		disk.Close()
		return nil, errors.New("The project's .agentworks folder must not be a link.")
	}
	return disk, nil
}
func readProjectActions(root string) (projectActionsConfig, string, error) {
	disk, err := projectActionsOpen(root)
	if err != nil {
		return projectActionsConfig{}, "", err
	}
	defer disk.Close()
	info, err := disk.Lstat(projectActionsFile)
	if os.IsNotExist(err) {
		return projectActionsConfig{Version: 1, Actions: []projectAction{}}, projectActionsRevision(nil), nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > projectActionsMaxBytes {
		return projectActionsConfig{}, "", errors.New("Project actions must be a small regular JSON file.")
	}
	f, err := disk.Open(projectActionsFile)
	if err != nil {
		return projectActionsConfig{}, "", errors.New("Project actions could not be read.")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, projectActionsMaxBytes+1))
	if err != nil {
		return projectActionsConfig{}, "", errors.New("Project actions could not be read.")
	}
	config, err := parseProjectActions(raw)
	return config, projectActionsRevision(raw), err
}
func saveProjectActions(root, expected string, config projectActionsConfig) (string, error) {
	_, revision, err := readProjectActions(root)
	if err != nil {
		return "", err
	}
	if expected == "" || expected != revision {
		return "", errors.New("Project actions changed. Reload before saving.")
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	validated, err := parseProjectActions(raw)
	if err != nil {
		return "", err
	}
	raw, _ = json.MarshalIndent(validated, "", "  ")
	raw = append(raw, '\n')
	disk, err := projectActionsOpen(root)
	if err != nil {
		return "", err
	}
	defer disk.Close()
	if err = disk.Mkdir(".agentworks", 0700); err != nil && !os.IsExist(err) {
		return "", errors.New("The project actions folder could not be created.")
	}
	// A server-generated filename, never the action name or incoming path.
	temp := ".agentworks/actions-" + newSteerMessageID() + ".json"
	f, err := disk.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.New("Project actions could not be saved.")
	}
	defer disk.Remove(temp)
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil || disk.Rename(temp, projectActionsFile) != nil {
		return "", errors.New("Project actions could not be saved.")
	}
	return projectActionsRevision(raw), nil
}

// Same project/owner/slot admission as the existing Terminal. A narrowed
// connection cannot turn access to one folder into a whole-project command.
func (api *StreamingAPI) projectActionsTarget(r *http.Request) (user, project, root, physical string, err error) {
	user, _, root, _, project, status, message := api.codeShellTargetFull(r)
	if status != 0 {
		return "", "", "", "", errors.New(message)
	}
	claims := GetUserFromContext(r.Context())
	roots, bound, e := crewroot.Lookup(workspaceGitDocsRoot(), root)
	if e != nil || !bound {
		return "", "", "", "", errors.New("The project folder is unavailable.")
	}
	if externalFileGuard(r.Context(), false) != nil {
		return "", "", "", "", errors.New("A folder-limited connection cannot run project actions.")
	}
	if token := claims.AccessToken; token != nil && (token.ProjectKind != "" || token.BoundAgentID != "" || token.BoundAgentName != "" || !token.Allows("crews:write") || !token.AllowsCrew(roots.ID)) {
		return "", "", "", "", errors.New("This connection cannot manage project actions.")
	}
	physical, ok := workspaceGitPhysicalPath(r, root)
	if !ok {
		return "", "", "", "", errors.New("The project folder is unavailable.")
	}
	physical, ok = workspaceGitConfined(physical, physical)
	if !ok {
		return "", "", "", "", errors.New("The project folder is unavailable.")
	}
	repos, _ := workspaceGitFindRepos(physical)
	if len(repos) == 0 {
		return "", "", "", "", errors.New("Project actions are available for developer projects with Git repositories.")
	}
	return user, project, root, physical, nil
}
func projectActionFolder(root string, action projectAction) (string, error) {
	target, inside := workspaceGitConfined(root, filepath.Join(root, filepath.FromSlash(action.Folder)))
	if !inside {
		return "", errors.New("The action folder is unavailable inside this project.")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return "", errors.New("The action folder is unavailable inside this project.")
	}
	rel, _ := filepath.Rel(root, target)
	if rel != "." && wf.Private(filepath.ToSlash(rel)) {
		return "", errors.New("An action cannot run from a private runtime folder.")
	}
	return target, nil
}
