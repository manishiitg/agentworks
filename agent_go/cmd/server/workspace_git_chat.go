package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	chatevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

// This is navigation evidence, not file authorship: a tool addressed a path in
// this checkout. Current branches and dirty files still come exclusively from Git.
// PLAT-746: never derive chat worktrees from global snapshot differences.
type workspaceGitChatUsage struct {
	sync.Mutex
	since int
	paths map[string]bool
}

func (api *StreamingAPI) markWorkspaceGitChatUsage(sessionID, base string, repos []workspaceGitRepo) {
	value, _ := api.workspaceGitChatUsage.LoadOrStore(sessionID+"\x00"+base, &workspaceGitChatUsage{since: -1, paths: map[string]bool{}})
	usage := value.(*workspaceGitChatUsage)
	usage.Lock()
	defer usage.Unlock()
	page := api.eventStore.GetEvents(sessionID, chatevents.GetEventsOptions{SinceIndex: usage.since})
	for _, event := range page.Events {
		if event.Type != "tool_call_start" || event.Data == nil || event.Data.Data == nil {
			continue
		}
		// Persisted journals restore generic payloads; live events are typed. Decode
		// the same small envelope for both, without retaining arguments or output.
		raw, err := json.Marshal(event.Data.Data)
		if err != nil {
			continue
		}
		var tool struct {
			ToolParams struct {
				Arguments string `json:"arguments"`
			} `json:"tool_params"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			continue
		}
		for _, path := range workspaceGitToolPaths(base, tool.ToolParams.Arguments) {
			usage.paths[path] = true
		}
	}
	usage.since = page.LastProcessedIndex
	for path := range usage.paths {
		best := -1
		for i := range repos {
			root := filepath.Join(base, filepath.FromSlash(repos[i].Root))
			rel, err := filepath.Rel(root, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && (best < 0 || len(repos[i].Root) > len(repos[best].Root)) {
				best = i
			}
		}
		if best >= 0 {
			repos[best].UsedInChat = true
		}
	}
}

func workspaceGitToolPaths(base, arguments string) []string {
	var params map[string]json.RawMessage
	if json.Unmarshal([]byte(arguments), &params) != nil {
		return nil
	}
	cwd := base
	var paths []string
	add := func(value string) {
		if value == "" || strings.ContainsAny(value, "$`\n") {
			return
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(cwd, value)
		}
		value = filepath.Clean(value)
		// Resolve the existing parent of deleted/new files too. A symlink leading
		// outside the workspace must never associate a tool with a local checkout.
		ancestor := value
		for {
			if real, err := filepath.EvalSymlinks(ancestor); err == nil {
				suffix, _ := filepath.Rel(ancestor, value)
				value = filepath.Join(real, suffix)
				break
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return
			}
			ancestor = parent
		}
		rel, err := filepath.Rel(base, value)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			paths = append(paths, value)
		}
	}
	for _, key := range []string{"workdir", "cwd", "working_directory"} {
		var dir string
		if json.Unmarshal(params[key], &dir) == nil && dir != "" && !strings.ContainsAny(dir, "$`\n") {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(base, dir)
			}
			cwd = filepath.Clean(dir)
			add(cwd)
			break
		}
	}
	for _, key := range []string{"path", "file_path", "filepath", "directory", "repo_path", "project_path"} {
		var path string
		if json.Unmarshal(params[key], &path) == nil {
			add(path)
		}
	}
	var list []string
	if json.Unmarshal(params["paths"], &list) == nil {
		for _, path := range list {
			add(path)
		}
	}
	for _, key := range []string{"command", "cmd"} {
		var command string
		if json.Unmarshal(params[key], &command) != nil {
			continue
		}
		tokens := workspaceGitLiteralWords(command)
		for i := 0; i < len(tokens); i++ {
			// Only command positions: a quoted echo containing "cd ..." is not use.
			if i > 0 && tokens[i-1] != ";" && tokens[i-1] != "&" && tokens[i-1] != "|" && tokens[i-1] != "\n" {
				continue
			}
			if tokens[i] == "cd" && i+1 < len(tokens) {
				dir := tokens[i+1]
				if strings.ContainsAny(dir, "$`") || strings.HasPrefix(dir, "-") {
					continue
				}
				add(dir)
				if filepath.IsAbs(dir) {
					cwd = filepath.Clean(dir)
				} else {
					cwd = filepath.Join(cwd, dir)
				}
			}
			if tokens[i] == "git" && i+2 < len(tokens) && tokens[i+1] == "-C" {
				add(tokens[i+2])
			}
		}
	}
	return paths
}

// Recognize literal shell words only; never expand variables, substitutions or
// execute a command. Quoted folders with spaces are common on personal Macs.
func workspaceGitLiteralWords(command string) []string {
	var out []string
	var word strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	for _, c := range command {
		if escaped {
			word.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case ' ', '\t', '\r':
			flush()
		case ';', '&', '|', '\n':
			flush()
			out = append(out, string(c))
		default:
			word.WriteRune(c)
		}
	}
	if quote != 0 || escaped {
		return nil
	}
	flush()
	return out
}
