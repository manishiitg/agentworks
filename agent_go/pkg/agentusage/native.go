// Package agentusage reads only provider-recorded numeric usage. It never
// tokenizes conversations or decodes message, tool or thinking content.
package agentusage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

const (
	MaxReadBytes       = 32 << 20
	MaxLineBytes       = 2 << 20
	MaxCalls           = 65536
	MaxTokens    int64 = 1 << 52
)

var (
	ErrUnavailable = errors.New("native usage unavailable")
	ErrLimit       = errors.New("native usage read or record cap reached")
	ErrIdentity    = errors.New("native usage session identity mismatch")
)

// Input includes cache; cached tokens are a subset, never added to Total.
type Counters struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
	Cached int64 `json:"cached_tokens"`
}

func (c Counters) Valid() bool {
	return c.Input >= 0 && c.Output >= 0 && c.Cached >= 0 && c.Cached <= c.Input && c.Input <= MaxTokens && c.Output <= MaxTokens && c.Input+c.Output <= MaxTokens
}

func (c Counters) Total() int64 { return c.Input + c.Output }

func (c Counters) Difference(previous Counters) (Counters, bool) {
	d := Counters{c.Input - previous.Input, c.Output - previous.Output, c.Cached - previous.Cached}
	return d, d.Valid()
}

type Native struct {
	BytesRead int64 `json:"-"`
	Counters
	Model  string `json:"model,omitempty"`
	Source string `json:"source"`
	Turns  *int64 `json:"turn_count,omitempty"`
	Calls  *int64 `json:"model_call_count,omitempty"`
}

func ModelID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 || strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return ""
	}
	return s
}

func records(r io.Reader, visit func([]byte) error) error {
	l := &io.LimitedReader{R: r, N: MaxReadBytes + 1}
	s := bufio.NewScanner(l)
	s.Buffer(make([]byte, 65536), MaxLineBytes)
	for s.Scan() {
		if l.N <= 0 {
			return ErrLimit
		}
		if err := visit(s.Bytes()); err != nil {
			return err
		}
	}
	if s.Err() != nil || l.N <= 0 {
		return ErrLimit
	}
	return nil
}

type anthropicCounters struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
	Read   int64 `json:"cache_read_input_tokens"`
	Write  int64 `json:"cache_creation_input_tokens"`
}

func (a anthropicCounters) normalized() (Counters, bool) {
	if a.Input < 0 || a.Read < 0 || a.Write < 0 || a.Input > MaxTokens || a.Read > MaxTokens || a.Write > MaxTokens {
		return Counters{}, false
	}
	c := Counters{Input: a.Input + a.Read + a.Write, Output: a.Output, Cached: a.Read + a.Write}
	return c, c.Valid()
}

// Claude repeats a call's usage on assistant content blocks. Later native
// updates replace earlier numbers for that ID; they are not additional calls.
func Claude(r io.Reader, session string) (Native, error) {
	type call struct {
		counters Counters
		model    string
	}
	calls := map[string]call{}
	users := map[string]struct{}{}
	var model string
	err := records(r, func(line []byte) error {
		var e struct {
			Type    string `json:"type"`
			Session string `json:"sessionId"`
			ID      string `json:"uuid"`
			Request string `json:"requestId"`
			Message struct {
				ID    string             `json:"id"`
				Model string             `json:"model"`
				Usage *anthropicCounters `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		if e.Session != "" && e.Session != session {
			return ErrIdentity
		}
		if e.Type == "user" && e.ID != "" {
			if len(users) >= MaxCalls {
				return ErrLimit
			}
			users[e.ID] = struct{}{}
		}
		if e.Type != "assistant" || e.Message.Usage == nil {
			return nil
		}
		id := e.Message.ID
		if id == "" {
			id = e.Request
		}
		if id == "" {
			return ErrUnavailable
		}
		c, ok := e.Message.Usage.normalized()
		if !ok {
			return ErrUnavailable
		}
		if _, found := calls[id]; !found && len(calls) >= MaxCalls {
			return ErrLimit
		}
		model = ModelID(e.Message.Model)
		calls[id] = call{c, model}
		return nil
	})
	if err != nil {
		return Native{}, err
	}
	if len(calls) == 0 {
		return Native{}, ErrUnavailable
	}
	out := Native{Source: "Claude transcript usage", Model: model}
	for _, c := range calls {
		out.Input += c.counters.Input
		out.Output += c.counters.Output
		out.Cached += c.counters.Cached
	}
	if !out.Counters.Valid() {
		return Native{}, ErrUnavailable
	}
	n := int64(len(calls))
	out.Calls = &n
	if len(users) > 0 {
		n := int64(len(users))
		out.Turns = &n
	}
	return out, nil
}

// Codex's latest cumulative native total is authoritative. Neither repeated
// token_count events nor last_token_usage are added to it.
func Codex(r io.Reader, session string) (Native, error) {
	var out Native
	var found, identity bool
	err := records(r, func(line []byte) error {
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type  string `json:"type"`
				ID    string `json:"id"`
				Model string `json:"model"`
				Info  *struct {
					Total *struct {
						Input  int64 `json:"input_tokens"`
						Output int64 `json:"output_tokens"`
						Cached int64 `json:"cached_input_tokens"`
						Write  int64 `json:"cache_write_input_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		if e.Type == "session_meta" {
			if e.Payload.ID != session {
				return ErrIdentity
			}
			identity = true
		}
		if e.Type == "turn_context" {
			out.Model = ModelID(e.Payload.Model)
		}
		if e.Type == "event_msg" && e.Payload.Type == "token_count" && e.Payload.Info != nil && e.Payload.Info.Total != nil {
			c := e.Payload.Info.Total
			if c.Cached < 0 || c.Write < 0 || c.Cached > MaxTokens || c.Write > MaxTokens {
				return ErrUnavailable
			}
			out.Counters = Counters{c.Input, c.Output, c.Cached + c.Write}
			if !out.Counters.Valid() {
				return ErrUnavailable
			}
			found = true
		}
		return nil
	})
	if err != nil {
		return Native{}, err
	}
	if !identity {
		return Native{}, ErrIdentity
	}
	if !found {
		return Native{}, ErrUnavailable
	}
	out.Source = "Codex session total"
	return out, nil
}

// Muse has no verified token total in its session index. Sum only unique,
// reported provider attribution records in the exact native session log.
func Muse(r io.Reader, session string) (Native, error) {
	var out Native
	seen := map[string]struct{}{}
	runs := map[string]struct{}{}
	err := records(r, func(line []byte) error {
		var e struct {
			ID       string `json:"id"`
			Sequence int64  `json:"sequence"`
			Stream   struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			} `json:"stream"`
			Payload struct {
				Kind  string `json:"kind"`
				Run   string `json:"run_id"`
				Event struct {
					Kind   string `json:"kind"`
					Record struct {
						Family   string `json:"usage_family"`
						Quantity struct {
							Unit     string `json:"unit"`
							Reported bool   `json:"reported"`
							Input    int64  `json:"input_tokens"`
							Output   int64  `json:"output_tokens"`
							Cached   int64  `json:"cached_tokens"`
						} `json:"quantity"`
						Owner struct {
							Run string `json:"run_id"`
						} `json:"owner"`
					} `json:"record"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) != nil {
			return nil
		}
		if e.Stream.Kind == "session" && e.Stream.ID != "" && e.Stream.ID != session {
			return ErrIdentity
		}
		rec := e.Payload.Event.Record
		q := rec.Quantity
		if e.Payload.Kind != "run" || e.Payload.Event.Kind != "goal_usage_attribution" || rec.Family != "provider" || q.Unit != "tokens" || !q.Reported {
			return nil
		}
		if e.ID == "" {
			return ErrUnavailable
		}
		if _, ok := seen[e.ID]; ok {
			return nil
		}
		if len(seen) >= MaxCalls {
			return ErrLimit
		}
		seen[e.ID] = struct{}{}
		c := Counters{q.Input, q.Output, q.Cached}
		if !c.Valid() {
			return ErrUnavailable
		}
		out.Input += c.Input
		out.Output += c.Output
		out.Cached += c.Cached
		run := rec.Owner.Run
		if run == "" {
			run = e.Payload.Run
		}
		if run != "" {
			runs[run] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return Native{}, err
	}
	if len(seen) == 0 || !out.Counters.Valid() {
		return Native{}, ErrUnavailable
	}
	out.Source = "Muse provider attribution"
	n := int64(len(seen))
	out.Calls = &n
	if len(runs) > 0 {
		n := int64(len(runs))
		out.Turns = &n
	}
	return out, nil
}

// Pi supplies SDK getSessionStats directly, which includes cache warming,
// compaction and tool-result usage. Only its numeric projection is accepted.
func PiStats(r io.Reader, session string) (Native, error) {
	var e struct {
		Session string `json:"sessionId"`
		Model   string `json:"model"`
		Turns   *int64 `json:"userMessages"`
		Tokens  struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
			Read   int64 `json:"cacheRead"`
			Write  int64 `json:"cacheWrite"`
			Total  int64 `json:"total"`
		} `json:"tokens"`
	}
	d := json.NewDecoder(io.LimitReader(r, 4097))
	if d.Decode(&e) != nil {
		return Native{}, ErrUnavailable
	}
	if e.Session != session {
		return Native{}, ErrIdentity
	}
	c, ok := (anthropicCounters{e.Tokens.Input, e.Tokens.Output, e.Tokens.Read, e.Tokens.Write}).normalized()
	if !ok || e.Tokens.Total != c.Total() || e.Turns != nil && (*e.Turns < 0 || *e.Turns > MaxTokens) {
		return Native{}, ErrUnavailable
	}
	return Native{Counters: c, Source: "Pi getSessionStats", Model: ModelID(e.Model), Turns: e.Turns}, nil
}
