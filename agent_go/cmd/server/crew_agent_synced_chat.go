package server

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// A shared agent's chat is synced from its person's Mac to the server (PLAT-746-1024). Teammates' agents read it
// while the agent is offline (crew_team_chat_read.go) and forks copy it (crew_agent_self_fork.go). Nothing on the
// server answers as the agent.

// crewOfflineCopyTabPrefix names the tabs the removed offline copies left in Team homes. They stay hidden until the
// one-off cleanup removes them on every server (#3194 follow-up), then this goes.
const crewOfflineCopyTabPrefix = "copy-"

// crewOfflineCopyHomeKey: the key is one of those leftover copy tabs in this home.
func crewOfflineCopyHomeKey(home, key string) bool {
	tab, ok := strings.CutPrefix(key, home+codeChatSideMarker+crewOfflineCopyTabPrefix)
	return ok && tab != ""
}

func crewOfflineRowBinding(t crewTeam, a crewTeamRegisteredAgent) (crewOfflineBinding, bool) {
	if a.Kind != "terminal" {
		return crewOfflineAppBinding(t, a)
	}
	if a.Terminal == nil {
		return crewOfflineBinding{}, false
	}
	b := crewOfflineBinding{Provider: a.Terminal.Provider, Session: a.ChatKey, Attachment: a.Terminal.AttachmentID}
	return b, b.valid()
}

// crewOfflineAppBinding is an app source's current conversation, as last reported by its owner's paired app and kept
// in the private history record. Only a source that is shared has one.
func crewOfflineAppBinding(t crewTeam, a crewTeamRegisteredAgent) (crewOfflineBinding, bool) {
	if wanted, err := crewOfflineAppWanted(t, a); err != nil || !wanted {
		return crewOfflineBinding{}, false
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return crewOfflineBinding{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err != nil {
		return crewOfflineBinding{}, false
	}
	var h crewOfflineHistory
	if json.Unmarshal(raw, &h) != nil {
		return crewOfflineBinding{}, false
	}
	b := h.crewOfflineBinding
	return b, b.Provider == "agentworks" && b.valid()
}

func crewOfflineNativeFormat(provider string) (string, string, bool) {
	switch provider {
	case "claude":
		return "claude-code", "claude-jsonl", true
	case "codex":
		return "codex-cli", "codex-rollout", true
	}
	return "", "", false
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// stageCrewOfflineNativeBundle writes the model's runtime journal (stage/source/session.jsonl) from the private raw
// journal. The raw source stays as synced; every string value of the runtime copy goes through the shared secret
// policy, keeping each row's JSON structure and native handles.
func stageCrewOfflineNativeBundle(raw io.Reader, b crewOfflineBinding, program, format, stage string) (*sessiontransfer.Bundle, error) {
	source := filepath.Join(stage, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return nil, err
	}
	journal, err := os.OpenFile(filepath.Join(source, "session.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(journal, sum)}
	err = redactCrewOfflineJSONL(raw, counted)
	if cerr := journal.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	bundle := &sessiontransfer.Bundle{Provider: program, Format: format, Transport: "tmux", SessionID: b.Session, Workspace: string(filepath.Separator),
		Date: time.Now().UTC().Format("2006/01/02"), Files: map[string]sessiontransfer.File{"session.jsonl": {Size: counted.n, SHA256: hex.EncodeToString(sum.Sum(nil))}}, Root: source}
	if err = bundle.Validate(); err != nil {
		return nil, err
	}
	return bundle, nil
}

// redactCrewOfflineJSONL copies native JSONL rows with every string value passed through the shared secret policy.
func redactCrewOfflineJSONL(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), crewOfflineDeltaLimit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid native history line")
		}
		if text, ok := value.(string); ok {
			value = redaction.Text(text)
		}
		redactCrewOfflineValue(value)
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil { // Encode ends the row with a newline
			return err
		}
		if _, err := w.Write(out.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}
