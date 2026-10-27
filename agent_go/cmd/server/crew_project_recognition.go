package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type localProjectFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type localProjectRecognition struct {
	Folder            string                      `json:"folder"`
	Instructions      []localProjectFile          `json:"instructions"`
	Skills            []localProjectFile          `json:"skills"`
	Partial           bool                        `json:"partial"`
	Sessions          []localProjectNativeSession `json:"sessions"`
	SessionsAvailable bool                        `json:"sessions_available"`
}

// Names only, without evaluating project configuration or following links.
// Recognition describes files found, never claims the current model loaded them.
func recognizeLocalProject(ctx context.Context, root string) localProjectRecognition {
	out := localProjectRecognition{Folder: filepath.Base(root), Instructions: []localProjectFile{}, Skills: []localProjectFile{}, Sessions: []localProjectNativeSession{}}
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			out.Partial = true
			return fs.SkipAll
		}
		if err != nil {
			out.Partial = true
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		visited++
		if visited > 20000 || len(out.Instructions)+len(out.Skills) >= 200 {
			out.Partial = true
			return fs.SkipAll
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", ".venv", "venv", "__pycache__", ".next", ".nuxt", "dist", "build", "target", ".cache", ".tmp", ".sandbox-cache":
				return fs.SkipDir
			}
			if len(parts) > 6 {
				out.Partial = true
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		item := localProjectFile{Path: filepath.ToSlash(rel), Name: entry.Name()}
		if name == "agents.md" || name == "claude.md" || name == "gemini.md" || name == "project_instructions.md" || filepath.ToSlash(rel) == ".github/copilot-instructions.md" {
			out.Instructions = append(out.Instructions, item)
		}
		if name == "skill.md" {
			for _, part := range parts[:len(parts)-1] {
				if part == "skills" || part == ".skills" {
					item.Name = filepath.Base(filepath.Dir(path))
					out.Skills = append(out.Skills, item)
					break
				}
			}
		}
		return nil
	})
	return out
}

type localNativeSource struct {
	Provider, ConnectionID, Label, Root string
}

type localProjectNativeSession struct {
	ID                    string            `json:"id"`
	Provider              string            `json:"provider"`
	Account               string            `json:"account"`
	Title                 string            `json:"title"`
	UpdatedAt             string            `json:"updated_at"`
	NativeID, Path, Model string            `json:"-"`
	Source                localNativeSource `json:"-"`
}

func localNativeSessionKey(source localNativeSource, native string) string {
	h := sha256.Sum256([]byte(source.Provider + "\x00" + source.ConnectionID + "\x00" + native))
	return hex.EncodeToString(h[:16])
}

// Admission to USE a shared account does not grant its owner's private chats.
func (api *StreamingAPI) localNativeSources(ctx context.Context, user, workspace string) ([]localNativeSource, error) {
	if !cliPersonalMac() || !IsLocalMode() {
		return nil, nil
	}
	scope := providerAccountScope{Principal: user, WorkspacePath: workspace, Product: "work"}
	sources := []localNativeSource{}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, provider := range []string{"claude-code", "codex-cli"} {
		if _, err := api.admitProviderAccount(ctx, scope, provider, "global:"+provider); err != nil {
			continue
		}
		root := filepath.Join(home, ".claude", "projects")
		if provider == "codex-cli" {
			root, err = codexSessionsRoot()
			if err != nil {
				return nil, err
			}
		}
		if provider == "claude-code" && os.Getenv("CLAUDE_CONFIG_DIR") != "" {
			root = filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects")
		}
		sources = append(sources, localNativeSource{provider, "global:" + provider, "Your local login", root})
	}
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.OwnerUserID != user || record.Provider != "claude-code" && record.Provider != "codex-cli" {
			continue
		}
		if _, err := api.admitProviderAccount(ctx, scope, record.Provider, record.ID); err != nil {
			continue
		}
		home, err := providerConnectionHome(record.ID)
		if err != nil {
			return nil, err
		}
		root := filepath.Join(home, ".claude", "projects")
		if record.Provider == "codex-cli" {
			root = filepath.Join(home, ".codex", "sessions")
		}
		sources = append(sources, localNativeSource{record.Provider, record.ID, record.DisplayName, root})
	}
	return sources, nil
}

// Read only a bounded launch prefix to match the authoritative cwd and native
// ID before reading any conversation text. A directory slug is not identity.
func localNativeSessionAt(path, project string, source localNativeSource) (localProjectNativeSession, bool) {
	file, err := os.Open(path)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	defer file.Close()
	scan := bufio.NewScanner(io.LimitReader(file, 512*1024))
	scan.Buffer(make([]byte, 4096), 512*1024)
	var id, cwd, title, model string
	for n := 0; n < 24 && scan.Scan(); n++ {
		var row struct {
			Type        string `json:"type"`
			SessionID   string `json:"sessionId"`
			Cwd         string `json:"cwd"`
			IsSidechain bool   `json:"isSidechain"`
			Payload     struct {
				ID      string `json:"id"`
				Cwd     string `json:"cwd"`
				Model   string `json:"model"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"payload"`
			Message struct {
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scan.Bytes(), &row) != nil {
			continue
		}
		if row.IsSidechain {
			return localProjectNativeSession{}, false
		}
		if source.Provider == "codex-cli" && row.Type == "session_meta" {
			id, cwd = row.Payload.ID, row.Payload.Cwd
		}
		if source.Provider == "claude-code" && row.SessionID != "" && row.Cwd != "" {
			id, cwd = row.SessionID, row.Cwd
		}
		if model == "" {
			model = firstNonEmptyTrimmed(row.Message.Model, row.Payload.Model)
		}
		if id != "" && cwd != "" {
			canonical, err := filepath.EvalSymlinks(cwd)
			if err != nil || canonical != project {
				return localProjectNativeSession{}, false
			}
			// Only after the folder matches may a title be extracted.
			if source.Provider == "codex-cli" && row.Type == "event_msg" && row.Payload.Type == "user_message" && title == "" {
				title = row.Payload.Message
			}
			if source.Provider == "claude-code" && row.Type == "user" && title == "" {
				title = extractClaudeTranscriptText(row.Message.Content)
			}
		}
	}
	if id == "" || cwd == "" || strings.ContainsAny(id, "/\\") {
		return localProjectNativeSession{}, false
	}
	if source.Provider == "claude-code" && filepath.Base(path) != id+".jsonl" {
		return localProjectNativeSession{}, false
	}
	if source.Provider == "codex-cli" && !strings.HasSuffix(filepath.Base(path), "-"+id+".jsonl") {
		return localProjectNativeSession{}, false
	}
	info, err := file.Stat()
	if err != nil {
		return localProjectNativeSession{}, false
	}
	label := "Claude"
	if source.Provider == "codex-cli" {
		label = "Codex"
	}
	if title == "" || strings.HasPrefix(title, "<") {
		title = label + " conversation"
	}
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "…"
	}
	return localProjectNativeSession{ID: localNativeSessionKey(source, id), Provider: source.Provider, Account: source.Label, Title: title, UpdatedAt: info.ModTime().UTC().Format(time.RFC3339), NativeID: id, Path: path, Model: model, Source: source}, true
}

func listLocalNativeSessions(ctx context.Context, project string, sources []localNativeSource) ([]localProjectNativeSession, bool) {
	out := []localProjectNativeSession{}
	partial, visited := false, 0
	for _, source := range sources {
		_ = filepath.WalkDir(source.Root, func(path string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				partial = true
				return fs.SkipAll
			}
			if err != nil {
				if !os.IsNotExist(err) {
					partial = true
				}
				return nil
			}
			visited++
			if visited > 20000 {
				partial = true
				return fs.SkipAll
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				rel, _ := filepath.Rel(source.Root, path)
				if len(strings.Split(rel, string(filepath.Separator))) > 3 {
					return fs.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			if source.Provider == "claude-code" && filepath.Dir(filepath.Dir(path)) != source.Root {
				return nil
			}
			if source.Provider == "codex-cli" && !strings.HasPrefix(entry.Name(), "rollout-") {
				return nil
			}
			if session, ok := localNativeSessionAt(path, project, source); ok {
				out = append(out, session)
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if len(out) > 30 {
		out = out[:30]
		partial = true
	}
	return out, partial
}

// Same owner/root/token checks for the website, MCP and Crew chat.
func (api *StreamingAPI) localProjectAccess(w http.ResponseWriter, r *http.Request, id string, write bool) (crewProjectBinding, crewroot.Roots, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || !IsLocalMode() || IsMultiUserMode() {
		externalError(w, 404, "not_found", "Local project recognition is available on your own computer.")
		return crewProjectBinding{}, crewroot.Roots{}, false
	}
	if token := claims.AccessToken; token != nil {
		scope := "crews:read"
		if write {
			scope = "crews:write"
		}
		if !token.Allows(scope) || !token.AllowsCrew(id) || token.FileGuard != nil {
			externalError(w, 403, "insufficient_scope", "Use a connection with full access to this own Crew.")
			return crewProjectBinding{}, crewroot.Roots{}, false
		}
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok || crew.OwnerID != claims.UserID {
		externalError(w, 404, "not_found", "Choose a Crew you own on this computer.")
		return crewProjectBinding{}, crewroot.Roots{}, false
	}
	roots, bound, err := localCrewRoots(crew.Binding.WorkspacePath)
	if err != nil || !bound {
		externalError(w, 404, "not_found", "This Crew does not use an existing local folder.")
		return crewProjectBinding{}, crewroot.Roots{}, false
	}
	if write {
		if status, msg := externalCrewAuthorGate(claims); status != 0 {
			externalError(w, status, "forbidden", msg)
			return crewProjectBinding{}, crewroot.Roots{}, false
		}
	}
	return crew, roots, true
}

func (api *StreamingAPI) handleLocalProject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodPost {
		api.resumeLocalProjectSession(w, r)
		return
	}
	id := r.URL.Query().Get("project_id")
	crew, roots, ok := api.localProjectAccess(w, r, id, false)
	if !ok {
		return
	}
	out := recognizeLocalProject(r.Context(), roots.Files)
	out.SessionsAvailable = cliPersonalMac()
	if r.URL.Query().Get("sessions") == "true" {
		sources, err := api.localNativeSources(r.Context(), roots.Owner, crew.Binding.WorkspacePath)
		if err != nil {
			externalError(w, 503, "sessions_unavailable", "Your local conversations could not be checked. Try again.")
			return
		}
		out.SessionsAvailable = cliPersonalMac()
		var partial bool
		out.Sessions, partial = listLocalNativeSessions(r.Context(), roots.Files, sources)
		out.Partial = out.Partial || partial
	}
	log.Printf("[LOCAL_PROJECT] project=%s action=recognize instructions=%d skills=%d sessions=%d partial=%t", roots.ID, len(out.Instructions), len(out.Skills), len(out.Sessions), out.Partial)
	writeAgentProfileJSON(w, 200, out)
}
