package sessiontransfer

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrCutNotFound means the conversation has no prompt matching the cut.
var ErrCutNotFound = fmt.Errorf("the message was not found in the native conversation")

// CutBefore shortens an exported (staged) Claude Code, Codex or agy conversation so
// it ends just before the person's prompt that contains text, counting
// matches from the newest (fromEnd 1 = the latest such prompt). Counting from
// the end works when the native session began after the chat's first turns. Edit and resend (#3134) then
// forks the shortened copy, so the original native session is never written.
// It returns how many of the person's prompts were kept.
func CutBefore(b *Bundle, text string, fromEnd int) (int, error) {
	text = strings.TrimSpace(text)
	if b == nil || text == "" || fromEnd < 1 {
		return 0, fmt.Errorf("choose a message to go back to")
	}
	if b.Format == agyFormat {
		return cutAgy(b, text, fromEnd)
	}
	if b.Format != "claude-jsonl" && b.Format != "codex-rollout" {
		return 0, fmt.Errorf("this native format cannot be cut")
	}
	if _, ok := b.Files["session.jsonl"]; !ok {
		return 0, fmt.Errorf("the native conversation is missing")
	}
	path, err := Path(b.Root, "session.jsonl", false)
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines, err := splitJSONLines(raw)
	if err != nil {
		return 0, err
	}
	var matches []cutMatch
	if b.Format == "claude-jsonl" {
		matches = claudeCutMatches(lines, text)
	} else {
		matches = codexCutMatches(lines, text)
	}
	if fromEnd > len(matches) {
		return 0, ErrCutNotFound
	}
	chosen := matches[len(matches)-fromEnd]
	cut, kept := chosen.line, chosen.before
	var out bytes.Buffer
	for _, line := range lines[:cut] {
		out.Write(line)
		out.WriteByte('\n')
	}
	if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
		return 0, err
	}
	info, err := Inspect(path)
	if err != nil {
		return 0, err
	}
	b.Files["session.jsonl"] = info
	return kept, nil
}

func splitJSONLines(raw []byte) ([][]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	lines := [][]byte{}
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(line)) > 0 {
			if !json.Valid(line) {
				return nil, fmt.Errorf("the native conversation has an unreadable line")
			}
			lines = append(lines, line)
		}
		if err == io.EOF {
			break
		}
	}
	return lines, nil
}

// A Claude Code prompt is a "user" row whose content is the person's text,
// not a tool result, a meta row or a compaction summary. Bookkeeping rows
// written just before it (file snapshots, queue operations) go with it.
// cutMatch is where a matching prompt's turn starts and how many of the
// person's prompts come before it.
type cutMatch struct{ line, before int }

func claudeCutMatches(lines [][]byte, text string) []cutMatch {
	matches, prompts := []cutMatch{}, 0
	for i, line := range lines {
		var row struct {
			Type             string `json:"type"`
			IsMeta           bool   `json:"isMeta"`
			IsSidechain      bool   `json:"isSidechain"`
			IsCompactSummary bool   `json:"isCompactSummary"`
			Message          struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "user" || row.IsMeta || row.IsSidechain || row.IsCompactSummary {
			continue
		}
		prompt, ok := claudePromptText(row.Message.Content)
		if !ok {
			continue
		}
		if strings.Contains(prompt, text) {
			cut := i
			for cut > 0 && claudeBookkeeping(lines[cut-1]) {
				cut--
			}
			matches = append(matches, cutMatch{cut, prompts})
		}
		prompts++
	}
	return matches
}

func claudePromptText(content json.RawMessage) (string, bool) {
	var plain string
	if json.Unmarshal(content, &plain) == nil {
		return plain, strings.TrimSpace(plain) != ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return "", false
	}
	var text strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "tool_result":
			return "", false
		case "text":
			text.WriteString(part.Text)
			text.WriteByte('\n')
		}
	}
	return text.String(), strings.TrimSpace(text.String()) != ""
}

func claudeBookkeeping(line []byte) bool {
	var row struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &row) != nil {
		return false
	}
	return row.Type == "file-history-snapshot" || row.Type == "queue-operation"
}

// A Codex turn starts at its task_started event; the person's prompt is a
// user message inside it. Context rows Codex injects as user messages
// (environment, AGENTS.md) never match the typed text.
func codexCutMatches(lines [][]byte, text string) []cutMatch {
	matches, turns, turnStart := []cutMatch{}, 0, -1
	matchedTurn := -1
	for i, line := range lines {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Message string `json:"message"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if row.Type == "event_msg" && row.Payload.Type == "task_started" {
			if turnStart >= 0 {
				turns++
			}
			turnStart = i
			continue
		}
		prompt := ""
		if row.Type == "response_item" && row.Payload.Type == "message" && row.Payload.Role == "user" {
			for _, part := range row.Payload.Content {
				if part.Type == "input_text" {
					prompt += part.Text + "\n"
				}
			}
		} else if row.Type == "event_msg" && row.Payload.Type == "user_message" {
			prompt = row.Payload.Message
		}
		if prompt == "" || !strings.Contains(prompt, text) || turnStart < 0 || matchedTurn == turnStart {
			continue
		}
		// A prompt is recorded twice (response item and event); count its turn once.
		matchedTurn = turnStart
		matches = append(matches, cutMatch{turnStart, turns})
	}
	return matches
}

// agyUserStep is agy's step type for a prompt the person (or the chat) sent.
const agyUserStep = 14

// cutAgy drops, in the staged copy of an agy conversation, the prompt that
// contains text and every step after it. agy stores a prompt's text verbatim in
// its step, so the match is on those bytes; the app may have wrapped the
// person's text with its own context.
func cutAgy(b *Bundle, text string, fromEnd int) (int, error) {
	if _, ok := b.Files[agyRoot]; !ok {
		return 0, fmt.Errorf("the native conversation is missing")
	}
	path, err := Path(b.Root, agyRoot, false)
	if err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	rows, err := db.Query("SELECT idx, step_payload FROM steps WHERE step_type = ? ORDER BY idx", agyUserStep)
	if err != nil {
		return 0, err
	}
	type prompt struct{ idx, before int }
	var matches []prompt
	prompts := 0
	for rows.Next() {
		var idx int
		var payload []byte
		if err := rows.Scan(&idx, &payload); err != nil {
			rows.Close()
			return 0, err
		}
		if bytes.Contains(payload, []byte(text)) {
			matches = append(matches, prompt{idx, prompts})
		}
		prompts++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if fromEnd > len(matches) {
		return 0, ErrCutNotFound
	}
	chosen := matches[len(matches)-fromEnd]
	if _, err := db.Exec("DELETE FROM steps WHERE idx >= ?", chosen.idx); err != nil {
		return 0, err
	}
	db.Close()
	info, err := Inspect(path)
	if err != nil {
		return 0, err
	}
	b.Files[agyRoot] = info
	return chosen.before, nil
}
