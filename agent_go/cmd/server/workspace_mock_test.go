package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

type mockWorkspaceAPI struct {
	mu       sync.Mutex
	files    map[string]string
	folders  map[string]bool
	chatRoot string
	// onAppend runs when a chat turn append arrives (before it is stored).
	onAppend func()
}

func (m *mockWorkspaceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/skills/workspace/list":
		m.handleWorkspaceSkills(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/documents":
		m.handleListDocuments(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/documents/"):
		m.handleDocument(w, r)
	case r.URL.Path == "/api/folders" && r.Method == http.MethodPost:
		m.handleCreateFolder(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/folders/") && r.Method == http.MethodDelete:
		m.handleDeleteFolder(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockWorkspaceAPI) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FolderPath string `json:"folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	if m.folders == nil {
		m.folders = map[string]bool{}
	}
	m.folders[strings.Trim(strings.TrimSpace(body.FolderPath), "/")] = true
	m.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"success": true,
	})
}

func (m *mockWorkspaceAPI) hasFolder(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.folders[strings.Trim(strings.TrimSpace(path), "/")]
}

func (m *mockWorkspaceAPI) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	folder := r.URL.Query().Get("folder")
	maxDepth := 1
	if raw := r.URL.Query().Get("max_depth"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			maxDepth = parsed
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	root := buildFolderTree(folder, maxDepth, m.files)
	if root == nil {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"data":    virtualtools.WorkspaceFolderListing{*root},
	})
}

func (m *mockWorkspaceAPI) handleDocument(w http.ResponseWriter, r *http.Request) {
	path := decodeWorkspacePath(strings.TrimPrefix(r.URL.Path, "/api/documents/"))

	switch r.Method {
	case http.MethodPost:
		if strings.HasSuffix(path, "/append-history") {
			// Goals builder chats (header + history-log record).
			path = strings.TrimSuffix(path, "/append-history")
			var body struct {
				Messages []json.RawMessage          `json:"messages"`
				Patch    map[string]json.RawMessage `json:"patch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			var record map[string]json.RawMessage
			if err := json.Unmarshal([]byte(m.files[path]), &record); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			var history []json.RawMessage
			_ = json.Unmarshal(record["conversation_history"], &history)
			history = append(history, body.Messages...)
			record["conversation_history"], _ = json.Marshal(history)
			for key, value := range body.Patch {
				record[key] = value
			}
			encoded, _ := json.Marshal(record)
			m.files[path] = string(encoded)
			writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
			return
		}
		if !strings.HasSuffix(path, "/append-turn") || !isChatHeaderPath(strings.TrimSuffix(path, "/append-turn")) {
			http.NotFound(w, r)
			return
		}
		path = strings.TrimSuffix(path, "/append-turn")
		if m.onAppend != nil {
			m.onAppend()
		}
		var body chatTurnAppend
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		dir := m.chatDirLocked(path)
		var stats chatlog.SaveStats
		var err error
		if body.OnlyMissing {
			stats, err = chatlog.AppendMissingTurns(dir, body.Turns, body.Patch)
		} else {
			stats, err = chatlog.AppendTurns(dir, body.Turns, body.Patch, body.Mode)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		header, _ := os.ReadFile(filepath.Join(dir, chatlog.HeaderFile))
		m.files[path] = string(header)
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "data": stats})
	case http.MethodGet:
		m.mu.Lock()
		content, ok := m.files[path]
		if ok && isChatHeaderPath(path) {
			turns, _ := strconv.Atoi(r.URL.Query().Get("turns"))
			raw, err := chatlog.Record(m.chatDirLocked(path), turns, r.URL.Query().Get("lines") == "1")
			if err != nil {
				m.mu.Unlock()
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			content = string(raw)
		}
		m.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if tail := r.URL.Query().Get("tail"); tail != "" {
			n, _ := strconv.Atoi(tail)
			var record map[string]json.RawMessage
			if json.Unmarshal([]byte(content), &record) == nil {
				var history []json.RawMessage
				_ = json.Unmarshal(record["conversation_history"], &history)
				total := len(history)
				if n < len(history) {
					history = history[len(history)-n:]
				}
				record["conversation_history"], _ = json.Marshal(history)
				record["history_total"], _ = json.Marshal(total)
				record["history_tail"] = json.RawMessage("true")
				encoded, _ := json.Marshal(record)
				content = string(encoded)
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"content": content,
			},
		})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if isChatHeaderPath(path) {
			http.Error(w, "chat history is append-only", http.StatusConflict)
			return
		}
		m.mu.Lock()
		m.files[path] = body.Content
		m.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"success": true,
		})
	case http.MethodDelete:
		m.mu.Lock()
		_, ok := m.files[path]
		if ok {
			delete(m.files, path)
			if isChatHeaderPath(path) {
				_ = chatlog.RemoveChat(m.chatDirLocked(path))
			}
		}
		m.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// chatDirLocked is where the mock keeps a chat folder (a real chatlog store
// in a temporary directory). Call with m.mu held.
func (m *mockWorkspaceAPI) chatDirLocked(headerPath string) string {
	if m.chatRoot == "" {
		m.chatRoot, _ = os.MkdirTemp("", "mock-chats-")
	}
	return filepath.Join(m.chatRoot, filepath.FromSlash(strings.TrimSuffix(headerPath, "/"+chatlog.HeaderFile)))
}

// seedChat stores a whole conversation record as a chat folder, as the
// one-off converter would.
func (m *mockWorkspaceAPI) seedChat(headerPath, record string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.files == nil {
		m.files = map[string]string{}
	}
	dir := m.chatDirLocked(headerPath)
	if _, err := chatlog.ConvertRecord(dir, []byte(record), "seed"); err != nil {
		return err
	}
	header, _ := os.ReadFile(filepath.Join(dir, chatlog.HeaderFile))
	m.files[headerPath] = string(header)
	return nil
}

func (m *mockWorkspaceAPI) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	folderPath := decodeWorkspacePath(strings.TrimPrefix(r.URL.Path, "/api/folders/"))
	prefix := folderPath + "/"

	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted bool
	for path := range m.files {
		if strings.HasPrefix(path, prefix) {
			delete(m.files, path)
			deleted = true
		}
	}

	if !deleted {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func decodeWorkspacePath(raw string) string {
	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, "/")
	decoded := make([]string, 0, len(parts))
	for _, part := range parts {
		value, err := url.PathUnescape(part)
		if err != nil {
			decoded = append(decoded, part)
			continue
		}
		decoded = append(decoded, value)
	}
	return strings.Join(decoded, "/")
}

func buildFolderTree(folder string, maxDepth int, files map[string]string) *virtualtools.WorkspaceFolderItem {
	rootNode := &folderNode{children: map[string]*folderNode{}}
	found := false
	prefix := folder + "/"

	for filePath := range files {
		if !strings.HasPrefix(filePath, prefix) {
			continue
		}
		found = true
		remainder := strings.TrimPrefix(filePath, prefix)
		if remainder == "" {
			continue
		}

		segments := strings.Split(remainder, "/")
		current := rootNode
		currentPath := folder
		for i, segment := range segments {
			child := current.children[segment]
			if child == nil {
				child = &folderNode{children: map[string]*folderNode{}}
				current.children[segment] = child
			}
			currentPath = currentPath + "/" + segment
			child.path = currentPath
			child.name = segment
			if i == len(segments)-1 {
				child.isFile = true
			}
			current = child
		}
	}

	if !found {
		return nil
	}

	return &virtualtools.WorkspaceFolderItem{
		FilePath: folder,
		Type:     "folder",
		// The workspace server counts depth by separators below the folder: "<id>" is 0 and "<id>/product.json" is 1,
		// so max_depth=1 includes each child's direct files (workspace/handlers/documents.go).
		Children: convertFolderChildren(rootNode, 0, maxDepth),
	}
}

type folderNode struct {
	name     string
	path     string
	isFile   bool
	children map[string]*folderNode
}

func convertFolderChildren(node *folderNode, depth, maxDepth int) []virtualtools.WorkspaceFolderItem {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)

	items := make([]virtualtools.WorkspaceFolderItem, 0, len(names))
	for _, name := range names {
		child := node.children[name]
		item := virtualtools.WorkspaceFolderItem{
			FilePath: child.path,
			Type:     "folder",
		}
		if child.isFile {
			item.Type = "file"
		} else if depth < maxDepth {
			item.Children = convertFolderChildren(child, depth+1, maxDepth)
		}
		items = append(items, item)
	}
	return items
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *mockWorkspaceAPI) handleWorkspaceSkills(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkspacePath string `json:"workspace_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	documents := []map[string]interface{}{}
	prefix := strings.TrimSuffix(body.WorkspacePath, "/") + "/"
	for path, content := range m.files {
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/SKILL.md") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
		if len(parts) < 3 {
			continue
		}
		documents = append(documents, map[string]interface{}{"folder_name": parts[len(parts)-2], "file_path": path, "document": content})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"documents": documents, "usage": map[string][]string{}})
}
