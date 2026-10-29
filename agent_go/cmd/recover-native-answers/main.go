// Command recover-native-answers puts back answers Claude gave inside a chat
// turn that the AgentWorks chat history never saved (PLAT-872: a long held
// turn answered several steers and was then cancelled, so the history kept the
// steers but not the answers).
//
// It reads Claude's own session transcript (the .jsonl file under
// ~/.claude/projects/...) and the chat's conversation record
// (<name>-conversation.json, with its <name>-conversation.history.jsonl), and
// lists every final answer in the transcript (an assistant record with
// stop_reason "end_turn" and text) that no assistant text part of the history
// contains. Each missing answer goes right after the person's message it
// replied to (after the assistant records that followed that message), found
// by that message's text; everything else, including newer
// messages, stays as it is. Answer text is copied verbatim; nothing is made up.
// An answer whose message cannot be found is reported and left out.
//
// Dry run (default) only prints. --apply also needs --app-stopped (a running
// server may hold this chat in memory and save over the repair); it first
// copies both files to <file>.bak-<UTC time>, then saves through the same
// chatlog code the server uses, which rewrites the history log and its
// counters together. Running it again adds nothing.
//
//	go run ./cmd/recover-native-answers --transcript <session>.jsonl --conversation <name>-conversation.json
//	go run ./cmd/recover-native-answers --transcript ... --conversation ... --apply --app-stopped
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

type nativeAnswer struct {
	UUID, Timestamp, Text, Replies string
}

func main() {
	transcript := flag.String("transcript", "", "Claude session transcript (.jsonl)")
	conversation := flag.String("conversation", "", "chat record: <name>-conversation.json (or its .history.jsonl)")
	apply := flag.Bool("apply", false, "write the repaired history (after a backup)")
	appStopped := flag.Bool("app-stopped", false, "confirm the AgentWorks server is stopped (required with --apply)")
	flag.Parse()
	if *transcript == "" || *conversation == "" {
		fmt.Fprintln(os.Stderr, "usage: recover-native-answers --transcript <session>.jsonl --conversation <name>-conversation.json [--apply --app-stopped]")
		os.Exit(2)
	}
	if *apply && !*appStopped {
		fmt.Fprintln(os.Stderr, "refusing --apply: stop the AgentWorks app or server first (it may save this chat over the repair), then add --app-stopped")
		os.Exit(2)
	}
	if err := run(*transcript, conversationPath(*conversation), *apply, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func conversationPath(path string) string {
	if strings.HasSuffix(path, ".history.jsonl") {
		return strings.TrimSuffix(path, ".history.jsonl") + ".json"
	}
	return path
}

func normalized(text string) string { return strings.Join(strings.Fields(text), " ") }

func short(text string, n int) string {
	text = normalized(text)
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

type transcriptEntry struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	TurnOrigin string `json:"turnOrigin"`
	Message    *struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
	} `json:"message"`
	Attachment *struct {
		Type      string `json:"type"`
		Prompt    string `json:"prompt"`
		HumanTurn bool   `json:"humanTurn"`
	} `json:"attachment"`
}

// contentText keeps plain text only: a string content, or "text" blocks.
// Tool calls, tool results and thinking never count.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func readNativeAnswers(path string) ([]nativeAnswer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var answers []nativeAnswer
	lastHuman := ""
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var e transcriptEntry
			if json.Unmarshal(line, &e) == nil && !e.IsSidechain {
				switch {
				case e.Type == "user" && e.Message != nil && !e.IsMeta && (e.Origin == nil || e.Origin.Kind != "task-notification") && e.TurnOrigin != "task_notification":
					if text := contentText(e.Message.Content); text != "" {
						lastHuman = text
					}
				case e.Type == "attachment" && e.Attachment != nil && e.Attachment.Type == "queued_command" && e.Attachment.HumanTurn:
					if text := strings.TrimSpace(e.Attachment.Prompt); text != "" {
						lastHuman = text
					}
				case e.Type == "assistant" && e.Message != nil && e.Message.StopReason == "end_turn":
					if text := contentText(e.Message.Content); text != "" {
						answers = append(answers, nativeAnswer{UUID: e.UUID, Timestamp: e.Timestamp, Text: text, Replies: lastHuman})
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return answers, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

type historyRow struct {
	Role  string            `json:"Role"`
	Parts []json.RawMessage `json:"Parts"`
}

// rowTexts are the row's Text parts only (never tool calls or other parts).
func rowTexts(raw json.RawMessage) (string, []string) {
	var row historyRow
	if json.Unmarshal(raw, &row) != nil {
		return "", nil
	}
	var texts []string
	for _, part := range row.Parts {
		var s string
		if json.Unmarshal(part, &s) == nil {
			texts = append(texts, s)
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(part, &fields) != nil {
			continue
		}
		for _, key := range []string{"Text", "text"} {
			if v, ok := fields[key]; ok && json.Unmarshal(v, &s) == nil {
				texts = append(texts, s)
				break
			}
		}
	}
	return strings.ToLower(row.Role), texts
}

func isAssistant(role string) bool { return role == "ai" || role == "assistant" }
func isHuman(role string) bool     { return role == "human" || role == "user" }

func answerPresent(history []json.RawMessage, answer string) int {
	want := normalized(answer)
	for i, raw := range history {
		role, texts := rowTexts(raw)
		if !isAssistant(role) {
			continue
		}
		for _, t := range texts {
			if strings.Contains(normalized(t), want) {
				return i
			}
		}
		if strings.Contains(normalized(strings.Join(texts, "\n\n")), want) {
			return i
		}
	}
	return -1
}

func humanMatches(raw json.RawMessage, replies string) bool {
	role, texts := rowTexts(raw)
	if !isHuman(role) {
		return false
	}
	have, want := normalized(strings.Join(texts, "\n\n")), normalized(replies)
	if want == "" || have == "" {
		return false
	}
	return have == want || (len(want) >= 12 && strings.Contains(have, want)) || (len(have) >= 12 && strings.Contains(want, have))
}

func run(transcriptPath, conversation string, apply bool, out io.Writer) error {
	answers, err := readNativeAnswers(transcriptPath)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	content, err := chatlog.Load(conversation)
	if err != nil {
		return fmt.Errorf("read conversation: %w", err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(content, &record); err != nil {
		return fmt.Errorf("conversation is not a record: %w", err)
	}
	var history []json.RawMessage
	if err := json.Unmarshal(record["conversation_history"], &history); err != nil {
		return fmt.Errorf("conversation has no conversation_history: %w", err)
	}
	before := len(history)
	fmt.Fprintf(out, "native final answers: %d\nhistory records: %d\n", len(answers), before)

	cursor, present, inserted, unmatched := -1, 0, 0, 0
	for _, a := range answers {
		if at := answerPresent(history, a.Text); at >= 0 {
			present++
			cursor = max(cursor, at)
			continue
		}
		human := -1
		for i := cursor + 1; i < len(history); i++ {
			if humanMatches(history[i], a.Replies) {
				human = i
				break
			}
		}
		if human < 0 {
			unmatched++
			fmt.Fprintf(out, "NOT PLACED %s %s: no message %q found after record %d; answer %q\n", a.UUID, a.Timestamp, short(a.Replies, 60), cursor, short(a.Text, 80))
			continue
		}
		row, err := json.Marshal(historyRow{Role: "ai", Parts: []json.RawMessage{mustText(a.Text)}})
		if err != nil {
			return err
		}
		// After the message and the assistant records that followed it (its tool
		// calls), before the next message of the person.
		at := human + 1
		for at < len(history) {
			if role, _ := rowTexts(history[at]); isHuman(role) {
				break
			}
			at++
		}
		history = append(history[:at], append([]json.RawMessage{row}, history[at:]...)...)
		cursor = at
		inserted++
		_, texts := rowTexts(history[human])
		fmt.Fprintf(out, "INSERT %s %s after record %d (human: %q): %q\n", a.UUID, a.Timestamp, human, short(strings.Join(texts, " "), 60), short(a.Text, 80))
	}
	fmt.Fprintf(out, "already saved: %d, to insert: %d, not placed: %d, history records: %d -> %d\n", present, inserted, unmatched, before, len(history))
	if !apply || inserted == 0 {
		if !apply && inserted > 0 {
			fmt.Fprintln(out, "dry run: nothing written (add --apply --app-stopped to write)")
		}
		return nil
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, path := range []string{conversation, chatlog.HistoryLogPath(conversation)} {
		if err := backup(path, path+".bak-"+stamp); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
		fmt.Fprintf(out, "backup: %s.bak-%s\n", path, stamp)
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		return err
	}
	record["conversation_history"] = encoded
	next, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := chatlog.Save(conversation, next); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Fprintf(out, "saved %s (%d answers inserted)\n", conversation, inserted)
	return nil
}

func mustText(text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]string{"Text": text})
	return raw
}

func backup(from, to string) error {
	data, err := os.ReadFile(from)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}
