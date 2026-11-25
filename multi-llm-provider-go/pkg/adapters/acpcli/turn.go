package acpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

// cancelWait bounds how long a cancelled turn waits for the agent to answer
// its prompt as cancelled before the process is stopped.
var cancelWait = 30 * time.Second

// pump reads every event for the life of the process: a running turn gets
// them; between turns, requests are answered with no person attending and
// notifications are dropped (session/load replays history this way).
func (s *session) pump() {
	idle := &nativeinput.Requests{}
	defer idle.Close()
	for {
		select {
		case <-s.rpc.done:
			return
		case msg := <-s.rpc.events:
			if msg.Method == "" {
				s.rpc.deliver(msg)
				continue
			}
			s.mu.Lock()
			listener := s.listener
			s.mu.Unlock()
			if listener != nil {
				select {
				case listener <- msg:
				case <-s.rpc.done:
					return
				}
				continue
			}
			if msg.isRequest() {
				s.answer(context.Background(), msg, idle)
			}
		}
	}
}

func (s *session) listen() <-chan message {
	ch := make(chan message, 4096)
	s.mu.Lock()
	s.listener = ch
	s.mu.Unlock()
	return ch
}

func (s *session) stopListening() {
	s.mu.Lock()
	s.listener = nil
	s.mu.Unlock()
}

type toolCall struct {
	name, args, result string
	started            time.Time
	open, ended        bool
}

type sessionUpdate struct {
	Kind       string          `json:"sessionUpdate"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	ToolKind   string          `json:"kind"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	Entries    json.RawMessage `json:"entries"`
	Used       *int            `json:"used"`
	Size       *int            `json:"size"`
	// ConfigOptions is set on config_option_update.
	ConfigOptions []configOption `json:"configOptions"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		Input       int `json:"inputTokens"`
		Output      int `json:"outputTokens"`
		CachedRead  int `json:"cachedReadTokens"`
		CachedWrite int `json:"cachedWriteTokens"`
		Thought     int `json:"thoughtTokens"`
		Total       int `json:"totalTokens"`
	} `json:"usage"`
}

// turn sends one prompt and streams the agent's updates until it answers.
func (s *session) turn(ctx context.Context, prompt string, emit func(llmtypes.StreamChunk), images turnImages) (*llmtypes.ContentResponse, error) {
	sessionID := s.currentSessionID()
	meta := map[string]any{"acp_agent": s.agent.ID, s.agent.metaKey("session_id"): sessionID, s.agent.metaKey("stream_source"): "acp"}
	events := s.listen()
	defer s.stopListening()
	questions := &nativeinput.Requests{}
	defer questions.Close()
	input := []any{}
	if prompt != "" {
		input = append(input, map[string]any{"type": "text", "text": prompt})
	}
	params, cleanup, err := paramsWithImages(ctx, s.agent.providerID(), images.owner, map[string]any{"sessionId": sessionID, "prompt": input}, images.messages, s.caps.PromptCapabilities.Image)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	s.mu.Lock()
	label := s.handleModel()
	s.mu.Unlock()
	var outcome func(model, stopReason string) error
	if s.outcome != nil {
		outcome = s.outcome.BeginTurn(sessionID)
	}
	// Every prompt of this turn answers here; a steer adds one (seq counts them) and only the latest ends the turn.
	complete := make(chan promptDone, 16)
	seq := 1
	sendPrompt := func(n int, params any) {
		go func() {
			// The prompt's own lifetime, not ctx: on Stop it is cancelled and waits for the agent's answer.
			var result promptResult
			err := s.rpc.call(context.Background(), "session/prompt", params, &result)
			complete <- promptDone{seq: n, err: err, result: result}
		}()
	}
	sendPrompt(seq, params)
	var steers chan steerRequest
	if s.steersWithPrompt {
		steers = make(chan steerRequest)
		s.mu.Lock()
		s.steer, s.steerDone = steers, make(chan struct{})
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			close(s.steerDone)
			s.steer, s.steerDone = nil, nil
			s.mu.Unlock()
		}()
	}
	var steeredUsage *llmtypes.Usage
	if image, ok := params.(*imageParams); ok {
		if err := <-image.wrote; err != nil {
			return nil, err
		}
	}
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: meta})
	var text strings.Builder
	tools := map[string]*toolCall{}
	turnCtx := ctx
	handle := func(msg message) {
		if msg.isRequest() {
			s.answer(turnCtx, msg, questions)
			return
		}
		if msg.Method != "session/update" {
			return
		}
		var envelope struct {
			SessionID string        `json:"sessionId"`
			Update    sessionUpdate `json:"update"`
		}
		if json.Unmarshal(msg.Params, &envelope) != nil || envelope.SessionID != sessionID {
			return
		}
		s.handleUpdate(envelope.Update, meta, tools, &text, emit)
	}
	cancelled := false
	for {
		select {
		case msg := <-events:
			handle(msg)
		case req := <-steers:
			if cancelled {
				req.reply <- errSteerNotTaken("the turn is stopping")
				continue
			}
			seq++
			sendPrompt(seq, map[string]any{"sessionId": sessionID, "prompt": []any{map[string]any{"type": "text", "text": req.text}}})
			log.Printf("[ACP_CLI] session=%s agent=%s action=steered prompt=%d", s.rpc.owner, s.agent.ID, seq)
			req.reply <- nil
		case done := <-complete:
			if done.seq != seq {
				// A steer took over this prompt: the agent answered it end_turn and carries the turn on.
				steeredUsage = addUsage(steeredUsage, promptUsage(done.result))
				if done.err != nil {
					log.Printf("[ACP_CLI] session=%s agent=%s action=steered_prompt_failed prompt=%d err=%v", s.rpc.owner, s.agent.ID, done.seq, done.err)
				}
				continue
			}
			err, result := done.err, done.result
			for len(events) > 0 {
				handle(<-events)
			}
			if cancelled || result.StopReason == "cancelled" {
				return nil, context.Canceled
			}
			if err != nil {
				if mapped := s.setupError(err); llmtypes.IsCodingAgentAuthRequiredError(mapped) {
					return nil, mapped
				}
				log.Printf("[ACP_CLI] session=%s agent=%s action=failed reason=prompt_failed last_error=%q", s.rpc.owner, s.agent.ID, s.rpc.stderrLine())
				if s.outcome != nil {
					return nil, s.outcome.RPCFailure(label, sessionID, err)
				}
				return nil, err
			}
			if outcome != nil {
				if failure := outcome(label, result.StopReason); failure != nil {
					return nil, failure
				}
			}
			if failure := s.stopFailure(result.StopReason); failure != nil {
				return nil, failure
			}
			usage := addUsage(steeredUsage, promptUsage(result))
			if strings.TrimSpace(text.String()) == "" && len(tools) == 0 {
				return nil, fmt.Errorf("%s returned no answer (stop reason: %s)", s.agent.Label, result.StopReason)
			}
			return s.response(text.String(), usage), nil
		case <-ctx.Done():
			if !cancelled {
				cancelled = true
				params := map[string]any{"sessionId": sessionID}
				if len(s.agent.CancelMeta) > 0 {
					params["_meta"] = s.agent.CancelMeta
				}
				_ = s.rpc.notify("session/cancel", params)
				questions.Close() // Pending permission requests are answered "cancelled".
				go func() {
					select {
					case <-time.After(cancelWait):
						log.Printf("[ACP_CLI] session=%s agent=%s action=killed reason=cancel_timeout", s.rpc.owner, s.agent.ID)
						s.rpc.finish(errors.New("agent did not stop in time"))
					case <-s.rpc.done:
					}
				}()
			}
			ctx = context.Background()
		case <-s.rpc.done:
			if cancelled {
				return nil, context.Canceled
			}
			return nil, s.rpc.failure()
		}
	}
}

func (s *session) handleUpdate(u sessionUpdate, meta map[string]any, tools map[string]*toolCall, text *strings.Builder, emit func(llmtypes.StreamChunk)) {
	switch u.Kind {
	case "agent_message_chunk", "agent_thought_chunk":
		var content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &content) != nil || content.Type != "text" || content.Text == "" {
			return
		}
		kind := llmtypes.StreamChunkTypeContent
		if u.Kind == "agent_thought_chunk" {
			kind = llmtypes.StreamChunkTypeReasoning
		} else {
			text.WriteString(content.Text)
		}
		chunkMeta := map[string]any{llmtypes.ContentDeltaMetadataKey: true}
		for k, v := range meta {
			chunkMeta[k] = v
		}
		emit(llmtypes.StreamChunk{Type: kind, Content: content.Text, Metadata: chunkMeta})
	case "tool_call", "tool_call_update":
		t := tools[u.ToolCallID]
		if t == nil {
			t = &toolCall{started: time.Now()}
			tools[u.ToolCallID] = t
		}
		if u.Title != "" {
			t.name = u.Title
		} else if t.name == "" && u.ToolKind != "" {
			t.name = u.ToolKind
		}
		if len(u.RawInput) > 0 && string(u.RawInput) != "{}" && string(u.RawInput) != "null" {
			t.args = string(u.RawInput)
		}
		if !t.open {
			t.open = true
			if t.args == "" {
				t.args = "{}"
			}
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolCallID: u.ToolCallID, ToolName: t.name, ToolArgs: t.args, Metadata: meta})
		}
		if summary := toolContentText(u.Content); summary != "" {
			t.result = summary
		} else if len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
			t.result = string(u.RawOutput)
		}
		if !t.ended && (u.Status == "completed" || u.Status == "failed") {
			t.ended = true
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: u.ToolCallID, ToolName: t.name, ToolArgs: t.args, ToolResult: t.result, ToolDuration: time.Since(t.started), Metadata: meta})
		}
	case "plan":
		raw, _ := json.Marshal(map[string]json.RawMessage{"entries": u.Entries})
		if plan, ok := llmtypes.ParseCodingAgentPlan(raw, "entries"); ok {
			plan.Provider, plan.PlanID = s.agent.providerID(), s.currentSessionID()
			emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: plan, Metadata: meta})
		}
	case "usage_update":
		if u.Used == nil || *u.Used < 0 {
			return
		}
		s.mu.Lock()
		status := &llmtypes.StatusLine{Provider: s.agent.providerID(), Model: s.handleModel(), Metadata: map[string]interface{}{"acp_agent": s.agent.ID, s.agent.metaKey("session_id"): s.sessionID}}
		s.mu.Unlock()
		size := 0
		if u.Size != nil && *u.Size > 0 {
			size = *u.Size
		}
		status.SetContextUsage(*u.Used, size)
		if *u.Used == 0 {
			status.Metadata[llmtypes.ContextUsedTokensMetaKey] = 0
			if size > 0 {
				status.Metadata[llmtypes.ContextWindowTokensMetaKey] = size
			}
		}
		s.mu.Lock()
		s.status = status
		s.mu.Unlock()
		emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeStatusLine, StatusLine: status, Metadata: meta})
	case "config_option_update":
		if u.ConfigOptions != nil {
			s.mu.Lock()
			s.options = u.ConfigOptions
			s.mu.Unlock()
		}
	default:
		// user_message_chunk, available_commands_update, current_mode_update,
		// session_info_update and agent extensions carry nothing for the turn.
	}
}

// toolContentText summarizes a tool call's content blocks: text as is, a
// diff as its path and changed line counts, a terminal by its id.
func toolContentText(raw json.RawMessage) string {
	var blocks []struct {
		Type    string `json:"type"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Path       string  `json:"path"`
		OldText    *string `json:"oldText"`
		NewText    string  `json:"newText"`
		TerminalID string  `json:"terminalId"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var out []string
	for _, b := range blocks {
		switch b.Type {
		case "content":
			if b.Content.Text != "" {
				out = append(out, b.Content.Text)
			}
		case "diff":
			old := ""
			if b.OldText != nil {
				old = *b.OldText
			}
			verb := "edited"
			if b.OldText == nil {
				verb = "created"
			}
			out = append(out, fmt.Sprintf("%s %s (+%d -%d lines)", verb, b.Path, lineCount(b.NewText), lineCount(old)))
		case "terminal":
			out = append(out, "terminal "+b.TerminalID)
		}
	}
	return strings.Join(out, "\n")
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// stopFailure maps a stop reason that is not a finished answer to a clear error.
func (s *session) stopFailure(reason string) error {
	switch reason {
	case "", "end_turn":
		return nil
	case "max_tokens", "max_turn_requests":
		return &llmerrors.Error{Provider: s.agent.providerID(), Model: s.agent.ID, Kind: llmerrors.KindInvalidRequest,
			Err: fmt.Errorf("%s stopped this turn: it reached its output or step limit.", s.agent.Label)}
	case "refusal":
		return &llmerrors.Error{Provider: s.agent.providerID(), Model: s.agent.ID, Kind: llmerrors.KindUnknown,
			Err: fmt.Errorf("%s refused to continue this turn.", s.agent.Label)}
	default:
		log.Printf("[ACP_CLI] session=%s agent=%s action=unknown_stop_reason reason=%s", s.rpc.owner, s.agent.ID, reason)
		return nil
	}
}

func (s *session) response(text string, usage *llmtypes.Usage) *llmtypes.ContentResponse {
	s.mu.Lock()
	sessionID, model, status := s.sessionID, s.handleModel(), s.status
	s.mu.Unlock()
	gi := &llmtypes.GenerationInfo{Additional: map[string]interface{}{"provider": s.agent.providerID(), "acp_agent": s.agent.ID, s.agent.metaKey("session_id"): sessionID}}
	for k, v := range s.agent.ResponseMeta {
		gi.Additional[k] = v
	}
	if usage != nil {
		prompt := usage.InputTokens
		gi.PromptTokens, gi.CompletionTokens, gi.TotalTokens = &prompt, &usage.OutputTokens, &usage.TotalTokens
		gi.CachedContentTokens = usage.CacheTokens
		gi.ReasoningTokens = usage.ReasoningTokens
	}
	if status != nil {
		for _, key := range []string{llmtypes.ContextUsedTokensMetaKey, llmtypes.ContextWindowTokensMetaKey} {
			if value, ok := status.Metadata[key]; ok {
				gi.Additional[key] = value
			}
		}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider: s.agent.providerID(), Transport: llmtypes.CodingProviderTransportACP, NativeSessionID: sessionID,
		WorkingDir: s.workdir, Model: model, Status: llmtypes.CodingProviderSessionStatusIdle,
	})
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: text, StopReason: "stop", GenerationInfo: gi}}, Usage: usage}
}
