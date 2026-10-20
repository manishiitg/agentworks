package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A label is presentation only. A literal attach NAME wins, including a name
// that happens to have the same shape as an automatically generated address.
func attachedChatLabel(a agentworksclient.AgentAttachment, explicitName string) string {
	if explicitName != "" {
		return explicitName
	}
	if !generatedAgentAddress(a.Provider, a.AgentName) {
		return a.AgentName
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return a.AgentName
	}
	// Native session IDs must never turn a metadata lookup into a path/glob.
	if _, err := uuid.Parse(a.SessionID); err != nil {
		return a.AgentName
	}
	var title string
	switch a.Provider {
	case "codex":
		base := os.Getenv("CODEX_HOME")
		if base == "" {
			base = filepath.Join(home, ".codex")
		}
		title = codexChatTitle(filepath.Join(base, "session_index.jsonl"), a.SessionID)
	case "claude":
		base := os.Getenv("CLAUDE_CONFIG_DIR")
		if base == "" {
			base = filepath.Join(home, ".claude")
		}
		paths, _ := filepath.Glob(filepath.Join(base, "projects", "*", a.SessionID+".jsonl"))
		if len(paths) == 1 {
			title = claudeChatTitle(paths[0], a.SessionID)
		}
	}
	if title = cleanChatLabel(title); title != "" {
		return title
	}
	return a.AgentName
}

func generatedAgentAddress(provider, name string) bool {
	rest, ok := strings.CutPrefix(name, provider+"-")
	if !ok {
		return false
	}
	i := strings.LastIndexByte(rest, '-')
	if i < 0 || i > 12 || len(rest[i+1:]) != 8 {
		return false
	}
	_, err := hex.DecodeString(rest[i+1:])
	return err == nil
}

func cleanChatLabel(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, v)
	r := []rune(strings.Join(strings.Fields(v), " "))
	if len(r) > 160 {
		r = r[:160]
	}
	return string(r)
}

// Read only metadata fields, from this exact session. Oversized transcript
// lines are skipped so a large tool result cannot hide a later rename.
func chatMetadata(path string, visit func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
		} else {
			visit(line)
		}
		if err != nil {
			return
		}
	}
}

func codexChatTitle(path, session string) string {
	title := ""
	chatMetadata(path, func(line []byte) {
		var v struct {
			ID    string `json:"id"`
			Title string `json:"thread_name"`
		}
		if json.Unmarshal(line, &v) == nil && v.ID == session {
			title = v.Title
		}
	})
	return title
}

func claudeChatTitle(path, session string) string {
	custom := ""
	chatMetadata(path, func(line []byte) {
		var v struct {
			Type    string `json:"type"`
			Session string `json:"sessionId"`
			Custom  string `json:"customTitle"`
		}
		if json.Unmarshal(line, &v) != nil || v.Session != session {
			return
		}
		if v.Type == "custom-title" {
			custom = cleanChatLabel(v.Custom)
		}
	})
	return custom
}
