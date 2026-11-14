package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

var workspaceEditorLocalAvailable = func() bool { return IsLocalMode() && runtime.GOOS == "darwin" }

type workspaceEditorChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var workspaceEditors = []workspaceEditorChoice{{"vscode", "VS Code"}, {"cursor", "Cursor"}, {"zed", "Zed"}, {"jetbrains", "JetBrains (IntelliJ IDEA)"}}

type workspaceEditorRequest struct {
	Workspace string `json:"workspace_path"`
	Op        string `json:"op"`
	Editor    string `json:"editor,omitempty"`
	Repo      string `json:"repo,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Remember  bool   `json:"remember,omitempty"`
}

func knownWorkspaceEditor(id string) bool {
	for _, editor := range workspaceEditors {
		if editor.ID == id {
			return true
		}
	}
	return false
}

// The server resolves a local project path, not a browser-supplied absolute
// filename or arbitrary protocol. A remote filesystem is never a Mac path.
func (api *StreamingAPI) handleWorkspaceEditor(w http.ResponseWriter, r *http.Request) {
	var req workspaceEditorRequest
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		req = workspaceEditorRequest{Workspace: q.Get("workspace_path"), Op: "capabilities", Repo: q.Get("repo"), File: q.Get("file")}
	} else {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil {
			externalError(w, 400, "invalid_arguments", "Choose a project file to open.")
			return
		}
	}
	api.workspaceEditor(w, r, req)
}
func (api *StreamingAPI) workspaceEditor(w http.ResponseWriter, r *http.Request, req workspaceEditorRequest) {
	w.Header().Set("Cache-Control", "private, no-store")
	requestID := newSteerMessageID()
	digest := sha256.Sum256([]byte(req.Workspace))
	project := hex.EncodeToString(digest[:6])
	reason := "invalid_arguments"
	ok := false
	defer func() {
		log.Printf("[OPEN_EDITOR] request=%s user=%s project=%s editor=%s line=%d ok=%t reason=%s", requestID, GetUserIDFromContext(r.Context()), project, editorLogID(req.Editor), req.Line, ok, reason)
	}()
	refuse := func(status int, code, message string) { reason = code; externalError(w, status, code, message) }
	if !workspaceEditorLocalAvailable() {
		refuse(409, "local_project_required", "Open this project's local folder in the Mac app. Server paths cannot be opened in a local editor.")
		return
	}
	if req.Op != "capabilities" && req.Op != "resolve" {
		refuse(400, "invalid_arguments", "Choose an editor action.")
		return
	}
	if req.Line < 0 || req.Line > 10000000 || (req.Editor != "" && !knownWorkspaceEditor(req.Editor)) {
		refuse(400, "invalid_arguments", "Choose one of the offered editors and a valid line.")
		return
	}
	canonical, valid := workspaceGitCanonicalPath(r, req.Workspace)
	if !valid || !workspaceGitWriteAllowed(r.Context(), r, GetUserFromContext(r.Context()), canonical) {
		refuse(403, "forbidden", "Only this project's Builder can open its local files in an editor.")
		return
	}
	// A folder-limited agent cannot turn its narrow file grant into an editor
	// window with the owner's whole project. The ordinary UI has no such guard.
	if guard := externalFileGuard(r.Context(), false); guard != nil {
		refuse(403, "forbidden", "This connection cannot open a local editor. Use the project in the Mac app.")
		return
	}
	base, valid := workspaceGitPhysicalPath(r, req.Workspace)
	if !valid {
		refuse(404, "not_found", "Project folder not found.")
		return
	}
	base, valid = workspaceGitConfined(base, base)
	if !valid {
		refuse(404, "not_found", "Project folder not found.")
		return
	}
	var target string
	if req.File != "" {
		repo, status, message := workspaceGitResolveRepo(base, req.Repo)
		if status != 0 {
			refuse(status, "not_found", message)
			return
		}
		rel, valid := workspaceGitRelFile(req.File)
		if !valid || !workspaceEditorFileAllowed(rel) {
			refuse(400, "invalid_path", "Choose a file inside this project.")
			return
		}
		target, valid = workspaceGitConfined(base, filepath.Join(repo, filepath.FromSlash(rel)))
		if !valid || !workspaceEditorFileAllowed(filepath.ToSlash(strings.TrimPrefix(target, base+string(filepath.Separator)))) {
			refuse(404, "not_found", "File not found inside this project.")
			return
		}
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() {
			refuse(404, "not_found", "This current file is no longer available.")
			return
		}
	} else {
		if req.Line != 0 || req.Repo != "" {
			refuse(400, "invalid_arguments", "A line needs a current project file.")
			return
		}
		repos, _ := workspaceGitFindRepos(base)
		if len(repos) == 0 {
			refuse(409, "developer_project_required", "Open in editor is available for projects with Git repositories.")
			return
		}
		target = base
	}
	if strings.IndexFunc(target, unicode.IsControl) >= 0 {
		refuse(400, "unsupported_path", "This folder or filename contains unsupported characters.")
		return
	}
	// Preference belongs to this signed-in person, never the project owner of
	// an arbitrary shared project, and uses the existing private workspace store.
	user := publicWorkspaceUserID(r)
	preferencePath := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "editor.json"))
	var saved struct {
		Editor string `json:"editor"`
	}
	raw, exists, err := readFileFromWorkspace(r.Context(), preferencePath)
	if err != nil || exists && json.Unmarshal([]byte(raw), &saved) != nil {
		refuse(503, "preferences_unavailable", "Could not read your editor choice. Try again.")
		return
	}
	if req.Editor == "" && knownWorkspaceEditor(saved.Editor) {
		req.Editor = saved.Editor
	}
	if req.Op == "capabilities" || req.Editor == "" {
		ok = true
		reason = "choose_editor"
		externalJSON(w, map[string]any{"available": true, "preferred_editor": req.Editor, "editors": workspaceEditors})
		return
	}
	if req.Editor != "jetbrains" && strings.Contains(target, ":") {
		refuse(400, "unsupported_path", "This filename contains a colon, which this editor's file links cannot open reliably.")
		return
	}
	if req.Remember {
		saved.Editor = req.Editor
		data, _ := json.Marshal(saved)
		if err := writeRawFileToWorkspace(r.Context(), preferencePath, string(data)); err != nil {
			refuse(503, "preferences_unavailable", "Could not save your editor choice. Try again.")
			return
		}
	}
	launch := map[string]any{"url": workspaceEditorURL(req.Editor, target, req.Line)}
	if req.Editor == "jetbrains" {
		// IDEA's navigation URL selects an already-known project by NAME,
		// not a new local folder. Use its documented fixed native launcher.
		launch = map[string]any{"jetbrains": map[string]any{"path": target, "line": req.Line}}
	}
	ok = true
	reason = "resolved"
	externalJSON(w, map[string]any{"available": true, "request_id": requestID, "editor": req.Editor, "launch": launch, "line": req.Line, "next": "Use this editor target in the Mac app. Resolving it has not opened an app or run a command."})
}
func workspaceEditorURL(editor, target string, line int) string {
	u := url.URL{Scheme: editor, Host: "file", Path: filepath.ToSlash(target)}
	path := strings.ReplaceAll(u.EscapedPath(), ":", "%3A")
	uri := editor + "://file" + path
	if line > 0 {
		uri += ":" + strconv.Itoa(line)
	}
	return uri
}
func (api *StreamingAPI) externalWorkspaceEditor(w http.ResponseWriter, r *http.Request, args map[string]any) {
	project, _, ok := api.readExternalWorkspace(r.Context(), GetUserFromContext(r.Context()), externalArg(args, "workspace_id"))
	if !ok {
		externalError(w, 404, "not_found", "Choose your own Crew before opening its editor.")
		return
	}
	line := 0
	if value, exists := args["line"]; exists {
		raw, _ := json.Marshal(value)
		if json.Unmarshal(raw, &line) != nil {
			externalError(w, 400, "invalid_arguments", "Use a positive line number.")
			return
		}
	}
	remember, _ := args["remember"].(bool)
	req := workspaceEditorRequest{Workspace: project.Binding.WorkspacePath, Op: "resolve", Editor: externalArg(args, "editor"), Repo: externalArg(args, "repo"), File: externalArg(args, "file"), Line: line, Remember: remember}
	api.workspaceEditor(w, r, req)
}

func workspaceEditorFileAllowed(rel string) bool {
	if strings.IndexFunc(rel, unicode.IsControl) >= 0 || crewCallerPrivatePath(rel) {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" || part == ".sandbox-cache" {
			return false
		}
	}
	return true
}

func editorLogID(id string) string {
	if knownWorkspaceEditor(id) {
		return id
	}
	return "unselected"
}
