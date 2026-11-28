package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Continue online (owner, 28 Nov, #4520, approved mockup "continue-online"): a chat in a Mac project moves to an
// agent in the Team's online project, so the work keeps going with the Mac closed. Exactly one destination: the
// Team's first online project the person may make agents in, else a new one made for the Team. The Mac collects
// what the chat needs (its conversation, changed repositories pushed as a branch, files it worked on, the
// project's instructions, memory and skills), the server opens the agent as the person with the conversation as
// its history and starts its first turn with the Mac agent's handoff note. The Mac chat stays usable.

const (
	continueOnlineFileLimit     = 10 << 20
	continueOnlineTotalLimit    = 50 << 20
	continueOnlineMaxFiles      = 100
	continueOnlineHistoryLimit  = 40 << 20
	continueOnlineReplyWait     = 75 * time.Second
	continueOnlineRequestLimit  = 64 << 20
	continueOnlineReplyMaxChars = 6000
)

type continueOnlineFile struct {
	Source string `json:"-"`
	Path   string `json:"path"`
	To     string `json:"to"`
	Size   int64  `json:"size"`
}

type continueOnlineRepo struct {
	Root   string `json:"-"`
	Name   string `json:"name"`
	Origin string `json:"origin,omitempty"`
	Branch string `json:"branch"`
	// Changed: the chat's own files with changes not yet committed; Unpushed: commits not on any remote yet.
	Changed  int `json:"changed"`
	Unpushed int `json:"unpushed"`
	files    []string
}

// continueOnlinePlan is what the dialog shows and what the move copies; the Mac decides it, the browser only shows it.
type continueOnlinePlan struct {
	Server      string               `json:"server"`
	ProjectID   string               `json:"project_id,omitempty"`
	ProjectName string               `json:"project_name"`
	NewProject  bool                 `json:"new_project"`
	Chat        string               `json:"chat"`
	Messages    int                  `json:"messages"`
	Repos       []continueOnlineRepo `json:"repos"`
	Files       []continueOnlineFile `json:"files"`
	Setup       []continueOnlineFile `json:"setup"`
	Skipped     []string             `json:"skipped"`
	Problem     string               `json:"problem,omitempty"`
	Ready       bool                 `json:"ready"`

	history    []llmtypes.MessageContent
	conn       desktopConnection
	team       string
	macProject string
	macName    string
}

// handleContinueOnlinePlan is GET /crew-teams/{project}/continue-online?key=<chat key> on the Mac: the readiness check.
func (api *StreamingAPI) handleContinueOnlinePlan(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !IsLocalMode() || claims == nil || ownerOfCrewProjectID(project) != claims.UserID {
		externalError(w, 403, "continue_online_unavailable", "Continue online works from your own project on your Mac.")
		return
	}
	plan, err := api.collectContinueOnline(r.Context(), claims.UserID, project, r.URL.Query().Get("key"))
	if err != nil {
		externalError(w, 409, "continue_online_unavailable", err.Error())
		return
	}
	externalJSON(w, plan)
}

// collectContinueOnline reads the chat and decides where it goes and what is copied. A missing Team or sign-in is a
// Problem the dialog shows, not an error.
func (api *StreamingAPI) collectContinueOnline(ctx context.Context, userID, macProject, chatKey string) (*continueOnlinePlan, error) {
	chatKey = strings.TrimSpace(chatKey)
	if chatKey == "" {
		chatKey = macProject
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(userID))
	if err != nil {
		return nil, err
	}
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, chatKey)]
	if !ok || record.ConversationKey != chatKey || crewResourceProjectID(record.ConversationKey) != macProject {
		return nil, fmt.Errorf("this chat has nothing saved yet; send it a message first")
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || roots.Owner != userID {
		return nil, fmt.Errorf("only your own chat in your own project can continue online")
	}
	plan := &continueOnlinePlan{Chat: firstNonEmptyTrimmed(record.Title, "Main"), macProject: macProject, Repos: []continueOnlineRepo{}, Files: []continueOnlineFile{}, Setup: []continueOnlineFile{}, Skipped: []string{}}
	plan.macName = continueOnlineProjectName(ctx, record.WorkspacePath, roots)
	data, err := ReadChatHistoryConversation(userID, record.SessionID, record.WorkspacePath)
	if err == nil {
		var saved struct {
			UserID  string                    `json:"user_id"`
			History []llmtypes.MessageContent `json:"conversation_history"`
		}
		if json.Unmarshal(data, &saved) == nil && saved.UserID == userID {
			plan.history = saved.History
		}
	}
	for _, message := range plan.history {
		if role := string(message.Role); role == "human" || role == "user" || role == "ai" || role == "assistant" {
			for _, part := range message.Parts {
				if text, ok := part.(llmtypes.TextContent); ok && strings.TrimSpace(text.Text) != "" {
					plan.Messages++ // the person's and the agent's messages, not tool steps
					break
				}
			}
		}
	}
	if len(plan.history) == 0 {
		plan.Problem = "This chat has no saved conversation yet. Send it a message first."
	}
	api.continueOnlineDestination(ctx, plan)
	continueOnlineCollectFiles(plan, roots)
	plan.Ready = plan.Problem == ""
	return plan, nil
}

func continueOnlineProjectName(ctx context.Context, workspace string, roots crewroot.Roots) string {
	raw, exists, err := readFileFromWorkspace(ctx, strings.TrimSuffix(workspace, "/")+"/product.json")
	var manifest productProjectManifest
	if err == nil && exists && json.Unmarshal([]byte(raw), &manifest) == nil {
		if name := firstNonEmptyTrimmed(manifest.Title, manifest.Label, manifest.Identity.Name); name != "" {
			return name
		}
	}
	base := filepath.Base(roots.Files)
	if i := strings.LastIndex(base, "-"); i > 0 && len(base)-i == 9 {
		base = base[:i] // a project folder ends in -<first 8 of its id>
	}
	return base
}

// continueOnlineDestination picks the one place the chat goes: the Team's online project, else a new one.
func (api *StreamingAPI) continueOnlineDestination(ctx context.Context, plan *continueOnlinePlan) {
	local, binding, conn, err := api.desktopCrewChatBinding(ctx, plan.macProject)
	if err != nil {
		plan.Problem = firstNonEmptyTrimmed(plan.Problem, "Turn on this project's Team and sign in to your company's server first (Team panel), so the chat has an online project to go to.")
		return
	}
	plan.conn, plan.team, plan.Server = conn, binding.PeerProjectID, conn.Name
	var view struct {
		OnlineProjects []struct {
			ProjectID     string `json:"project_id"`
			Name          string `json:"name"`
			CanMakeAgents bool   `json:"can_make_agents"`
		} `json:"online_projects"`
	}
	if err := crewTeamRemoteJSON(ctx, local.UserID, conn, http.MethodGet, "/api/crew-teams/"+url.PathEscape(binding.PeerProjectID)+"/tab", nil, &view); err != nil {
		plan.Problem = firstNonEmptyTrimmed(plan.Problem, conn.Name+" could not be reached: "+laptopActionErrorText(err.Error()))
		return
	}
	for _, p := range view.OnlineProjects {
		if p.CanMakeAgents && p.ProjectID != "" {
			plan.ProjectID, plan.ProjectName = p.ProjectID, firstNonEmptyTrimmed(p.Name, "online project")
			return
		}
	}
	plan.NewProject, plan.ProjectName = true, strings.TrimSpace(plan.macName+" online")
}

var continueOnlinePatchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add) File: (.+)$`)

// continueOnlineCollectFiles finds the files the chat changed (from its write and edit tool calls), then splits them:
// files tracked in a git repository travel on a pushed branch, the rest are copied under from-mac/.
func continueOnlineCollectFiles(plan *continueOnlinePlan, roots crewroot.Roots) {
	paths, seen := []string{}, map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(roots.Files, raw)
		}
		raw = filepath.Clean(raw)
		if !seen[raw] {
			seen[raw] = true
			paths = append(paths, raw)
		}
	}
	for _, message := range plan.history {
		for _, part := range message.Parts {
			var call *llmtypes.ToolCall
			switch c := part.(type) {
			case llmtypes.ToolCall:
				call = &c
			case *llmtypes.ToolCall:
				call = c
			}
			if call == nil || call.FunctionCall == nil || !continueOnlineWriteTool(call.FunctionCall.Name) {
				continue
			}
			var args map[string]any
			if json.Unmarshal([]byte(call.FunctionCall.Arguments), &args) != nil {
				continue
			}
			for _, key := range []string{"file_path", "path", "notebook_path"} {
				if value, ok := args[key].(string); ok {
					add(value)
				}
			}
			for _, key := range []string{"patch", "input", "command"} {
				if value, ok := args[key].(string); ok {
					for _, m := range continueOnlinePatchFile.FindAllStringSubmatch(value, -1) {
						add(m[1])
					}
				}
			}
		}
	}
	repos, toplevel := map[string]*continueOnlineRepo{}, map[string]string{}
	used, total := map[string]bool{}, int64(0)
	copyFile := func(path, to string, size int64, list *[]continueOnlineFile) {
		if len(plan.Files)+len(plan.Setup) >= continueOnlineMaxFiles || total+size > continueOnlineTotalLimit {
			plan.Skipped = append(plan.Skipped, continueOnlineShow(path, roots)+" (over the copy limit)")
			return
		}
		for n := 2; used[to]; n++ {
			ext := filepath.Ext(to)
			to = strings.TrimSuffix(to, ext) + fmt.Sprintf("-%d", n) + ext
		}
		used[to], total = true, total+size
		*list = append(*list, continueOnlineFile{Source: path, Path: continueOnlineShow(path, roots), To: to, Size: size})
	}
	stamp := time.Now().Format("20060102-1504")
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue // removed since, or not a file
		}
		base, slashed := filepath.Base(path), filepath.ToSlash(path)
		switch {
		case strings.HasPrefix(base, ".env"):
			plan.Skipped = append(plan.Skipped, continueOnlineShow(path, roots)+" (may hold keys; add keys to the online project's vault)")
			continue
		case strings.Contains(slashed, "/.git/") || strings.Contains(slashed, "/node_modules/"):
			continue
		case info.Size() > continueOnlineFileLimit:
			plan.Skipped = append(plan.Skipped, continueOnlineShow(path, roots)+" (larger than 10 MB)")
			continue
		}
		dir := filepath.Dir(path)
		top, known := toplevel[dir]
		if !known {
			top = continueOnlineGit(dir, "rev-parse", "--show-toplevel")
			toplevel[dir] = top
		}
		if top != "" {
			rel, _ := filepath.Rel(top, path)
			if continueOnlineGit(top, "ls-files", "--", rel) != "" {
				repo := repos[top]
				if repo == nil {
					repo = &continueOnlineRepo{Root: top, Name: filepath.Base(top), Origin: continueOnlineGit(top, "remote", "get-url", "origin")}
					repo.Branch = "agentworks/continue-" + continueOnlineSlug(plan.Chat) + "-" + stamp
					repos[top] = repo
				}
				if repo.Origin != "" {
					repo.files = append(repo.files, rel)
					if continueOnlineGit(top, "status", "--porcelain", "--", rel) != "" {
						repo.Changed++
					}
					continue
				}
				// No origin to push to: the file is copied like any other.
			}
		}
		to := "from-mac/" + base
		if rel, err := filepath.Rel(roots.Files, path); err == nil && !strings.HasPrefix(rel, "..") {
			to = "from-mac/" + filepath.ToSlash(rel)
		}
		copyFile(path, to, info.Size(), &plan.Files)
	}
	// A repository goes along only when the chat's own work in it is not on the remote yet: its changed files, or
	// commits not pushed anywhere. Work already pushed is reachable online as it is.
	for _, repo := range repos {
		if repo.Origin == "" {
			continue
		}
		_, _ = fmt.Sscan(continueOnlineGit(repo.Root, "rev-list", "--count", "HEAD", "--not", "--remotes"), &repo.Unpushed)
		if repo.Changed > 0 || repo.Unpushed > 0 {
			plan.Repos = append(plan.Repos, *repo)
		}
	}
	sort.Slice(plan.Repos, func(i, j int) bool { return plan.Repos[i].Name < plan.Repos[j].Name })
	// The project's own instructions, memory and skills, for the online agent to read.
	setup := []string{}
	for _, name := range []string{"PROJECT_INSTRUCTIONS.md", "AGENTS.md", "MEMORY.md"} {
		setup = append(setup, filepath.Join(roots.Files, name))
	}
	for _, root := range []string{roots.Files, roots.Resources} {
		if matches, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md")); err == nil {
			setup = append(setup, matches...)
		}
	}
	for _, path := range setup {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > continueOnlineFileLimit || seen[path] {
			continue
		}
		seen[path] = true
		rel := filepath.Base(path)
		if strings.HasSuffix(path, "SKILL.md") {
			rel = "skills/" + filepath.Base(filepath.Dir(path)) + "/SKILL.md"
		}
		copyFile(path, "from-mac/setup/"+rel, info.Size(), &plan.Setup)
	}
}

func continueOnlineWriteTool(name string) bool {
	name = strings.ToLower(name)
	for _, word := range []string{"write", "edit", "patch", "create_file", "str_replace"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func continueOnlineShow(path string, roots crewroot.Roots) string {
	if rel, err := filepath.Rel(roots.Files, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}

func continueOnlineSlug(s string) string {
	out := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > 30 {
		out = strings.Trim(out[:30], "-")
	}
	return firstNonEmptyTrimmed(out, "chat")
}

// continueOnlineSnapshot is the commit to push: HEAD plus only this chat's uncommitted files, built in a separate
// index so other uncommitted work in the folder, the person's staging and the stash list stay untouched.
func continueOnlineSnapshot(repo continueOnlineRepo) (string, error) {
	head := continueOnlineGit(repo.Root, "rev-parse", "HEAD")
	if head == "" {
		return "", fmt.Errorf("no commit to start from")
	}
	if repo.Changed == 0 {
		return head, nil
	}
	index, err := os.CreateTemp("", "continue-online-index-")
	if err != nil {
		return "", err
	}
	index.Close()
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	if _, err := continueOnlineGitEnv(repo.Root, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := continueOnlineGitEnv(repo.Root, env, append([]string{"add", "-A", "--"}, repo.files...)...); err != nil {
		return "", err
	}
	tree, err := continueOnlineGitEnv(repo.Root, env, "write-tree")
	if err != nil {
		return "", err
	}
	return continueOnlineGitEnv(repo.Root, env, "commit-tree", tree, "-p", head, "-m", "Changes from a chat continued online")
}

func continueOnlineGitEnv(dir string, env []string, args ...string) (string, error) {
	return continueOnlineGitRun(dir, 30*time.Second, env, args...)
}

// continueOnlineGit runs one read-only or push git command without a prompt; empty on any failure.
func continueOnlineGit(dir string, args ...string) string {
	out, err := continueOnlineGitErr(dir, 20*time.Second, args...)
	if err != nil {
		return ""
	}
	return out
}

func continueOnlineGitErr(dir string, timeout time.Duration, args ...string) (string, error) {
	return continueOnlineGitRun(dir, timeout, nil, args...)
}

func continueOnlineGitRun(dir string, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		line := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(line, "\n"); i >= 0 {
			line = line[i+1:]
		}
		return "", fmt.Errorf("%s", firstNonEmptyTrimmed(line, err.Error()))
	}
	return strings.TrimSpace(string(out)), nil
}

// runContinueOnline does the move the plan describes, as the Mac project's owner signed in to the server.
func (api *StreamingAPI) runContinueOnline(ctx context.Context, userID string, plan *continueOnlinePlan, note string) (map[string]any, error) {
	if plan.Problem != "" {
		return nil, fmt.Errorf("%s", plan.Problem)
	}
	if strings.TrimSpace(note) == "" {
		return nil, fmt.Errorf("pass handoff_note: what the online agent needs to carry on (the task, where it stands, what is next, keys it needs)")
	}
	if plan.NewProject {
		made, err := api.createOnlineProjectFromMac(ctx, userID, plan.macProject, plan.conn, plan.ProjectName, "Carries on work moved from a chat on a Mac.")
		if err != nil {
			return nil, fmt.Errorf("the online project could not be made: %v", err)
		}
		var created struct {
			Project string `json:"project"`
			Name    string `json:"name"`
		}
		_ = json.Unmarshal([]byte(made), &created)
		if created.Project == "" {
			return nil, fmt.Errorf("the online project was made but its ID is unknown; open the Team panel and try again")
		}
		plan.ProjectID, plan.ProjectName = created.Project, firstNonEmptyTrimmed(created.Name, plan.ProjectName)
	}
	branches, problems := []map[string]any{}, []string{}
	for _, repo := range plan.Repos {
		sha, err := continueOnlineSnapshot(repo)
		if err != nil {
			problems = append(problems, repo.Name+": "+err.Error())
			continue
		}
		if _, err := continueOnlineGitErr(repo.Root, 2*time.Minute, "push", "origin", sha+":refs/heads/"+repo.Branch); err != nil {
			problems = append(problems, repo.Name+": push failed ("+err.Error()+")")
			continue
		}
		branches = append(branches, map[string]any{"repo": repo.Name, "origin": continueOnlineRemoteName(repo.Origin), "branch": repo.Branch, "changed_files": repo.Changed, "unpushed_commits": repo.Unpushed})
	}
	copied := []string{}
	for _, file := range append(append([]continueOnlineFile{}, plan.Files...), plan.Setup...) {
		data, err := os.ReadFile(file.Source)
		if err != nil {
			problems = append(problems, file.Path+": could not be read")
			continue
		}
		if _, err := callServerTool(ctx, userID, plan.conn, "files", map[string]any{"action": "write", "crew_id": plan.ProjectID, "path": file.To, "content_base64": base64.StdEncoding.EncodeToString(data)}); err != nil {
			problems = append(problems, file.Path+": "+err.Error())
			continue
		}
		copied = append(copied, file.To)
	}
	history := plan.history
	for len(history) > 1 {
		data, _ := json.Marshal(history)
		if len(data) <= continueOnlineHistoryLimit {
			break
		}
		history = history[len(history)/4:] // the oldest part goes first
	}
	message := continueOnlineFirstMessage(plan, note, branches, copied, problems)
	var reply continueOnlineReply
	err := api.continueOnlineRemote(ctx, userID, plan.conn, plan.team, map[string]any{"project_id": plan.ProjectID, "name": plan.Chat, "source": plan.macName, "message": message, "history": history}, &reply)
	if err != nil {
		return nil, fmt.Errorf("the online agent could not be started: %s", laptopActionErrorText(err.Error()))
	}
	log.Printf("[CONTINUE_ONLINE] project=%s server=%s online_project=%s chat=%s new_project=%t branches=%d files=%d problems=%d", plan.macProject, plan.conn.Name, plan.ProjectID, reply.ChatID, plan.NewProject, len(branches), len(copied), len(problems))
	out := map[string]any{"server": plan.Server, "project": plan.ProjectID, "project_name": plan.ProjectName, "new_project": plan.NewProject, "agent": reply.Name, "chat_id": reply.ChatID, "branches": branches, "files_copied": len(copied), "skipped": plan.Skipped}
	if len(problems) > 0 {
		out["problems"] = problems
	}
	return continueOnlineReplyOut(out, reply, plan.Server), nil
}

// continueOnlineAnswer sends the Mac agent's answer to the online agent's question during the handoff.
func (api *StreamingAPI) continueOnlineAnswer(ctx context.Context, userID, macProject, project, chatID, message string) (map[string]any, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(chatID) == "" || strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("pass project and chat_id from the continue_online result, and message")
	}
	_, binding, conn, err := api.desktopCrewChatBinding(ctx, macProject)
	if err != nil {
		return nil, fmt.Errorf("turn on this project's Team and sign in to its server first")
	}
	var reply continueOnlineReply
	if err := api.continueOnlineRemote(ctx, userID, conn, binding.PeerProjectID, map[string]any{"project_id": project, "chat_id": chatID, "message": message}, &reply); err != nil {
		return nil, fmt.Errorf("the online agent could not be reached: %s", laptopActionErrorText(err.Error()))
	}
	return continueOnlineReplyOut(map[string]any{"server": conn.Name, "project": project, "chat_id": chatID}, reply, conn.Name), nil
}

type continueOnlineReply struct {
	ChatID       string `json:"chat_id"`
	Name         string `json:"name"`
	Reply        string `json:"reply"`
	StillWorking bool   `json:"still_working"`
}

func continueOnlineReplyOut(out map[string]any, reply continueOnlineReply, server string) map[string]any {
	switch {
	case reply.StillWorking:
		out["online_agent"] = "still checking; its answer will be in its own chat"
		out["next"] = fmt.Sprintf("Tell the person the chat now continues online in %s on %s and keeps working with the Mac closed. Its tab is in this project's agent list.", firstNonEmptyTrimmed(stringValue(out["project_name"]), "the online project"), server)
	case strings.Contains(strings.ToLower(reply.Reply), "ready, working online"):
		out["online_agent_reply"] = reply.Reply
		out["next"] = "The online agent has everything and is working. Tell the person in one line that the chat continues online (name the project) and that anything typed here is separate from now on."
	default:
		out["online_agent_reply"] = reply.Reply
		out["next"] = "The online agent asked for something. Answer it with server_project_files action=continue_online_answer (project, chat_id, message); copy a missing file first with action=write. Repeat until it says it is ready, then tell the person."
	}
	return out
}

func continueOnlineRemoteName(origin string) string {
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		return u.Host + u.Path // never the credentials an https remote may carry
	}
	if _, rest, ok := strings.Cut(origin, "@"); ok {
		return rest
	}
	return origin
}

func continueOnlineFirstMessage(plan *continueOnlinePlan, note string, branches []map[string]any, copied, problems []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continued from the person's Mac: this chat carries on %q from the Mac project %q. The conversation above came from there.\n\nHandoff note from the agent on the Mac:\n%s\n", plan.Chat, plan.macName, strings.TrimSpace(note))
	if len(branches) > 0 {
		b.WriteString("\nRepository changes, pushed as branches (this chat's files not yet committed are included):\n")
		for _, br := range branches {
			fmt.Fprintf(&b, "- %v: branch %v on %v\n", br["repo"], br["branch"], br["origin"])
		}
	}
	files, setup := []string{}, false
	for _, path := range copied {
		if strings.HasPrefix(path, "from-mac/setup/") {
			setup = true
		} else {
			files = append(files, path)
		}
	}
	if len(files) > 0 {
		b.WriteString("\nFiles copied into this project:\n- " + strings.Join(files, "\n- ") + "\n")
	}
	if setup {
		b.WriteString("\nThe Mac project's instructions, memory and skills are in from-mac/setup/. Read them and follow what applies here.\n")
	}
	if len(plan.Skipped) > 0 {
		b.WriteString("\nNot copied:\n- " + strings.Join(plan.Skipped, "\n- ") + "\n")
	}
	if len(problems) > 0 {
		b.WriteString("\nCould not be copied:\n- " + strings.Join(problems, "\n- ") + "\n")
	}
	b.WriteString("\nFirst check you have what you need to carry on here without the Mac (the branch, files, keys, access). If something is missing, say exactly what in your reply: the Mac agent reads it and sends it. When you have everything, start your reply with \"Ready, working online\", say in a line what you will do next, and carry on with the task.")
	return b.String()
}

// continueOnlineRemote posts to the server's continue-from-mac route with a longer timeout than a Team sync call:
// the conversation can be large and the server waits a little for the online agent's first answer.
func (api *StreamingAPI) continueOnlineRemote(ctx context.Context, userID string, conn desktopConnection, team string, in, out any) error {
	ctx = context.WithValue(ctx, crewTeamTransportKey{}, http.DefaultTransport)
	return crewTeamRemoteJSON(ctx, userID, conn, http.MethodPost, "/api/crew-teams/"+url.PathEscape(team)+"/continue-from-mac", in, out)
}

// handleContinueFromMac is POST /crew-teams/{team}/continue-from-mac on the server. Without chat_id it opens the
// person's new agent in the Team's online project (the same checks as New agent online), saves the Mac conversation
// as its history and starts its first turn with message. With chat_id it sends message to that agent (the Mac agent
// answering during the handoff). It waits a little for the agent's answer; the turn goes on either way.
func (api *StreamingAPI) handleContinueFromMac(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	team := mux.Vars(r)["project"]
	if IsLocalMode() {
		externalError(w, 409, "server_only", "The online agent is made on the server; this Mac sends the request there.")
		return
	}
	if !crewTeamClaims(claims, team, true) {
		externalError(w, 403, "team_forbidden", "Use your own account for this.")
		return
	}
	var in struct {
		ProjectID string                    `json:"project_id"`
		ChatID    string                    `json:"chat_id"`
		Name      string                    `json:"name"`
		Source    string                    `json:"source"`
		Message   string                    `json:"message"`
		History   []llmtypes.MessageContent `json:"history"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, continueOnlineRequestLimit)
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.ProjectID) == "" || strings.TrimSpace(in.Message) == "" {
		externalError(w, 400, "invalid_arguments", "Send the online project and the message for its agent.")
		return
	}
	project := strings.TrimSpace(in.ProjectID)
	t, err := readCrewTeam(team)
	if err != nil || !t.On || !t.member(claims.UserID) || !t.hasProject(project) {
		externalError(w, 404, "project_not_in_team", "That online project isn't used by this Team now.")
		return
	}
	if !crewTeamProjectMakerAllowed(t, project, claims.UserID) {
		externalError(w, 403, "not_in_project", "Only people who work in that project make agents there. Ask "+crewOwnerDisplayName(ownerOfCrewProjectID(project))+" to add you.")
		return
	}
	ctx := context.WithValue(r.Context(), UserContextKey, claims)
	chatID, name := strings.TrimSpace(in.ChatID), strings.TrimSpace(in.Name)
	if chatID == "" {
		name = firstNonEmptyTrimmed(name, "From my Mac")
		args := map[string]any{"crew_id": project, "action": "side_open", "name": name}
		body, _ := json.Marshal(args)
		rec := httptest.NewRecorder()
		api.externalCrewChatsCall(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(ctx), args)
		var opened struct {
			ChatID string `json:"chat_id"`
			Name   string `json:"name"`
		}
		if rec.Code >= 300 || json.Unmarshal(rec.Body.Bytes(), &opened) != nil || opened.ChatID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(max(rec.Code, 409))
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		chatID, name = opened.ChatID, firstNonEmptyTrimmed(opened.Name, name)
		crewTeamWakeSoon()
	}
	key := project + codeChatSideMarker + chatID
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		externalError(w, 500, "continue_online_failed", "The online agent's chat could not be read.")
		return
	}
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
	if !ok || record.ConversationKey != key {
		externalError(w, 404, "chat_not_found", "That online agent is not one of yours in this project.")
		return
	}
	if in.ChatID == "" {
		if len(in.History) > 0 {
			if err := writeCopiedChatHistory(ctx, claims.UserID, record, in.History, nil); err != nil {
				log.Printf("[CONTINUE_ONLINE] team=%s project=%s chat=%s event=history_failed reason=%q", team, project, chatID, err)
				externalError(w, 500, "continue_online_failed", "The conversation could not be saved in the online agent.")
				return
			}
			if err := api.seedForkChatJournal(claims.UserID, record, "", firstNonEmptyTrimmed(in.Source, "your Mac")+" on your Mac", in.History); err != nil {
				log.Printf("[CONTINUE_ONLINE] team=%s project=%s chat=%s event=journal_failed reason=%q", team, project, chatID, err)
			}
		}
		note := continueOnlineFirstTurnNote(name, firstNonEmptyTrimmed(in.Source, "the Mac project"))
		if _, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, claims.UserID, crewProfileID, key, chatContextChange{ForkNote: &note}, claims.UserID); err != nil {
			log.Printf("[CONTINUE_ONLINE] team=%s project=%s chat=%s event=note_failed", team, project, chatID)
		}
	}
	answer := make(chan string, 1)
	turnCtx := context.WithoutCancel(ctx)
	go func() {
		reply, err := api.continueOnlineTurn(turnCtx, claims.UserID, key, in.Message)
		if err != nil {
			log.Printf("[CONTINUE_ONLINE] team=%s project=%s chat=%s event=turn_failed reason=%q", team, project, chatID, err)
			reply = "The online agent could not start: " + err.Error()
		}
		answer <- reply
	}()
	out := continueOnlineReply{ChatID: chatID, Name: name}
	select {
	case out.Reply = <-answer:
		if runes := []rune(out.Reply); len(runes) > continueOnlineReplyMaxChars {
			out.Reply = string(runes[:continueOnlineReplyMaxChars]) + "…"
		}
	case <-time.After(continueOnlineReplyWait):
		out.StillWorking = true
	}
	log.Printf("[CONTINUE_ONLINE] team=%s actor=%s project=%s chat=%s event=turn first=%t answered=%t", team, logUsernameForUserID(claims.UserID), project, chatID, in.ChatID == "", !out.StillWorking)
	externalJSON(w, out)
}

// continueOnlineTurn runs one turn in the person's own online agent, like a message typed in it.
func (api *StreamingAPI) continueOnlineTurn(ctx context.Context, userID, key, text string) (string, error) {
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, userID)
	if err != nil {
		return "", err
	}
	// The chat is the person's own (it is in their registry); the project may be someone else's that they work in.
	binding, _, err := resolveConversationBindingForUser(ctx, userID, profile, key)
	if err != nil {
		return "", fmt.Errorf("that agent is not available to you")
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil {
		return "", err
	}
	reqMap, sessionID, _, err := productBotTurnRequest(ctx, userID, profile, conversation, services.BotIncomingMessage{Text: text}, services.ThreadID{})
	if err != nil {
		return "", err
	}
	reqMap["triggered_by"] = "code_chat"
	reqMap["triggered_by_label"] = "From your Mac"
	result, err := api.startSessionInternalWithResult(ctx, reqMap, sessionID, userID, nil)
	return result.FinalResponse, err
}

func continueOnlineFirstTurnNote(agentName, source string) string {
	return fmt.Sprintf("[This chat is %q, which continues the person's own chat from their Mac project %q. The conversation above is theirs: those replies were written by the agent on the Mac, and you now carry on its work here on the server, where it keeps running with the Mac closed. You do not share files with the Mac: work on the copies and branches the handoff names.]", agentName, source)
}
