package chatlog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrAlreadyConverted means the chat folder already holds turns.
var ErrAlreadyConverted = errors.New("chatlog: chat folder already has turns")

// Fields of the old single-record and header+history formats that do not
// carry over into a chat folder header.
var oldRecordFields = map[string]bool{
	"conversation_history": true, "ui_events": true,
	"history_log": true, "history_count": true, "history_system": true,
	"history_tail": true, "history_total": true,
	"structured_turn_checkpoints": true,
}

// Plan is what a whole conversation record becomes as a chat folder.
type Plan struct {
	Header map[string]json.RawMessage
	Turns  []Turn
	// UserTurns counts person messages (a restart once cut a chat to fewer).
	UserTurns int
	Messages  int
	Bytes     int64
	// real is each turn's time from its own saved events (zero when unknown).
	real []time.Time
}

// PlanRecord splits a whole conversation record (as Load returns it) into a
// header and one turn per person message. The saved UI events go on the last
// turn, so the restored trace keeps them.
func PlanRecord(record []byte, idPrefix string) (*Plan, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(record, &fields); err != nil {
		return nil, err
	}
	var history, events []json.RawMessage
	if raw, ok := fields["conversation_history"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &history); err != nil {
			return nil, err
		}
	}
	if raw, ok := fields["ui_events"]; ok && string(raw) != "null" {
		_ = json.Unmarshal(raw, &events)
	}
	start := time.Now().UTC()
	for _, key := range []string{"created_at", "updated_at"} {
		var value string
		if json.Unmarshal(fields[key], &value) == nil {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				start = parsed.Add(-time.Duration(len(history)) * time.Millisecond)
				break
			}
		}
	}
	system, turns := SplitHistory(history, idPrefix, start)
	real := placeEvents(turns, events)
	plan := &Plan{Header: map[string]json.RawMessage{}, Turns: turns, real: real}
	for key, value := range fields {
		if !oldRecordFields[key] {
			plan.Header[key] = value
		}
	}
	if system != nil {
		plan.Header[hSystem] = system
	}
	plan.count()
	return plan, nil
}

// count fills a plan's person turns, messages and bytes from its turns.
func (plan *Plan) count() {
	plan.UserTurns, plan.Messages, plan.Bytes = 0, 0, 0
	for _, turn := range plan.Turns {
		plan.Messages += len(turn.Messages)
		for _, message := range turn.Messages {
			var row struct {
				Role string `json:"Role"`
			}
			if json.Unmarshal(message, &row) == nil && (row.Role == "human" || row.Role == "user") {
				plan.UserTurns++
			}
		}
		line, _ := json.Marshal(turn)
		plan.Bytes += int64(len(line)) + 1
	}
	header, _ := json.Marshal(plan.Header)
	plan.Bytes += int64(len(header))
}

// ConvertRecord writes a whole conversation record as a new chat folder. It
// refuses a folder that already has turns.
func ConvertRecord(dir string, record []byte, idPrefix string) (*Plan, error) {
	plan, err := PlanRecord(record, idPrefix)
	if err != nil {
		return nil, err
	}
	return plan, WritePlan(dir, plan, nil)
}

// WritePlan writes a plan as a new chat folder; keep are lines to append after
// it (turns added since an earlier conversion). It refuses a folder that
// already has turns.
func WritePlan(dir string, plan *Plan, keep []Turn) error {
	if info, err := os.Stat(filepath.Join(dir, TurnsFile)); err == nil && info.Size() > 0 {
		return ErrAlreadyConverted
	}
	if _, err := AppendTurns(dir, append(append([]Turn(nil), plan.Turns...), keep...), plan.Header, ModeReplace); err != nil {
		return err
	}
	return nil
}

// MergeReport says where a merged chat's turns came from.
type MergeReport struct {
	// FromCopy[i] is how many kept turns are copy i's version.
	FromCopy []int
	// Disagree counts turns found in several copies with different content.
	Disagree int
}

// MergePlans joins every copy of one chat (oldest copy first) into one plan:
// the union of their turns. A turn is known by its person message text and
// how many times that text came before it; a turn only one copy has goes
// right after the turn it follows in that copy. When several copies hold a
// turn, the fullest version (most messages and events) is kept. The header
// is the newest copy's, with a title from any copy when it has none.
func MergePlans(plans []*Plan, idPrefix string) (*Plan, MergeReport) {
	report := MergeReport{FromCopy: make([]int, len(plans))}
	type slot struct {
		key   string
		turn  Turn
		real  time.Time
		copy  int
		first int
		sum   string
		multi bool
	}
	var order []*slot
	byKey := map[string]*slot{}
	keyOf := func(plan *Plan) []string {
		seen := map[string]int{}
		keys := make([]string, len(plan.Turns))
		for ti, turn := range plan.Turns {
			text := firstPersonText(turn)
			keys[ti] = fmt.Sprintf("%x#%d", sha256.Sum256([]byte(text)), seen[text])
			seen[text]++
		}
		return keys
	}
	for ci, plan := range plans {
		keys := keyOf(plan)
		inCopy := make(map[string]bool, len(keys))
		for _, key := range keys {
			inCopy[key] = true
		}
		after := -1
		for ti, turn := range plan.Turns {
			key := keys[ti]
			var real time.Time
			if ti < len(plan.real) {
				real = plan.real[ti]
			}
			sum := turnContent(turn)
			if existing := byKey[key]; existing != nil {
				if existing.sum != sum {
					existing.multi = true
				}
				if len(turn.Messages)+len(turn.UIEvents) > len(existing.turn.Messages)+len(existing.turn.UIEvents) {
					existing.turn, existing.copy, existing.sum = turn, ci, sum
				}
				if existing.real.IsZero() {
					existing.real = real
				}
				for i, s := range order {
					if s == existing {
						after = i
					}
				}
				continue
			}
			s := &slot{key: key, turn: turn, real: real, copy: ci, sum: sum}
			byKey[key] = s
			// Turns only older copies have, and this copy lacks, came before
			// this copy went its own way: a newer copy's own turns follow them.
			for after+1 < len(order) && order[after+1].first < ci && !inCopy[order[after+1].key] {
				after++
			}
			s.first = ci
			order = append(order, nil)
			copy(order[after+2:], order[after+1:])
			order[after+1] = s
			after++
		}
	}
	merged := &Plan{Header: map[string]json.RawMessage{}}
	if len(plans) > 0 {
		for key, value := range plans[len(plans)-1].Header {
			merged.Header[key] = value
		}
		for i := len(plans) - 2; i >= 0; i-- {
			if raw, ok := merged.Header["title"]; ok && string(raw) != `""` && string(raw) != "null" {
				break
			}
			if raw, ok := plans[i].Header["title"]; ok {
				merged.Header["title"] = raw
			}
		}
	}
	real := make([]time.Time, len(order))
	for i, s := range order {
		turn := s.turn
		turn.ID = fmt.Sprintf("%s-%d", idPrefix, i+1)
		merged.Turns = append(merged.Turns, turn)
		real[i] = s.real
		report.FromCopy[s.copy]++
		if s.multi {
			report.Disagree++
		}
	}
	merged.real = real
	setTurnTimes(merged.Turns, real)
	merged.count()
	return merged, report
}

func turnContent(turn Turn) string {
	sum := sha256.New()
	for _, message := range turn.Messages {
		sum.Write(compactRaw(message))
		sum.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", sum.Sum(nil))
}

type oldEvent struct {
	raw    json.RawMessage
	turnID string
	kind   string
	text   string
	at     time.Time
}

func decodeOldEvent(raw json.RawMessage) oldEvent {
	var e struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Data      struct {
			Data struct {
				Content  string `json:"content"`
				Metadata struct {
					TurnID         string `json:"turn_id"`
					DisplayContent string `json:"display_content"`
				} `json:"metadata"`
			} `json:"data"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &e)
	text := e.Data.Data.Metadata.DisplayContent
	if strings.TrimSpace(text) == "" {
		text = e.Data.Data.Content
	}
	return oldEvent{raw: raw, turnID: e.Data.Data.Metadata.TurnID, kind: e.Type, text: normalizeText(text), at: e.Timestamp}
}

func normalizeText(text string) string { return strings.Join(strings.Fields(text), " ") }

// firstPersonText is the text of a turn's first person message.
func firstPersonText(turn Turn) string {
	for _, message := range turn.Messages {
		var row struct {
			Role  string `json:"Role"`
			Parts []struct {
				Text string `json:"Text"`
			} `json:"Parts"`
		}
		if json.Unmarshal(message, &row) != nil || (row.Role != "human" && row.Role != "user") {
			continue
		}
		var texts []string
		for _, part := range row.Parts {
			texts = append(texts, part.Text)
		}
		return normalizeText(strings.Join(texts, " "))
	}
	return ""
}

// placeEvents puts each saved UI event on the turn it belongs to. Events are
// grouped by their own metadata.turn_id; a group's user_message is matched to
// the converted turn whose person message has that text, which also gives the
// turn its real time. A group with no match goes to the matched turn whose
// time window holds its first event; only what matches neither goes on the
// last turn. Turn times are then made strictly increasing in turn order.
func placeEvents(turns []Turn, events []json.RawMessage) []time.Time {
	if len(turns) == 0 {
		return nil
	}
	type group struct {
		events []oldEvent
		first  time.Time
		text   string
	}
	var groups []*group
	byTurn := map[string]*group{}
	for _, raw := range events {
		e := decodeOldEvent(raw)
		g := byTurn[e.turnID]
		if e.turnID == "" || g == nil {
			g = &group{}
			groups = append(groups, g)
			if e.turnID != "" {
				byTurn[e.turnID] = g
			}
		}
		g.events = append(g.events, e)
		if !e.at.IsZero() && (g.first.IsZero() || e.at.Before(g.first)) {
			g.first = e.at
		}
		if e.kind == "user_message" && g.text == "" {
			g.text = e.text
		}
	}
	real := make([]time.Time, len(turns))
	owner := make([]int, len(groups))
	next := 0
	for gi, g := range groups {
		owner[gi] = -1
		if g.text == "" {
			continue
		}
		for ti := next; ti < len(turns); ti++ {
			text := firstPersonText(turns[ti])
			if text != "" && (text == g.text || strings.Contains(text, g.text) || strings.Contains(g.text, text)) {
				owner[gi], next = ti, ti+1
				if !g.first.IsZero() {
					real[ti] = g.first
				}
				break
			}
		}
	}
	for gi, g := range groups {
		if owner[gi] >= 0 {
			continue
		}
		owner[gi] = len(turns) - 1
		if g.first.IsZero() {
			continue
		}
		for ti := len(turns) - 1; ti >= 0; ti-- {
			if !real[ti].IsZero() && !real[ti].After(g.first) {
				owner[gi] = ti
				break
			}
		}
	}
	for gi, g := range groups {
		for _, e := range g.events {
			turns[owner[gi]].UIEvents = append(turns[owner[gi]].UIEvents, e.raw)
		}
	}
	setTurnTimes(turns, real)
	return real
}

// setTurnTimes makes turn times strictly increasing in turn order, using the
// real ones where known.
func setTurnTimes(turns []Turn, real []time.Time) {
	var earliest time.Time
	for _, at := range real {
		if !at.IsZero() {
			earliest = at
			break
		}
	}
	var prev time.Time
	for i := range turns {
		at := real[i]
		if i == 0 && at.IsZero() {
			if !earliest.IsZero() {
				at = earliest.Add(-time.Duration(len(turns)) * time.Millisecond)
			} else if parsed, err := time.Parse(time.RFC3339Nano, turns[0].At); err == nil {
				at = parsed
			}
		}
		if at.IsZero() || (!prev.IsZero() && !at.After(prev)) {
			at = prev.Add(time.Millisecond)
		}
		turns[i].At = at.UTC().Format(time.RFC3339Nano)
		turns[i].SenderUserID = TurnSender(turns[i].UIEvents)
		prev = at
	}
}
