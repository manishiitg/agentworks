package musecli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// An edited message rewinds a Muse chat. Muse's session log is event-sourced
// with content hashes, so it is never cut as a file: the chat keeps a pending
// rewind instead, and the next `muse serve` start asks Muse itself to fork the
// session after the last turn to keep (`session/fork` with a cut point).
//
// The pending rewind rides in the chat's saved Muse session id:
// `<id>#rewind:<fromEnd>:<text>` with one such part per edit made before the
// next message. The first turn after the fork reports the new session's real
// id, which replaces it.
const museRewindPart = "#rewind:"

// museRewindNeedleBytes bounds the edited message's text kept in the id: the
// opening of a message is enough to tell it apart.
const museRewindNeedleBytes = 1024

// MuseRewindResumeID returns the saved session id for a chat whose message
// text (the fromEnd-th such message from the newest) was edited. sessionID may
// already hold pending rewinds; this one is applied after them.
func MuseRewindResumeID(sessionID, text string, fromEnd int) string {
	return sessionID + museRewindPart + strconv.Itoa(fromEnd) + ":" + base64.RawURLEncoding.EncodeToString([]byte(museRewindNeedle(text)))
}

type museRewindCut struct {
	text    string
	fromEnd int
}

// parseMuseRewind splits a saved id into the source session and its pending
// cuts, oldest first. ok is false for a plain session id.
func parseMuseRewind(id string) (string, []museRewindCut, bool) {
	parts := strings.Split(id, museRewindPart)
	if len(parts) < 2 || parts[0] == "" {
		return id, nil, false
	}
	cuts := make([]museRewindCut, 0, len(parts)-1)
	for _, part := range parts[1:] {
		count, encoded, found := strings.Cut(part, ":")
		fromEnd, err := strconv.Atoi(count)
		text, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if !found || err != nil || fromEnd < 1 || decodeErr != nil || strings.TrimSpace(string(text)) == "" {
			return id, nil, false
		}
		cuts = append(cuts, museRewindCut{text: string(text), fromEnd: fromEnd})
	}
	return parts[0], cuts, true
}

func museRewindNeedle(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= museRewindNeedleBytes {
		return text
	}
	text = text[:museRewindNeedleBytes]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return strings.TrimSpace(text)
}

type museHistoryItem struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	TurnID string `json:"turnId"`
}

var errMuseRewindToStart = errors.New("the edited message started the Muse session")

// museRewindLastTurn applies the cuts in order and returns the last turn to
// keep: the turn before the one holding the edited message.
func museRewindLastTurn(items []museHistoryItem, cuts []museRewindCut) (string, error) {
	last := ""
	for _, cut := range cuts {
		var matches []string
		for _, item := range items {
			if item.Kind == "userMessage" && item.TurnID != "" && strings.Contains(item.Text, cut.text) {
				matches = append(matches, item.TurnID)
			}
		}
		if cut.fromEnd > len(matches) {
			return "", fmt.Errorf("the edited message is not in the Muse session")
		}
		edited := matches[len(matches)-cut.fromEnd]
		previous, end := "", len(items)
		for i, item := range items {
			if item.TurnID == edited {
				end = i
				break
			}
			if item.TurnID != "" {
				previous = item.TurnID
			}
		}
		if previous == "" {
			return "", errMuseRewindToStart
		}
		items, last = items[:end], previous
	}
	return last, nil
}

// museMSPForkBefore opens a fork of source that ends after the last turn the
// cuts keep. opened receives the new session.
func museMSPForkBefore(ctx context.Context, rpc *museRPC, source string, cuts []museRewindCut, opened *museMSPSessionResult) error {
	var read struct {
		History struct {
			Mode  string            `json:"mode"`
			Items []museHistoryItem `json:"items"`
		} `json:"history"`
	}
	if err := rpc.call(ctx, "session/read", map[string]any{"sessionId": source, "excludeItems": false}, &read); err != nil {
		return fmt.Errorf("read the Muse session: %w", err)
	}
	if read.History.Mode != "inline" {
		return fmt.Errorf("Muse served the session's history as %q, not item by item", read.History.Mode)
	}
	last, err := museRewindLastTurn(read.History.Items, cuts)
	if err != nil {
		return err
	}
	params := map[string]any{"commandId": museCommandID(), "sessionId": source, "cutPoint": map[string]any{"lastTurnId": last}, "excludeItems": true}
	if err := rpc.call(ctx, "session/fork", params, opened); err != nil {
		return fmt.Errorf("fork the Muse session: %w", err)
	}
	if opened.Session.SessionID == "" {
		return fmt.Errorf("Muse returned no session for the fork")
	}
	return nil
}
