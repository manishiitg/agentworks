package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

// A teammate's agent that has been offline past crewTeamAwayGrace is not answered for by a copy. The asking agent
// reads that agent's chat itself, from the copy its Mac already syncs to the server (owner, 22 Nov: "we don't need a
// cli working on server or don't rely on it"). Reading follows the agent's own "Teammates see" choice: a Live chat can
// be read, Just the agent refuses. The agent's person is told who read what when the agent is back.

// crewTeamChatReadAction is the messages tool action (and the in-app read_agent_chat tool) that reads it.
const crewTeamChatReadAction = "history"

// crewTeamChatReadLimit bounds how much of a very large synced chat is numbered; older bytes are left out and said so.
const crewTeamChatReadLimit = 100 << 20

var errCrewTeamNoSyncedChat = errors.New("no synced chat")

type crewTeamChatLines struct {
	binding  crewOfflineBinding
	revision uint64
	lines    []chatHistoryLine
	partial  bool // the Mac is still copying it; the newest messages may be missing
	trimmed  bool // over crewTeamChatReadLimit; only the newest part is numbered
}

// One numbered copy per source agent, rebuilt when a newer sync is committed.
var crewTeamChatLineCache sync.Map

// crewTeamChatReadable: this agent has some of its chat on the server, so an offline ask can offer reading it.
func crewTeamChatReadable(t crewTeam, a crewTeamRegisteredAgent) bool {
	if a.Rule.ChatShare != crewChatShareLive {
		return false
	}
	if _, ok := crewOfflineRowBinding(t, a); !ok {
		return false
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "history.json"))
	return err == nil
}

// crewTeamSyncedChatLines numbers a source agent's synced chat the way chat_history_read numbers a chat's own history:
// one line per message, tool calls with their results, every line through the shared secret policy.
func crewTeamSyncedChatLines(t crewTeam, a crewTeamRegisteredAgent) (crewTeamChatLines, error) {
	b, ok := crewOfflineRowBinding(t, a)
	if !ok {
		return crewTeamChatLines{}, errCrewTeamNoSyncedChat
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return crewTeamChatLines{}, err
	}
	h, err := readCrewOfflineHistory(dir, b)
	if err != nil {
		return crewTeamChatLines{}, err
	}
	if h.Revision == 0 || h.Offset <= 0 {
		return crewTeamChatLines{}, errCrewTeamNoSyncedChat
	}
	key := t.ProjectID + "\x00" + a.ID
	if cached, ok := crewTeamChatLineCache.Load(key); ok {
		if c := cached.(crewTeamChatLines); c.revision == h.Revision && sameCrewOfflineSource(c.binding, b) {
			return c, nil
		}
	}
	stage, err := os.MkdirTemp(dir, "read-")
	if err != nil {
		return crewTeamChatLines{}, err
	}
	defer os.RemoveAll(stage)
	path := filepath.Join(stage, "session.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return crewTeamChatLines{}, err
	}
	// A chat over the limit keeps its newest whole rows: skip the older bytes, then the cut row.
	out := &crewTeamTailWriter{w: file, skip: h.Offset - crewTeamChatReadLimit}
	for _, batch := range h.Batches {
		if err = assembleChunked(filepath.Join(dir, "chunks"), batch, out); err != nil {
			break
		}
	}
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return crewTeamChatLines{}, err
	}
	var history []llmtypes.MessageContent
	switch b.Provider {
	case "claude":
		history, err = claudecode.ReadTranscriptFile(path)
	case "codex":
		history, err = codexcli.ReadTranscriptFile(path)
	default:
		history, err = readCrewTeamAppJournal(path)
	}
	if err != nil {
		return crewTeamChatLines{}, err
	}
	lines := chatHistoryLines(history)
	for i := range lines {
		lines[i].Text = redaction.Text(lines[i].Text)
	}
	c := crewTeamChatLines{binding: b, revision: h.Revision, lines: lines, partial: h.Offset != h.SourceSize, trimmed: out.dropped}
	crewTeamChatLineCache.Store(key, c)
	return c, nil
}

// crewTeamTailWriter drops the first skip bytes and the partial row they cut, then writes the rest.
type crewTeamTailWriter struct {
	w       io.Writer
	skip    int64
	seen    int64
	cutting bool
	dropped bool
}

func (c *crewTeamTailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if c.skip > 0 && c.seen < c.skip {
		take := min(int64(len(p)), c.skip-c.seen)
		c.seen += take
		p = p[take:]
		c.cutting, c.dropped = true, true
	}
	if c.cutting && len(p) > 0 {
		at := bytes.IndexByte(p, '\n')
		if at < 0 {
			return n, nil
		}
		p, c.cutting = p[at+1:], false
	}
	if len(p) > 0 {
		if _, err := c.w.Write(p); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// readCrewTeamAppJournal decodes a synced app chat: one saved message per row.
func readCrewTeamAppJournal(path string) ([]llmtypes.MessageContent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []llmtypes.MessageContent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), crewOfflineDeltaLimit)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var m llmtypes.MessageContent
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, scanner.Err()
}

// crewTeamReadChat is the history action: the viewer's agent reads one teammate agent's synced chat. Admission is the
// same Team row an ask uses, rechecked now, plus the agent's own Teammates see choice and the offline rule.
func crewTeamReadChat(ctx context.Context, viewer, target string, args map[string]any) (map[string]any, error) {
	target = strings.TrimSpace(target)
	if !strings.HasPrefix(target, "agent:") {
		return nil, admissionRefused("Give target: the teammate's agent address from list_agents.")
	}
	row, t, err := crewTeamAdmitAddress(ctx, viewer, target)
	if err != nil {
		return nil, err
	}
	a, ok := t.Agents[row.ID]
	if !ok || row.Kind != "agent" {
		return nil, admissionRefused("Only a teammate's own agent has a chat to read. Ask the Team agent instead.")
	}
	fail := func(reason, text string) (map[string]any, error) {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=refused reason=%s", t.ProjectID, a.ID, viewer, reason)
		return nil, admissionRefused(text)
	}
	if a.OwnerID != viewer && a.Rule.ChatShare != crewChatShareLive {
		return fail("chat_private", crewChatPrivateLine(a.OwnerID))
	}
	if row.Online {
		return fail("source_online", row.Name+" is online. Ask it instead (messages action=ask).")
	}
	off := time.Since(row.OfflineSince)
	if off < crewTeamAwayGrace {
		return fail("away", fmt.Sprintf("%s is only away (offline for %s). Ask it: the ask waits and it answers when it is back.", row.Name, crewTeamOfflineFor(off)))
	}
	chat, err := crewTeamSyncedChatLines(t, a)
	if errors.Is(err, errCrewTeamNoSyncedChat) {
		return fail("not_synced", row.Name+"'s chat is not on the server yet. Ask again with offline=\"wait\" and it answers when it is back.")
	}
	if err != nil {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=refused reason=read_failed error=%q", t.ProjectID, a.ID, viewer, err.Error())
		return nil, fmt.Errorf("%s's chat could not be read now. Try again in a minute.", row.Name)
	}
	out := map[string]any{"agent": row.Name, "person": row.Person, "offline_for": crewTeamOfflineFor(off), "lines": len(chat.lines),
		"note": "This is " + firstNonEmptyTrimmed(row.Person, "your teammate") + "'s own chat with this agent, read while it is offline. It may be out of date; they see what you read when they are back."}
	if chat.partial {
		out["notice"] = "This chat is still being copied to the server, so the newest messages may be missing."
	} else if chat.trimmed {
		out["notice"] = "This chat is very large: only its newest part can be read."
	}
	query, line := chatHistoryQueryArg(args), chatHistoryIntArg(args, "line")
	if query == "" && line == 0 {
		out["next"] = fmt.Sprintf("Search it with query=\"words\" (newest matches first), or read one message with line=N (1 to %d).", len(chat.lines))
		return out, nil
	}
	result, err := readChatHistoryLines(chat.lines, args)
	if err != nil {
		return nil, err
	}
	out["result"] = result
	what, seen := "", []string{}
	switch r := result.(type) {
	case chatHistorySearchResult:
		what = fmt.Sprintf("searched for %q", query)
		for _, m := range r.Matches {
			seen = append(seen, strconv.Itoa(m.Line))
		}
	case chatHistoryLine:
		what = "read line " + strconv.Itoa(r.Line)
		seen = append(seen, strconv.Itoa(r.Line))
	}
	if a.OwnerID != viewer {
		recordCrewTeamChatRead(t, a, chat.binding, viewer, what, seen)
	}
	log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=read offline_for=%s query_len=%d line=%d lines_seen=%d revision=%d", t.ProjectID, a.ID, viewer, off.Round(time.Second), len(query), line, len(seen), chat.revision)
	return out, nil
}

// recordCrewTeamChatRead queues "who read what" for the agent's own person; it reaches the agent when it is back,
// through the same return path its Mac already reads.
func recordCrewTeamChatRead(t crewTeam, a crewTeamRegisteredAgent, b crewOfflineBinding, reader, what string, seen []string) {
	saw := "no matches"
	if len(seen) > 0 {
		saw = "saw lines " + strings.Join(seen, ", ")
	}
	teamDir, err := crewTeamDir(t.ProjectID)
	if err == nil {
		mu := crewTeamRecordMutex(teamDir)
		mu.Lock()
		defer mu.Unlock()
		var dir string
		if dir, err = crewOfflineDirectory(t.ProjectID, a.ID); err == nil {
			err = appendCrewOfflineReturn(t.ProjectID, a.ID, dir, b, crewOfflineReturnedAnswer{ID: "read-" + uuid.NewString(), At: time.Now().UTC(), Asker: reader, Question: what, Answer: saw})
		}
	}
	if err != nil {
		log.Printf("[TEAM_CHAT_READ] project=%s agent=%s caller=%s event=read_note_failed error=%q", t.ProjectID, a.ID, reader, err.Error())
	}
}
