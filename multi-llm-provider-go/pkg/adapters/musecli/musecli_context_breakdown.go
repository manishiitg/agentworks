package musecli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

// ContextBreakdown is what filled Muse's latest model request, in bytes, from
// the request trace Muse writes to its own session log. Total is all of it.
type ContextBreakdown struct {
	Total int
	// Setup is Muse's own instructions (its system prompt).
	Setup int
	// Skills is the skills list Muse adds to its context.
	Skills int
	// Context is the rest of the context Muse adds (memory, workspace and
	// session notes, policies).
	Context int
	// Messages, Replies and ToolResults are the conversation itself.
	Messages, Replies, ToolResults int
}

// ReadContextBreakdown reads the latest request trace of a Muse session; ok is
// false when the session log has none.
func ReadContextBreakdown(nativeSessionID string, accountDataHome ...string) (ContextBreakdown, bool) {
	path := museSessionLogPath(nativeSessionID, accountDataHome...)
	if path == "" {
		return ContextBreakdown{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return ContextBreakdown{}, false
	}
	defer f.Close()
	type lane struct {
		LogicalLane struct {
			Value string `json:"value"`
		} `json:"logical_lane"`
		Source struct {
			Value string `json:"value"`
		} `json:"source"`
		ByteCount int `json:"byte_count"`
	}
	var row struct {
		Payload struct {
			Event struct {
				Kind      string `json:"kind"`
				BlockID   string `json:"block_id"`
				TextBytes int    `json:"text_bytes"`
				Bounded   struct {
					TotalLaneBytes int    `json:"total_lane_bytes"`
					Aggregates     []lane `json:"aggregates"`
				} `json:"bounded"`
			} `json:"event"`
		} `json:"payload"`
	}
	empty := row
	var out ContextBreakdown
	var lanes []lane
	skills := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	traceMarker, blockMarker := []byte(`"model_input_trace_recorded"`), []byte(`"context_block_diagnostic"`)
	for scanner.Scan() {
		line := scanner.Bytes()
		trace, block := bytes.Contains(line, traceMarker), bytes.Contains(line, blockMarker)
		if !trace && !block {
			continue
		}
		row = empty
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		event := row.Payload.Event
		switch {
		case event.Kind == "context_block_diagnostic" && event.BlockID == "skills_catalog":
			skills = event.TextBytes
		case event.Kind == "model_input_trace_recorded" && event.Bounded.TotalLaneBytes > 0:
			out.Total, lanes = event.Bounded.TotalLaneBytes, event.Bounded.Aggregates
		}
	}
	if out.Total <= 0 {
		return ContextBreakdown{}, false
	}
	for _, l := range lanes {
		switch l.LogicalLane.Value {
		case "system_base":
			out.Setup += l.ByteCount
		case "context":
			out.Context += l.ByteCount
		case "tool_result":
			out.ToolResults += l.ByteCount
		case "history":
			switch l.Source.Value {
			case "history":
				out.Replies += l.ByteCount
			case "current_user":
				out.Messages += l.ByteCount
			default:
				out.Context += l.ByteCount
			}
		}
	}
	if skills > 0 && skills <= out.Context {
		out.Skills, out.Context = skills, out.Context-skills
	}
	return out, true
}
