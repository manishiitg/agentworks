package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/internal/agentreview"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// turnEndCapture records every canonical completion and the first tool end.
type turnEndCapture struct {
	mu          sync.Mutex
	completions []turnEndCompletion
	toolEnded   chan struct{}
	toolOnce    sync.Once
	arrived     chan struct{}
	// onFirstCompletion, when set, runs synchronously inside the first
	// completion's emission -- while the Run that emitted it is still active.
	onFirstCompletion func()
	completionOnce    sync.Once
}

type turnEndCompletion struct {
	at         time.Time
	turnID     string
	final      string
	metadata   map[string]interface{}
	eventTurnI string
}

func (c *turnEndCapture) Name() string { return "turn-end-capture" }

func (c *turnEndCapture) HandleEvent(_ context.Context, event *events.AgentEvent) error {
	switch data := event.Data.(type) {
	case *events.ToolCallEndEvent:
		c.toolOnce.Do(func() { close(c.toolEnded) })
	case *events.UnifiedCompletionEvent:
		meta := map[string]interface{}{}
		for k, v := range data.Metadata {
			meta[k] = v
		}
		c.mu.Lock()
		c.completions = append(c.completions, turnEndCompletion{
			at: time.Now(), turnID: fmt.Sprint(data.Metadata["turn_id"]), final: data.FinalResult,
			metadata: meta, eventTurnI: event.TurnID,
		})
		hook := c.onFirstCompletion
		c.mu.Unlock()
		if hook != nil {
			c.completionOnce.Do(hook)
		}
		select {
		case c.arrived <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *turnEndCapture) snapshot() []turnEndCompletion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]turnEndCompletion(nil), c.completions...)
}

// TestSessionLiveInputAtTurnEndReachesChat reproduces Excellence 2026-09-29
// 06:27 with a real CLI: live input sent through Session.Send while a
// Session.Run is finishing its final answer. The CLI accepts the text and
// answers it only after the running response completes, i.e. after Run has
// returned. Before the fix nothing read the CLI's reply: no stream, no
// completion, and the chat showed nothing. The reply must now arrive as a
// follow-up canonical completion (source mcpagent_session), exactly once, and
// the input must never be sent twice.
//
// OPT-IN with the steer e2e gate (RUN_MCPAGENT_STEER_E2E=1): live-model
// timing. LIVE_INPUT_RACE_PROVIDERS narrows rows (default Claude,Muse).
func TestSessionLiveInputAtTurnEndReachesChat(t *testing.T) {
	if os.Getenv("RUN_MCPAGENT_STEER_E2E") != "1" {
		t.Skip("set RUN_MCPAGENT_STEER_E2E=1 to run the live-model turn-end live-input e2e")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	wanted := os.Getenv("LIVE_INPUT_RACE_PROVIDERS")
	if wanted == "" {
		wanted = "Claude,Muse"
	}
	modes := []string{"FinalStreaming", "AtCompletion"}
	for _, tc := range multiTurnProviderCases {
		tc := tc
		if !strings.Contains(","+wanted+",", ","+tc.name+",") {
			continue
		}
		for _, mode := range modes {
			mode := mode
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				if _, err := exec.LookPath(tc.binary); err != nil {
					t.Skipf("%s CLI required", tc.binary)
				}
				// AtCompletion races a scheduler; retry until one attempt
				// provably delivered into the still-active Run.
				attempts := 1
				if mode == "AtCompletion" {
					attempts = 3
				}
				for attempt := 1; attempt <= attempts; attempt++ {
					if runTurnEndLiveInputAttempt(t, tc, mode, attempt == attempts) {
						return
					}
					t.Logf("[%s/%s] attempt %d sent after the Run had already ended; retrying", tc.name, mode, attempt)
				}
			})
		}
	}
}

// runTurnEndLiveInputAttempt runs one attempt. It returns false (without
// failing) when mode AtCompletion missed the race and last is false.
func runTurnEndLiveInputAttempt(t *testing.T, tc multiTurnProviderCase, mode string, last bool) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	convID := "turnend-" + realBridgeRandHex(4)
	word := "ZEBRA_" + realBridgeRandHex(6)

	agent, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), t.TempDir(), convID, true)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	defer cleanup()
	capture := &turnEndCapture{toolEnded: make(chan struct{}), arrived: make(chan struct{}, 16)}
	session, err := agent.Start(ctx)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer session.Close()

	liveInput := fmt.Sprintf("New request: reply with exactly the word %s and nothing else.", word)
	type sendOutcome struct {
		delivery DeliveryResult
		err      error
		startAt  time.Time
		doneAt   time.Time
	}
	sendDone := make(chan sendOutcome, 1)
	send := func() {
		started := time.Now()
		delivery, serr := session.Send(ctx, liveInput)
		sendDone <- sendOutcome{delivery: delivery, err: serr, startAt: started, doneAt: time.Now()}
	}
	sendNow := make(chan struct{})
	var sendOnce sync.Once
	trigger := func() { sendOnce.Do(func() { close(sendNow) }) }

	var chunkMu sync.Mutex
	var chunks []string
	streaming := func(chunk llmtypes.StreamChunk) {
		if chunk.Type != llmtypes.StreamChunkTypeContent || strings.TrimSpace(chunk.Content) == "" {
			return
		}
		chunkMu.Lock()
		chunks = append(chunks, chunk.Content)
		chunkMu.Unlock()
		if mode == "FinalStreaming" {
			select {
			case <-capture.toolEnded:
				trigger()
			default:
			}
		}
	}
	switch mode {
	case "FinalStreaming":
		// Send the moment the final answer starts streaming after the tool:
		// the CLI is writing its last response, so it queues the input behind
		// the turn's completion. Lanes whose content arrives in one block (or
		// whose tool events are END-only) fall back to a delay after the tool.
		go func() {
			select {
			case <-capture.toolEnded:
			case <-ctx.Done():
				return
			}
			select {
			case <-sendNow:
			case <-time.After(4 * time.Second):
				trigger()
			}
		}()
	case "AtCompletion":
		// Send the moment the Run emits its completion, while the Run is still
		// active: the incident shape, where the CLI accepted the input (the
		// muse durable ack) only after the running turn had settled. The
		// listener lingers briefly like a host persisting the completion.
		capture.onFirstCompletion = func() {
			trigger()
			time.Sleep(30 * time.Millisecond)
		}
	}
	go func() {
		select {
		case <-sendNow:
			send()
		case <-ctx.Done():
		}
	}()
	agent.addEventListener(capture)

	type runOutcome struct {
		result Result
		err    error
		at     time.Time
	}
	runDone := make(chan runOutcome, 1)
	go func() {
		res, rerr := session.Run(ctx, Turn{
			Input: "Run exactly this one shell command and wait for it: sleep 3 && echo ALPHA_DONE. " +
				"Then write a single 150-word paragraph about lighthouses, then stop. Do not ask questions.",
			StreamingCallback: streaming,
		})
		runDone <- runOutcome{result: res, err: rerr, at: time.Now()}
	}()

	var run runOutcome
	select {
	case run = <-runDone:
	case <-time.After(8 * time.Minute):
		t.Fatal("running turn never completed")
	}
	if run.err != nil {
		t.Fatalf("running turn failed: %v", run.err)
	}
	var sent sendOutcome
	select {
	case sent = <-sendDone:
	case <-time.After(3 * time.Minute):
		t.Fatalf("live input was never sent (mode %s)", mode)
	}
	if sent.err != nil {
		t.Fatalf("Send at turn end: %v", sent.err)
	}
	if sent.delivery.Status != UserMessageDeliveryStatusSentToCLI || sent.delivery.Transport != llm.CodingAgentTransportTmux {
		t.Fatalf("delivery = %+v, want sent_to_cli over tmux", sent.delivery)
	}
	runTurnID := run.result.TurnID
	deliveredIntoRun := sent.delivery.TurnID == runTurnID
	if mode == "AtCompletion" && !deliveredIntoRun && !last {
		return false
	}
	if !deliveredIntoRun {
		t.Fatalf("live input was not delivered into the running turn (delivery turn %q, run turn %q)", sent.delivery.TurnID, runTurnID)
	}
	queuedBehindTurn := !strings.Contains(run.result.Text, word)
	t.Logf("[%s/%s] send started %s before run returned, ack %s after run returned; run text contains word=%v", tc.name, mode,
		run.at.Sub(sent.startAt).Round(time.Millisecond), sent.doneAt.Sub(run.at).Round(time.Millisecond), !queuedBehindTurn)

	// Wait for the follow-up: a canonical completion for another turn.
	followupsOf := func(all []turnEndCompletion) []turnEndCompletion {
		var out []turnEndCompletion
		for _, c := range all {
			if c.turnID != runTurnID && c.metadata["source"] == "mcpagent_session" {
				out = append(out, c)
			}
		}
		return out
	}
	deadline := time.After(5 * time.Minute)
	for len(followupsOf(capture.snapshot())) == 0 {
		select {
		case <-capture.arrived:
		case <-deadline:
			t.Fatalf("no follow-up completion after the Run returned: the live input's reply never reached the host (completions=%+v)", capture.snapshot())
		}
	}
	// Duplicates would show up right after the first follow-up.
	time.Sleep(8 * time.Second)
	all := capture.snapshot()
	followups := followupsOf(all)
	var runCompletion *turnEndCompletion
	for i, c := range all {
		if c.turnID == runTurnID {
			runCompletion = &all[i]
		}
	}
	if len(followups) != 1 {
		t.Fatalf("follow-up completions = %d, want exactly one: %+v", len(followups), followups)
	}
	followup := followups[0]
	if mode == "FinalStreaming" && (runCompletion == nil || runCompletion.metadata[LiveInputFollowupMetadataKey] != true) {
		t.Fatalf("run completion must carry %s: %+v", LiveInputFollowupMetadataKey, runCompletion)
	}
	answered := followup.metadata[AnsweredByPreviousResponseMetadataKey] == true
	if queuedBehindTurn {
		if answered || !strings.Contains(followup.final, word) {
			t.Fatalf("the CLI answered the input after the turn, but the follow-up final = %q (answered=%v)", followup.final, answered)
		}
	} else if !answered || followup.final != "" {
		t.Fatalf("input steered the running turn; follow-up must close it without duplicate text, got final=%q answered=%v", followup.final, answered)
	}

	chunkMu.Lock()
	runChunks := len(chunks)
	chunkMu.Unlock()
	runMarked := interface{}(nil)
	if runCompletion != nil {
		runMarked = runCompletion.metadata[LiveInputFollowupMetadataKey]
	}
	rec := agentreview.WriteWithCriteria(t, "TestSessionLiveInputAtTurnEndReachesChat_"+tc.name+"_"+mode,
		tc.name+" ("+mode+"): live input sent into a Session.Run at its end is answered after the Run returns; the reply arrives as exactly one follow-up canonical completion",
		[]string{
			"delivered_into_run=true: the input was sent while the Run was still active (the race)",
			"when queued_behind_turn=true the run's answer lacks the live-input word and the follow-up final text is the CLI's reply (the ZEBRA word), not a repeat of the run's answer",
			"when queued_behind_turn=false the follow-up is answered_by_previous_response with empty final text (no duplicate message)",
			"exactly one follow-up completion; the input was sent once (delivery sent_to_cli)",
		},
		map[string]any{
			"provider":                   tc.name,
			"mode":                       mode,
			"conversation_id":            convID,
			"live_input_word":            word,
			"delivered_into_run":         deliveredIntoRun,
			"send_start_before_run_end":  run.at.Sub(sent.startAt).Milliseconds(),
			"send_ack_after_run_end_ms":  sent.doneAt.Sub(run.at).Milliseconds(),
			"run_text":                   run.result.Text,
			"run_stream_chunks":          runChunks,
			"run_completion_marked":      runMarked,
			"queued_behind_turn":         queuedBehindTurn,
			"followup_final":             followup.final,
			"followup_answered_previous": answered,
			"followup_after_run_ms":      followup.at.Sub(run.at).Milliseconds(),
			"followup_completions":       len(followups),
		},
		map[string]any{"mode": mode, "queued_behind_turn": queuedBehindTurn, "followups": len(followups), "answered": answered, "delivered_into_run": deliveredIntoRun},
	)
	agentreview.RequireReviewed(t, rec)
	return true
}
