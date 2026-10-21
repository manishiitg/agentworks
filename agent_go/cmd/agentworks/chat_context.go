package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Only directory metadata from this exact session. No remote URLs, tool text,
// prompts or transcript-derived description of what the agent is doing.
func attachedChatContext(ctx context.Context, a agentworksclient.AgentAttachment, cwd string) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if cwd == "" {
		if _, err := uuid.Parse(a.SessionID); err != nil {
			return "", ""
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ""
		}
		switch a.Provider {
		case "codex":
			base := os.Getenv("CODEX_HOME")
			if base == "" {
				base = filepath.Join(home, ".codex")
			}
			cwd, _ = usageIndex(ctx, filepath.Join(base, "state_5.sqlite"), "SELECT cwd, '' FROM threads WHERE id=?", a.SessionID)
		case "claude":
			base := os.Getenv("CLAUDE_CONFIG_DIR")
			if base == "" {
				base = filepath.Join(home, ".claude")
			}
			paths, _ := filepath.Glob(filepath.Join(base, "projects", "*", a.SessionID+".jsonl"))
			if len(paths) == 1 {
				f, err := os.Open(paths[0])
				if err == nil {
					// The first bounded record contains cwd/session metadata. Never scan bodies.
					line, _ := bufio.NewReaderSize(f, 64<<10).ReadSlice('\n')
					var header struct {
						Session string `json:"sessionId"`
						Cwd     string `json:"cwd"`
					}
					if json.Unmarshal(line, &header) == nil && header.Session == a.SessionID {
						cwd = header.Cwd
					}
					_ = f.Close()
				}
			}
		}
	}
	if !filepath.IsAbs(cwd) {
		return "", ""
	}
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	raw, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	parts := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(parts) != 2 {
		return "", ""
	}
	return cleanChatLabel(filepath.Base(parts[0])), cleanChatLabel(parts[1])
}
