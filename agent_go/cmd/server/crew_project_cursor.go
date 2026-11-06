package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Cursor CLI keeps UUID conversations in a cwd-hashed directory. Its SDK has
// a different store: never treat one as the other or search other folders.
func listLocalCursorSessions(ctx context.Context, project string, source localNativeSource) ([]localProjectNativeSession, bool) {
	candidate, err := sessiontransfer.CursorCLIPath(source.Root, project, "00000000-0000-4000-8000-000000000000")
	if err != nil {
		return nil, true
	}
	rel, _ := filepath.Rel(source.Root, filepath.Dir(candidate))
	root, err := sessiontransfer.PathDirectory(source.Root, filepath.ToSlash(rel), false)
	if err != nil {
		return nil, !os.IsNotExist(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, true
	}
	running := localCursorRunningSessions(ctx)
	out := []localProjectNativeSession{}
	partial := false
	for n, entry := range entries {
		if ctx.Err() != nil || n >= 20000 {
			partial = true
			break
		}
		if !entry.IsDir() || !sessiontransfer.UUID.MatchString(entry.Name()) {
			continue
		}
		session, ok := localCursorSessionAt(project, source, entry.Name())
		if ok {
			session.InUse = running[session.NativeID]
			out = append(out, session)
		}
	}
	return out, partial
}

func localCursorSessionAt(project string, source localNativeSource, id string) (localProjectNativeSession, bool) {
	directory, err := sessiontransfer.CursorCLIPath(source.Root, project, id)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	rel, _ := filepath.Rel(source.Root, directory)
	metadata, err := sessiontransfer.Path(source.Root, filepath.ToSlash(filepath.Join(rel, "meta.json")), false)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	info, err := os.Stat(metadata)
	if err != nil || info.Size() > 1<<20 {
		return localProjectNativeSession{}, false
	}
	raw, err := os.ReadFile(metadata)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	var meta struct {
		Cwd, Title, Name string
		SchemaVersion    int
	}
	if json.Unmarshal(raw, &meta) != nil || meta.SchemaVersion != 1 {
		return localProjectNativeSession{}, false
	}
	cwd, err := filepath.EvalSymlinks(meta.Cwd)
	if err != nil || cwd != project {
		return localProjectNativeSession{}, false
	}
	path, err := sessiontransfer.Path(source.Root, filepath.ToSlash(filepath.Join(rel, "store.db")), false)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	info, err = os.Stat(path)
	if err != nil {
		return localProjectNativeSession{}, false
	}
	updated := info.ModTime()
	// SQLite reads the WAL too; reject linked sidecars before previewing.
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar, e := sessiontransfer.Path(source.Root, filepath.ToSlash(filepath.Join(rel, "store.db"+suffix)), false)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return localProjectNativeSession{}, false
		}
		if stat, e := os.Stat(sidecar); e == nil && stat.ModTime().After(updated) {
			updated = stat.ModTime()
		}
	}
	title := strings.Join(strings.Fields(firstNonEmptyTrimmed(meta.Title, meta.Name, "Cursor conversation")), " ")
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "…"
	}
	return localProjectNativeSession{ID: localNativeSessionKey(source, id), Provider: source.Provider, Account: source.Label, Title: title, UpdatedAt: updated.UTC().Format(time.RFC3339), NativeID: id, Path: path, Project: project, Source: source}, true
}

// Best-effort advisory only. Absence is not a claim that no other writer exists;
// the picker always explains that Cursor continues the original conversation.
func localCursorRunningSessions(ctx context.Context) map[string]bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	output, _ := exec.CommandContext(ctx, "ps", "-axo", "args=").Output()
	found := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(strings.ToLower(line), "cursor") {
			continue
		}
		args := strings.Fields(line)
		for i, arg := range args {
			if (arg == "--resume" || arg == "--resume-session") && i+1 < len(args) && sessiontransfer.UUID.MatchString(args[i+1]) {
				found[args[i+1]] = true
			}
			if id, ok := strings.CutPrefix(arg, "--resume="); ok && sessiontransfer.UUID.MatchString(id) {
				found[id] = true
			}
		}
	}
	return found
}

func readLocalNativeMessages(session localProjectNativeSession) ([]builderConversationMessage, error) {
	switch session.Provider {
	case "claude-code":
		messages, _, err := readNewClaudeTranscriptMessages(session.Path, time.Time{})
		return messages, err
	case "codex-cli":
		messages, _, err := readCodexTranscriptMessages(session.Path)
		return messages, err
	case "cursor-cli":
		// Recheck exact-folder metadata and links when preview/import is requested.
		current, ok := localCursorSessionAt(session.Project, session.Source, session.NativeID)
		if !ok || current.Path != session.Path {
			return nil, fmt.Errorf("this Cursor conversation is no longer available")
		}
		transcript, found, err := cursorcli.ReadNativeTranscriptFromConfigDir(session.Source.Root, session.NativeID, session.Project)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("this Cursor conversation has no saved history")
		}
		return builderConversationMessagesFromLLMTypes(transcript.Messages), nil
	}
	return nil, fmt.Errorf("this conversation's AI program is unavailable")
}

// PLAT-746-335: the owner chose same-session continuation for Cursor. The CLI
// launcher links this admitted account's native store into its private config;
// no journal or credentials are copied and the SDK store is never substituted.
func (api *StreamingAPI) continueLocalCursorRuntime(r *http.Request, roots crewroot.Roots, conversation ProductConversationRecord, selected localProjectNativeSession, model string) (*ChatHistoryAgentRuntime, error) {
	for _, command := range []string{"cursor-agent", "tmux"} {
		if _, err := exec.LookPath(command); err != nil {
			return nil, fmt.Errorf("install Cursor CLI and tmux on this Mac, then try Continue again")
		}
	}
	destination := conversation
	destination.Provider, destination.ModelID, destination.ConnectionID = selected.Provider, model, selected.Source.ConnectionID
	_, runtimeDir, err := api.crewProgramStore(r.Context(), GetUserFromContext(r.Context()), roots, destination, "tmux", false)
	if err != nil {
		return nil, err
	}
	cwd, err := crewCLIWorkingDir(roots.Virtual, roots.Owner, conversation.SessionID, selected.Provider, false)
	if err != nil {
		return nil, err
	}
	if cwd != roots.Files {
		return nil, fmt.Errorf("this Cursor conversation belongs to a different project folder")
	}
	return crewNativeRuntime(destination, conversation.SessionID, &sessiontransfer.Bundle{Provider: "cursor-cli", Transport: "tmux", Format: "cursor-cli-sqlite", SessionID: selected.NativeID}, cwd, runtimeDir), nil
}
