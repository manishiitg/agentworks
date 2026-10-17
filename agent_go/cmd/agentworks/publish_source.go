package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// ownPublicationSource has no session/path override: a chat may export only its
// verified invoking session. Website publication uses its own server chat store.
func ownPublicationSource(ctx context.Context, o *options, root string) (publication.Source, error) {
	config, err := o.path()
	if err != nil {
		return publication.Source{}, err
	}
	own, ok, err := ownAgentSession(ctx, filepath.Dir(config), o)
	if err != nil {
		return publication.Source{}, err
	}
	if !ok || own.session == "" {
		return publication.Source{}, fmt.Errorf("Publish from your Claude Code or Codex chat so its exact conversation can be saved. The website can publish its own project chat.")
	}
	if _, err = uuid.Parse(own.session); err != nil {
		return publication.Source{}, fmt.Errorf("this chat has no valid native session identity")
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		return publication.Source{}, err
	}
	got, err := filepath.EvalSymlinks(own.cwd)
	if err != nil || want != got {
		return publication.Source{}, fmt.Errorf("publish from this chat's own project folder")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return publication.Source{}, err
	}
	base, p := "", ""
	switch own.provider {
	case "codex":
		base = o.getenv("CODEX_HOME")
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
		p, _ = usageIndex(ctx, filepath.Join(base, "state_5.sqlite"), "SELECT rollout_path, COALESCE(model,'') FROM threads WHERE id=?", own.session)
		base = filepath.Join(base, "sessions")
		if p == "" {
			matches, _ := filepath.Glob(filepath.Join(base, "*", "*", "*", "rollout-*-"+own.session+".jsonl"))
			if len(matches) == 1 {
				p = matches[0]
			}
		}
	case "claude":
		base = o.getenv("CLAUDE_CONFIG_DIR")
		if base == "" {
			base = filepath.Join(home, ".claude")
		}
		base = filepath.Join(base, "projects")
		matches, _ := filepath.Glob(filepath.Join(base, "*", own.session+".jsonl"))
		if len(matches) == 1 {
			p = matches[0]
		}
	default:
		return publication.Source{}, fmt.Errorf("Raw chat publishing currently supports Claude Code and Codex. Publish from one of those chats, or use the website's own project chat.")
	}
	if p == "" {
		return publication.Source{}, fmt.Errorf("this exact chat's raw history is unavailable; no other conversation was selected")
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return publication.Source{}, fmt.Errorf("native history is outside its provider's session store")
	}
	store, err := os.OpenRoot(base)
	if err != nil {
		return publication.Source{}, err
	}
	defer store.Close()
	f, err := store.Open(rel)
	if err != nil {
		return publication.Source{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return publication.Source{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
		return publication.Source{}, fmt.Errorf("this chat exceeds the 64 MiB transfer limit; its raw history was not truncated")
	}
	raw, err := io.ReadAll(io.LimitReader(f, info.Size()))
	if err != nil {
		return publication.Source{}, err
	}
	// Capture only complete records while the current chat continues writing.
	end := bytes.LastIndexByte(raw, '\n') + 1
	if end == 0 {
		return publication.Source{}, fmt.Errorf("this chat has no complete saved history record yet")
	}
	raw = raw[:end]
	matched := false
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row map[string]any
		if json.Unmarshal(line, &row) != nil {
			return publication.Source{}, fmt.Errorf("the captured native chat contains an incomplete history record; retry after it saves")
		}
		if own.provider == "claude" && row["sessionId"] == own.session {
			matched = true
		}
		if row["type"] == "session_meta" {
			if payload, ok := row["payload"].(map[string]any); ok && payload["id"] == own.session {
				matched = true
			}
		}
	}
	if !matched {
		return publication.Source{}, fmt.Errorf("native history does not match this exact chat")
	}
	version := ""
	bin := map[string]string{"claude": "claude", "codex": "codex"}[own.provider]
	if output, versionErr := exec.CommandContext(ctx, bin, "--version").Output(); versionErr == nil {
		version = strings.TrimSpace(string(output))
		if len(version) > 120 {
			version = version[:120]
		}
	}
	return publication.Source{History: publication.History{Provider: own.provider, SessionID: own.session, SourceCWD: want, CLIVersion: version, CapturedBytes: int64(len(raw))}, Raw: raw}, nil
}

func publicationIgnored(ctx context.Context, root string) map[string]bool {
	return publication.GitIgnored(ctx, root)
}
