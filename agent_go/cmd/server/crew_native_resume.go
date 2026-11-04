package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// carryClaudeTranscriptBetweenRuntimes lets a Claude chat resume in this turn's runtime folder when its transcript was
// written from another runtime folder of the same Crew (owner's Main, 4 Nov: a turn that ran in Run mode left its
// session under the Run folder, and the next Builder turn's `claude --resume` found nothing and exited 1). Claude
// keeps <store>/projects/<slug of the folder>/<id>.jsonl; when this folder's copy is missing, the one from a sibling
// runtime whose project link is this Crew's is copied in (the original stays). Every outcome is logged.
func carryClaudeTranscriptBetweenRuntimes(agent *mcpagent.Agent) {
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil || current.Provider.Provider != "claude-code" {
		return
	}
	id := strings.TrimSpace(current.Provider.NativeSessionID)
	dir := strings.TrimSpace(current.Provider.WorkingDir)
	if id != "" && dir != "" && !strings.ContainsAny(id, `/\`) {
		carryClaudeTranscriptIntoRuntime(dir, id)
	}
}

func carryClaudeTranscriptIntoRuntime(dir, id string) {
	crew, err := os.Readlink(filepath.Join(dir, cliruntime.ProjectLink))
	if err != nil {
		return // not a linked Crew runtime
	}
	slug := sessiontransfer.ClaudeSlug(dir)
	for _, store := range claudeTranscriptStores(dir) {
		target := filepath.Join(store, "projects", slug, id+".jsonl")
		if _, err := os.Stat(target); err == nil {
			return
		}
		matches, _ := filepath.Glob(filepath.Join(store, "projects", "*", id+".jsonl"))
		for _, match := range matches {
			from := claudeSiblingRuntime(filepath.Dir(dir), filepath.Base(filepath.Dir(match)), crew)
			if from == "" {
				log.Printf("[NATIVE_RESUME] Claude session %s is under %s, not a runtime of this Crew; not copied", id, filepath.Base(filepath.Dir(match)))
				continue
			}
			if err := copyClaudeTranscript(match, target); err != nil {
				log.Printf("[NATIVE_RESUME] could not copy Claude session %s from runtime %s into %s: %v", id, filepath.Base(from), filepath.Base(dir), err)
				return
			}
			log.Printf("[NATIVE_RESUME] copied Claude session %s from runtime %s into %s; resuming natively", id, filepath.Base(from), filepath.Base(dir))
			return
		}
	}
	log.Printf("[NATIVE_RESUME] Claude session %s has no transcript for runtime %s; claude --resume will not find it", id, filepath.Base(dir))
}

// claudeTranscriptStores lists where Claude Code may keep this runtime's sessions: the runtime's own private home on a
// server, else the account's Claude folder (a personal Mac).
func claudeTranscriptStores(dir string) []string {
	stores := []string{filepath.Join(dir, security.SandboxPersistentDirName, "cli-home", "claude-code", ".claude")}
	if config := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); config != "" {
		stores = append(stores, config)
	}
	if home, err := os.UserHomeDir(); err == nil {
		stores = append(stores, filepath.Join(home, ".claude"))
	}
	out := []string{}
	for _, store := range stores {
		if info, err := os.Stat(filepath.Join(store, "projects")); err == nil && info.IsDir() {
			out = append(out, store)
		}
	}
	return out
}

// claudeSiblingRuntime returns the runtime folder in base whose Claude slug is slug and whose project link is crew.
func claudeSiblingRuntime(base, slug, crew string) string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		dir := filepath.Join(base, entry.Name())
		if sessiontransfer.ClaudeSlug(dir) != slug {
			continue
		}
		if link, err := os.Readlink(filepath.Join(dir, cliruntime.ProjectLink)); err == nil && link == crew {
			return dir
		}
	}
	return ""
}

func copyClaudeTranscript(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	tmp := to + ".carry"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}
