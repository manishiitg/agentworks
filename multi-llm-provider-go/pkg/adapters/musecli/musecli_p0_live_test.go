package musecli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live P0 certification turns for muse-cli. All tests in this file run
// against the real CLI with Meta auth (stored `muse login`) on the
// contributor tier, gated by -coding-cli-p0-live. Budgets are generous on
// purpose: a slow model must still certify, and a timeout fails loudly with
// the latest pane/log attached rather than passing vacuously.

func requireMetaMuseCLIE2E(t *testing.T) {
	t.Helper()
	requireRealMuseCLIE2E(t)
	if os.Getenv(EnvMuseCLIExecProvider) != "" {
		t.Skip("live Meta certs need the real provider, not " + os.Getenv(EnvMuseCLIExecProvider))
	}
}

// A long native shell task may outlive its foreground answer. Muse can record
// post-terminal task rows without an explicit task_backgrounded marker.
func TestMuseCLIRealBackgroundTaskAfterRunTerminal(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	prompt := "Use a shell command to run `sleep 25; echo BACKGROUND_DONE > task-result.txt` in this directory. The command may continue in the background; tell me as soon as it has started. Do not wait for task-result.txt before replying."
	turnStart := time.Now()
	resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
		WithTmuxTransport(true), WithWorkingDir(workdir), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatal(err)
	}
	handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if handle == nil || handle.NativeSessionID == "" {
		t.Fatal("missing native session handle")
	}
	path := museSessionLogPath(handle.NativeSessionID)
	runID, acceptedSeq, ok := museAcceptedIntentSince(path, turnStart, promptSnippet(prompt))
	if !ok || runID == "" {
		t.Fatal("accepted run missing from native journal")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var terminalSeq int64
	for _, line := range strings.Split(string(raw), "\n") {
		var row struct {
			Sequence    int64  `json:"sequence"`
			PayloadType string `json:"payload_type"`
			Payload     struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
				Event struct {
					Kind     string `json:"kind"`
					Terminal string `json:"terminal"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.PayloadType == "runtime.session" && row.Payload.Kind == "run" && row.Payload.RunID == runID && row.Payload.Event.Kind == "terminal" && row.Payload.Event.Terminal == "completed" {
			terminalSeq = row.Sequence
		}
	}
	if terminalSeq <= acceptedSeq {
		t.Fatalf("current run has no completed terminal: run=%s", runID)
	}
	reader := NewBackgroundTaskReader(handle.NativeSessionID, 0)
	var later []BackgroundTaskEvent
	completed := false
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := reader.Poll()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.RunID == runID && row.Sequence > terminalSeq {
				later = append(later, row)
				if row.Kind == "completed" {
					completed = true
				}
			}
		}
		if completed {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !completed {
		t.Fatalf("no completed task event after native foreground terminal: run=%s terminal=%d late=%+v", runID, terminalSeq, later)
	}
}

// museLiveAdapter returns an adapter on CLI defaults: empty modelID means
// no --model flag, so the CLI resolves muse-spark-1.3-contributor itself
// (single model everywhere; the constructor's second arg is the model id,
// not the provider name).
func museLiveAdapter() *MuseCLIAdapter {
	return NewMuseCLIAdapter("", "", museTestLogger{})
}

// museDrainStream non-blockingly collects everything buffered on ch.
func museDrainStream(ch chan llmtypes.StreamChunk) []llmtypes.StreamChunk {
	var out []llmtypes.StreamChunk
	for {
		select {
		case c := <-ch:
			out = append(out, c)
		default:
			return out
		}
	}
}

// museTranscriptFinal returns the last assistant text for a native session,
// failing the test when the sidecar is missing or empty.
func museTranscriptFinal(t *testing.T, nativeSessionID string) string {
	t.Helper()
	path := museSessionLogPath(nativeSessionID)
	if path == "" {
		t.Fatalf("no session log for native session %s", nativeSessionID)
	}
	transcript, ok := readMuseTranscriptMessages(path, "")
	if !ok {
		t.Fatalf("unreadable session log at %s", path)
	}
	final := museLastAssistantText(transcript)
	if strings.TrimSpace(final) == "" {
		t.Fatalf("no assistant text in %s", path)
	}
	return final
}

// TestMuseCLIRealExecTurnContract is the exec-lane P0: one Meta turn through
// the adapter proves completion detection (clean return, no error), final
// extraction (returned text == sidecar transcript's last assistant text),
// and the tokens/costs path (usage + shadow estimate attached live).
func TestMuseCLIRealExecTurnContract(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	streamChan := make(chan llmtypes.StreamChunk, 256)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Do not use any tools. Answer directly with exactly what is asked."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Reply with exactly: PINEAPPLE-EXEC"}}},
	}, llmtypes.WithReasoningEffort("low"), llmtypes.WithStreamingChan(streamChan), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	final := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(final, "PINEAPPLE-EXEC") {
		t.Fatalf("final = %q, want the PINEAPPLE-EXEC marker", final)
	}
	gi := resp.Choices[0].GenerationInfo
	if gi == nil || gi.CodingProviderSessionHandle == nil {
		t.Fatal("missing session handle on live turn")
	}
	sidecar := museTranscriptFinal(t, gi.CodingProviderSessionHandle.NativeSessionID)
	if !strings.Contains(sidecar, "PINEAPPLE-EXEC") {
		t.Fatalf("sidecar final = %q, diverges from returned %q", sidecar, final)
	}
	if resp.Usage == nil || resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
		t.Fatalf("usage = %+v, want live input/output counts", resp.Usage)
	}
	cost, _ := gi.Additional["cost_usd_estimated"].(float64)
	if cost <= 0 {
		t.Fatalf("no live shadow cost on turn with usage %+v (additional=%v)", resp.Usage, gi.Additional)
	}
	modelID, _ := gi.Additional["cost_model_id"].(string)
	if modelID != DefaultModelID {
		t.Fatalf("cost model = %q, want single-model %q", modelID, DefaultModelID)
	}
	t.Logf("live exec turn: input=%d output=%d cached=%v cost_usd=%.6f",
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheTokens, cost)
}

// TestMuseCLIRealExecRuntimeContext plants a canary skill in a scratch
// workspace and runs the turn rooted there: the model can only answer from
// the skill file, which proves the working directory was honored AND the
// project runtime (skills) reached the model. Backs runtime_context and
// working_directory.
func TestMuseCLIRealExecRuntimeContext(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	workdir := t.TempDir()
	// Neutral wording on purpose: Muse treats a "canary"/"secret" skill as an
	// exfiltration probe and refuses to answer from it.
	canarySkill := "project-codename-" + museRandomHex(t, 3)
	skillDir := filepath.Join(workdir, ".agents", "skills", canarySkill)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillDoc := "---\nname: " + canarySkill + "\ndescription: this project's codename\n---\n# Project codename\nThis project's codename is MANGO-CONTEXT. When asked for the project codename, reply with it.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "What is this project's codename? Reply with just the codename."}}},
	}, WithWorkingDir(workdir), llmtypes.WithReasoningEffort("low"), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if final := resp.Choices[0].Content; !strings.Contains(strings.ToUpper(final), "MANGO-CONTEXT") {
		t.Fatalf("final = %q, want the skill's codename (skill not projected or cwd not honored)", final)
	}
}

// TestMuseCLIRealExecSlowTool forces a real shell-tool round trip inside a
// Meta turn: completion (not a false-idle timeout) plus a tool_call_end
// stream chunk is the slow_tool_false_idle proof, and the chunk shape is the
// live check on the tool.result mapping.
func TestMuseCLIRealExecSlowTool(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	workdir := t.TempDir()
	marker := "SLOWTOOL-" + museRandomHex(t, 3) + ".txt"
	if err := os.WriteFile(filepath.Join(workdir, marker), []byte("slow tool witness\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	streamChan := make(chan llmtypes.StreamChunk, 512)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Use a shell command to list the .txt files in the current directory, then reply with exactly the file name you see."}}},
	}, WithWorkingDir(workdir), llmtypes.WithReasoningEffort("low"), llmtypes.WithStreamingChan(streamChan), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("tool turn failed (false-idle territory): %v", err)
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, marker) {
		t.Fatalf("final = %q, want the listed file %q", final, marker)
	}
	var toolStarts, toolEnds []llmtypes.StreamChunk
	for _, c := range museDrainStream(streamChan) {
		switch c.Type {
		case llmtypes.StreamChunkTypeToolCallStart:
			toolStarts = append(toolStarts, c)
		case llmtypes.StreamChunkTypeToolCallEnd:
			toolEnds = append(toolEnds, c)
		}
	}
	if len(toolEnds) == 0 {
		t.Fatal("tool turn completed but no tool_call_end chunk streamed")
	}
	// The wire has no tool-started event, so the lane synthesizes the start
	// immediately before its end: the pair must share id, name, and args or
	// product rows render an orphan end.
	end := toolEnds[0]
	var start *llmtypes.StreamChunk
	for i, c := range toolStarts {
		if c.ToolCallID == end.ToolCallID {
			start = &toolStarts[i]
			break
		}
	}
	if start == nil {
		t.Fatalf("tool_call_end %q streamed with no matching synthetic start", end.ToolCallID)
	}
	if start.ToolName != end.ToolName || start.ToolArgs != end.ToolArgs {
		t.Fatalf("pair mismatch: start (%q, %q) vs end (%q, %q)", start.ToolName, start.ToolArgs, end.ToolName, end.ToolArgs)
	}
	t.Logf("tool_call pair: tool=%q call=%q args=%q", end.ToolName, end.ToolCallID, end.ToolArgs)
}

// TestMuseCLIRealTmuxToolRunTerminal is the interactive slow_tool_false_idle
// proof. A model step may finish with tool_calls, and task rows may complete,
// but the adapter must wait for the same run's terminal event and return the
// answer produced after the tool result.
func TestMuseCLIRealTmuxToolRunTerminal(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	token := "TOOLRUN-" + museRandomHex(t, 4)
	if err := os.WriteFile(filepath.Join(workdir, "marker.txt"), []byte(token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := museLiveBootTUI(t, ctx, workdir)
	prompt := "Use a shell command to sleep 8 seconds and then print the contents of marker.txt in the current directory. Reply with exactly the printed text and nothing else."
	turnStart := time.Now()
	if err := museSendPrompt(ctx, session, prompt); err != nil {
		t.Fatal(err)
	}
	_, path, err := museWaitIntake(ctx, session, turnStart, promptSnippet(prompt), workdir)
	if err != nil {
		t.Fatal(err)
	}
	runID, acceptedSeq, ok := museAcceptedIntentSince(path, turnStart, promptSnippet(prompt))
	if !ok || runID == "" {
		t.Fatal("accepted tool turn has no run ID")
	}
	if _, err := museWaitTurnTerminal(ctx, session, path, runID, acceptedSeq, 6*time.Minute); err != nil {
		t.Fatalf("tool run did not complete: %v", err)
	}
	transcript, ok := readMuseTranscriptMessages(path, runID)
	if !ok || strings.TrimSpace(museLastAssistantText(transcript)) != token {
		t.Fatalf("current run answer = %q, want %q", museLastAssistantText(transcript), token)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var toolCallsSeq, toolResultsSeq, terminalSeq int64
	for _, line := range strings.Split(string(raw), "\n") {
		var row struct {
			Sequence    int64  `json:"sequence"`
			PayloadType string `json:"payload_type"`
			Payload     struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
				Event struct {
					Kind     string `json:"kind"`
					Terminal string `json:"terminal"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.PayloadType != "runtime.session" ||
			row.Payload.Kind != "run" || row.Payload.RunID != runID {
			continue
		}
		switch row.Payload.Event.Kind {
		case "assistant_tool_calls_committed":
			toolCallsSeq = row.Sequence
		case "tool_result_batch_committed":
			toolResultsSeq = row.Sequence
		case "terminal":
			if row.Payload.Event.Terminal == "completed" {
				terminalSeq = row.Sequence
			}
		}
	}
	if toolCallsSeq <= acceptedSeq || toolResultsSeq <= toolCallsSeq || terminalSeq <= toolResultsSeq {
		t.Fatalf("run sequence accepted=%d tool_calls=%d tool_results=%d terminal=%d", acceptedSeq, toolCallsSeq, toolResultsSeq, terminalSeq)
	}
}

// museLiveBootTUI launches a Meta TUI in workdir and waits for the settled
// idle pane. The session is killed on test cleanup.
func museLiveBootTUI(t *testing.T, ctx context.Context, workdir string) string {
	t.Helper()
	session := "mlp-muse-live-" + museRandomHex(t, 3)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})
	cleanup, err := museLaunchTUI(ctx, workdir, session, "meta", "", nil)
	if err != nil {
		t.Fatalf("launch TUI: %v", err)
	}
	t.Cleanup(cleanup)
	if _, err := museWaitSettled(ctx, session, 90*time.Second); err != nil {
		t.Fatalf("TUI never settled: %v", err)
	}
	return session
}

// museLiveSubmitTurn sends one prompt, verifies native intake, then waits for
// that run's terminal event. The pane is returned only as presentation data.
func museLiveSubmitTurn(t *testing.T, ctx context.Context, session, prompt string) (pane, logPath string) {
	t.Helper()
	turnStart := time.Now()
	if err := museSendPrompt(ctx, session, prompt); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	_, path, err := museWaitIntake(ctx, session, turnStart, promptSnippet(prompt), "")
	if err != nil {
		t.Fatalf("prompt never taken in: %v", err)
	}
	runID, acceptedSeq, ok := museAcceptedIntentSince(path, turnStart, promptSnippet(prompt))
	if !ok || runID == "" {
		t.Fatal("accepted turn has no run ID")
	}
	after, err := museWaitTurnTerminal(ctx, session, path, runID, acceptedSeq, 5*time.Minute)
	if err != nil {
		t.Fatalf("turn never completed: %v", err)
	}
	return after, path
}

func museLiveLogText(t *testing.T, logPath string) string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	return string(raw)
}

// TestMuseCLIRealPersistentSession proves the pooled lane the orchestrator
// uses for terminal attach: launch-only boots one TUI and hands back its
// tmux name, two turns run on that SAME live session (same tmux name, same
// native session), and KillMusePersistentSession tears it down. Backs the
// builder-tmux work (no new cert: continuity itself is covered by the
// multi-turn cert; this pins ownership).
func TestMuseCLIRealPersistentSession(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	owner := "mlp-persist-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	// One workdir for the whole test: the pool relaunches when it moves,
	// so rotating temp dirs would defeat the reuse assertion below.
	workdir := t.TempDir()
	persistent := func(o ...llmtypes.CallOption) []llmtypes.CallOption {
		return append([]llmtypes.CallOption{
			WithPersistentInteractiveSession(true),
			WithInteractiveSessionID(owner),
			WithWorkingDir(workdir),
			llmtypes.WithReasoningEffort("low"),
		}, o...)
	}

	launch, err := adapter.GenerateContent(ctx, nil,
		append(persistent(), llmtypes.WithCodingProviderLaunchOnly())...)
	if err != nil {
		t.Fatalf("launch-only: %v", err)
	}
	tmuxName := launch.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession
	if tmuxName == "" {
		t.Fatal("launch-only returned no tmux session to attach the terminal to")
	}
	if !museTmuxSessionAlive(ctx, tmuxName) {
		t.Fatalf("pooled TUI %q not alive after launch", tmuxName)
	}

	turn := func(word string) (string, string) {
		t.Helper()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Reply with exactly " + word + " and nothing else."}}},
		}, persistent()...)
		if err != nil {
			t.Fatalf("persistent turn: %v", err)
		}
		h := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
		if h.TmuxSession != tmuxName {
			t.Fatalf("turn ran on %q, want pooled %q", h.TmuxSession, tmuxName)
		}
		if !strings.Contains(resp.Choices[0].Content, word) {
			t.Fatalf("answer = %q, want %q", resp.Choices[0].Content, word)
		}
		return h.NativeSessionID, resp.Choices[0].Content
	}
	token1 := "PERS1-" + museRandomHex(t, 3)
	native1, _ := turn(token1)
	token2 := "PERS2-" + museRandomHex(t, 3)
	native2, _ := turn(token2)
	if native1 == "" || native1 != native2 {
		t.Fatalf("native sessions %q vs %q: pooled turns must share one native session", native1, native2)
	}
	KillMusePersistentSession(owner)
	if museTmuxSessionAlive(ctx, tmuxName) {
		t.Fatalf("pooled TUI %q still alive after kill", tmuxName)
	}
}

// TestMuseCLIRealTmuxMultiTurn drives two turns through ONE TUI session:
// per-turn completion detection (matching run terminal event), continuity
// (both answers in one native transcript), and transcript final extraction.
// Backs multi_turn and done_detection.
func TestMuseCLIRealTmuxMultiTurn(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session := museLiveBootTUI(t, ctx, t.TempDir())

	token1 := "MULTI1-" + museRandomHex(t, 3)
	_, log1 := museLiveSubmitTurn(t, ctx, session, "Reply with exactly "+token1+" and nothing else.")
	token2 := "MULTI2-" + museRandomHex(t, 3)
	after2, log2 := museLiveSubmitTurn(t, ctx, session, "Reply with exactly "+token2+" and nothing else.")
	if log1 != log2 {
		t.Fatalf("turns landed in different native sessions: %s vs %s (no continuity)", log1, log2)
	}
	logText := museLiveLogText(t, log1)
	if !strings.Contains(logText, token1) || !strings.Contains(logText, token2) {
		t.Fatal("native transcript does not hold both turns' prompts")
	}
	transcript, ok := readMuseTranscriptMessages(log1, "")
	if !ok {
		t.Fatal("unreadable native transcript")
	}
	if final := museLastAssistantText(transcript); !strings.Contains(final, token2) {
		t.Fatalf("last assistant text = %q, want turn 2 answer %q", final, token2)
	}
	if !museTUIAtPrompt(after2) {
		t.Fatal("post-turn-2 pane not settled idle")
	}
}

// TestMuseCLIRealTmuxReplyFidelity runs the formatting prompt through one
// bounded TUI turn and judges the transcript extraction against the wrapped
// pane. Backs reply_formatting_fidelity (agent-review sign-off included).
func TestMuseCLIRealTmuxReplyFidelity(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session := museLiveBootTUI(t, ctx, t.TempDir())

	// Single line on purpose: the TUI submits on pasted newlines, so a
	// multi-line prompt fragments into several turns (proven live
	// 2026-09-10: the 120-char cross-newline snippet never matched intake).
	// The structure under test is what the MODEL must produce, not what we
	// paste in.
	//
	// The 300-char minimum is load-bearing for the agent-review
	// fingerprint, not decoration: the pane buffer keeps boot-width (200)
	// chrome lines, so the authored lines must clear 200 for the
	// unwrapped bit to hold run after run. Narrowing the pane to 80 makes
	// the deterministic wrap comparison decisive on top of that.
	token := "FMT_" + museRandomHex(t, 5)
	if err := exec.CommandContext(ctx, "tmux", "resize-window", "-t", session, "-x", "80", "-y", "24").Run(); err != nil {
		t.Fatalf("resize pane to 80 cols: %v", err)
	}
	time.Sleep(2 * time.Second)
	prompt := "TEXT FORMATTING test " + token + ", no tools. Output ONLY the markdown described next, no preamble, no closing. " +
		"First: one paragraph about " + token + " as a single line of at least 300 characters so a narrow terminal must wrap it. " +
		"Then exactly one blank line. " +
		"Then a second paragraph about " + token + ", again one single line of at least 300 characters. " +
		"Then three lines, each starting with '- ', three bullets about " + token + ", nothing after."
	_, logPath := museLiveSubmitTurn(t, ctx, session, prompt)
	// Drop scrollback (boot-width chrome) so the captured screen is the
	// 80-col turn rendering, then re-capture the settled visible pane.
	if err := exec.CommandContext(ctx, "tmux", "clear-history", "-t", session).Run(); err != nil {
		t.Fatalf("clear scrollback: %v", err)
	}
	pane, err := museTmuxCapturePane(ctx, session)
	if err != nil {
		t.Fatalf("re-capture pane: %v", err)
	}
	if !museTUIAtPrompt(pane) {
		t.Fatalf("pane not settled after scrollback clear:\n%s", pane)
	}
	transcript, ok := readMuseTranscriptMessages(logPath, "")
	if !ok {
		t.Fatal("unreadable native transcript")
	}
	content := strings.TrimSpace(museLastAssistantText(transcript))
	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "muse-cli",
		TmuxScreen:     pane,
		Extracted:      content,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "Return the two long paragraphs and the three-item list with their markdown structure intact.",
		ExpectedNote: "Live muse TUI turn. The pane capture is expected to be hard-wrapped; the extracted answer must NOT be, " +
			"because it comes from the session.jsonl transcript via museLastAssistantText rather than from the screen.",
	})
}

// TestMuseCLIRealTmuxLiveInput queues a follow-up while a long turn is
// busy: the follow-up must be accepted mid-turn and answered after it.
// Backs live_input and busy_live_input.
func TestMuseCLIRealTmuxLiveInput(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session := museLiveBootTUI(t, ctx, t.TempDir())

	token := "QUEUED-" + museRandomHex(t, 3)
	essay := "Write a 400-word essay on the history of concrete with extensive detail. Token " + museRandomHex(t, 2) + "."
	essayStart := time.Now()
	if err := museSendPrompt(ctx, session, essay); err != nil {
		t.Fatalf("send long turn: %v", err)
	}
	if _, _, err := museWaitIntake(ctx, session, essayStart, promptSnippet(essay), ""); err != nil {
		t.Fatalf("long turn never taken in: %v", err)
	}
	time.Sleep(8 * time.Second)
	pane, err := museTmuxCapturePane(ctx, session)
	if err != nil {
		t.Fatalf("capture busy pane: %v", err)
	}
	if museTUIAtPrompt(pane) && musePaneStable(ctx, session, pane) {
		t.Log("long turn finished before the 8s queue window; follow-up still exercises live input on an idle pane")
	}
	queueStart := time.Now()
	queuedPrompt := "Reply with exactly " + token + " and nothing else."
	if err := museSendPrompt(ctx, session, queuedPrompt); err != nil {
		t.Fatalf("send queued follow-up: %v", err)
	}
	// Intake matches the accepted intent's text by prefix (71fdb70), the
	// same promptSnippet production passes. A mid-prompt token never matches.
	_, logPath, err := museWaitIntake(ctx, session, queueStart, promptSnippet(queuedPrompt), "")
	if err != nil {
		t.Fatalf("queued follow-up never taken in: %v", err)
	}
	deadline := time.Now().Add(6 * time.Minute)
	for {
		pane, err = museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		if strings.Contains(pane, token) && museTUIAtPrompt(pane) &&
			musePaneStable(ctx, session, pane) && museLogQuietSince(logPath, 5*time.Second) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued follow-up never answered; latest pane:\n%s", pane)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait canceled: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if logText := museLiveLogText(t, logPath); !strings.Contains(logText, token) {
		t.Fatal("native transcript missing the queued follow-up")
	}
}

// TestMuseCLIRealTmuxCancellation interrupts a running turn with Ctrl-C:
// the TUI must report the interrupt, return to idle, and still take the
// next turn. Backs cancellation.
func TestMuseCLIRealTmuxCancellation(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	session := museLiveBootTUI(t, ctx, t.TempDir())

	essayStart := time.Now()
	longPrompt := "Write a 600-word essay on glass manufacturing history with extensive detail."
	if err := museSendPrompt(ctx, session, longPrompt); err != nil {
		t.Fatalf("send long turn: %v", err)
	}
	if _, _, err := museWaitIntake(ctx, session, essayStart, promptSnippet(longPrompt), ""); err != nil {
		t.Fatalf("long turn never taken in: %v", err)
	}
	// Prove the model is mid-answer before interrupting: the "◆" answer
	// marker must be on screen. Interrupting typed-but-unsubmitted input
	// just clears the line (no turn runs, no marker) — that failure mode
	// wasted a 90s poll in the first version of this test.
	streamDeadline := time.Now().Add(90 * time.Second)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		if strings.Contains(pane, "◆") {
			break
		}
		if time.Now().After(streamDeadline) {
			t.Fatalf("long turn never started streaming; latest pane:\n%s", pane)
		}
		time.Sleep(time.Second)
	}
	if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-c").Run(); err != nil {
		t.Fatalf("send Ctrl-C: %v", err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var pane string
	for {
		p, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		pane = p
		if strings.Contains(pane, "Interrupt") && museTUIAtPrompt(pane) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no interrupt marker after Ctrl-C; latest pane:\n%s", pane)
		}
		time.Sleep(time.Second)
	}
	// A Ctrl-C before any model output makes Muse retract the run and
	// restore the prompt into the composer (run_retracted
	// no_output_interrupt_restore). Production clears that draft in
	// musePrepareStoppedPrompt; do the same here or the next prompt is
	// appended to it.
	if draft, ok := museActiveComposerDraft(pane); ok && draft != "" {
		if !strings.HasPrefix(longPrompt, draft) && !strings.HasPrefix(draft, longPrompt) {
			t.Fatalf("unexpected composer draft after interrupt: %q", draft)
		}
		if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "C-u").Run(); err != nil {
			t.Fatalf("clear restored draft: %v", err)
		}
		clearDeadline := time.Now().Add(5 * time.Second)
		for {
			p, err := museTmuxCapturePane(ctx, session)
			if err != nil {
				t.Fatalf("capture pane: %v", err)
			}
			if d, ok := museActiveComposerDraft(p); ok && d == "" {
				break
			}
			if time.Now().After(clearDeadline) {
				t.Fatalf("restored draft not cleared; latest pane:\n%s", p)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	token := "AFTERCANCEL-" + museRandomHex(t, 3)
	after, logPath := museLiveSubmitTurn(t, ctx, session, "Reply with exactly "+token+" and nothing else.")
	if !strings.Contains(museLiveLogText(t, logPath), token) {
		t.Fatal("TUI did not take a fresh turn after cancellation")
	}
	if !museTUIAtPrompt(after) {
		t.Fatal("pane not settled after post-cancel turn")
	}
}

// TestMuseCLIRealPersistentStopThenNewMessage is the application-facing Stop
// contract: Escape interrupts the current reply, cancellation ends its Go
// caller, and a new message under the SAME owner completes. This catches the
// orphaned deterministic tmux name that made every later message fail with
// "duplicate session" after Stop.
func TestMuseCLIRealPersistentStopThenNewMessage(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	owner := "muse-stop-resume-" + museRandomHex(t, 4)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	// Reproduce a canceled prelaunch (or server restart): tmux survived, but
	// this process has no pool entry for the owner's deterministic name.
	tmuxName := musePersistentTmuxName(owner)
	if out, err := exec.CommandContext(t.Context(), "tmux", "new-session", "-d", "-s", tmuxName, "sleep 60").CombinedOutput(); err != nil {
		t.Fatalf("seed untracked Muse tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = museClosePersistentTmux(tmuxName) })
	adapter := museLiveAdapter()
	opts := []llmtypes.CallOption{
		WithPersistentInteractiveSession(true),
		WithInteractiveSessionID(owner),
		WithWorkingDir(t.TempDir()),
	}
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	defer cancelTurn()
	turnDone := make(chan error, 1)
	go func() {
		_, err := adapter.GenerateContent(turnCtx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Write a detailed 1200-word essay on glass manufacturing history. Do not finish early."),
		}, opts...)
		turnDone <- err
	}()
	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case err := <-turnDone:
			t.Fatalf("first turn ended before Stop could interrupt it: %v", err)
		default:
		}
		if museTmuxSessionAlive(context.Background(), "="+tmuxName) {
			pane, err := museTmuxCapturePane(context.Background(), tmuxName)
			if err == nil && strings.Contains(pane, "◆") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Muse did not start its first reply before Stop")
		}
		time.Sleep(500 * time.Millisecond)
	}
	keyCtx, cancelKey := context.WithTimeout(context.Background(), 10*time.Second)
	if err := SendMuseInteractiveControlKey(keyCtx, owner, "Escape"); err != nil {
		cancelKey()
		t.Fatalf("send Stop Escape: %v", err)
	}
	cancelKey()
	cancelTurn()
	select {
	case err := <-turnDone:
		if err == nil {
			t.Fatal("stopped turn returned as a successful completed answer")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("stopped turn did not release its adapter call")
	}
	if !museTmuxSessionAlive(context.Background(), "="+tmuxName) {
		t.Fatal("Stop destroyed the persistent Muse terminal")
	}
	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancelRetry()
	token := "MUSE_AFTER_STOP_" + museRandomHex(t, 4)
	retry, err := adapter.GenerateContent(retryCtx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly "+token+" and nothing else."),
	}, opts...)
	if err != nil {
		t.Fatalf("new message after Stop: %v", err)
	}
	if len(retry.Choices) == 0 || !strings.Contains(retry.Choices[0].Content, token) {
		t.Fatalf("new message was not answered: %#v", retry.Choices)
	}
	if got := retry.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession; got != tmuxName {
		t.Fatalf("Stop replaced the live pane: got %q want %q", got, tmuxName)
	}
}

// TestMuseCLIRealTmuxParallelIsolation runs two TUI sessions in two
// workdirs at once with distinct prompts: each native transcript must hold
// its own answer and never the other's. Backs parallel_isolation.
func TestMuseCLIRealTmuxParallelIsolation(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	workA, workB := t.TempDir(), t.TempDir()
	sessA := "mlp-muse-par-a-" + museRandomHex(t, 3)
	sessB := "mlp-muse-par-b-" + museRandomHex(t, 3)
	for _, s := range []string{sessA, sessB} {
		s := s
		t.Cleanup(func() {
			_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", s).Run()
		})
	}
	cleanupA, err := museLaunchTUI(ctx, workA, sessA, "meta", "", nil)
	if err != nil {
		t.Fatalf("launch A: %v", err)
	}
	t.Cleanup(cleanupA)
	cleanupB, err := museLaunchTUI(ctx, workB, sessB, "meta", "", nil)
	if err != nil {
		t.Fatalf("launch B: %v", err)
	}
	t.Cleanup(cleanupB)
	if _, err := museWaitSettled(ctx, sessA, 90*time.Second); err != nil {
		t.Fatalf("A never settled: %v", err)
	}
	if _, err := museWaitSettled(ctx, sessB, 90*time.Second); err != nil {
		t.Fatalf("B never settled: %v", err)
	}
	tokenA := "PARA-" + museRandomHex(t, 3)
	tokenB := "PARB-" + museRandomHex(t, 3)
	startA := time.Now()
	promptA := "Reply with exactly " + tokenA + " and nothing else."
	if err := museSendPrompt(ctx, sessA, promptA); err != nil {
		t.Fatalf("send A: %v", err)
	}
	startB := time.Now()
	promptB := "Reply with exactly " + tokenB + " and nothing else."
	if err := museSendPrompt(ctx, sessB, promptB); err != nil {
		t.Fatalf("send B: %v", err)
	}
	_, logA, err := museWaitIntake(ctx, sessA, startA, promptSnippet(promptA), "")
	if err != nil {
		t.Fatalf("A prompt never taken in: %v", err)
	}
	_, logB, err := museWaitIntake(ctx, sessB, startB, promptSnippet(promptB), "")
	if err != nil {
		t.Fatalf("B prompt never taken in: %v", err)
	}
	runA, seqA, ok := museAcceptedIntentSince(logA, startA, promptSnippet(promptA))
	if !ok || runA == "" {
		t.Fatal("A accepted turn has no run ID")
	}
	runB, seqB, ok := museAcceptedIntentSince(logB, startB, promptSnippet(promptB))
	if !ok || runB == "" {
		t.Fatal("B accepted turn has no run ID")
	}
	if _, err := museWaitTurnTerminal(ctx, sessA, logA, runA, seqA, 5*time.Minute); err != nil {
		t.Fatalf("A never completed: %v", err)
	}
	if _, err := museWaitTurnTerminal(ctx, sessB, logB, runB, seqB, 5*time.Minute); err != nil {
		t.Fatalf("B never completed: %v", err)
	}
	if logA == logB {
		t.Fatal("both parallel turns landed in one native session (no isolation)")
	}
	textA, textB := museLiveLogText(t, logA), museLiveLogText(t, logB)
	if !strings.Contains(textA, tokenA) || strings.Contains(textA, tokenB) {
		t.Fatal("session A transcript crossed with B")
	}
	if !strings.Contains(textB, tokenB) || strings.Contains(textB, tokenA) {
		t.Fatal("session B transcript crossed with A")
	}
}

// TestMuseCLIRealTrustGate boots a FRESH workspace without --trust-workspace:
// the trust gate must appear (never a silent hang), accepting it must settle
// the TUI, and the first turn must work with no gate text leaking into the
// answer. Backs trust_auth_prompts.
func TestMuseCLIRealTrustGate(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	session := "mlp-muse-trust-" + museRandomHex(t, 3)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})
	launch := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session,
		"-x", "200", "-y", "50", "-c", workdir, "muse", "--provider", "echo")
	if out, err := launch.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	gateDeadline := time.Now().Add(60 * time.Second)
	var pane string
	for {
		p, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		pane = p
		if musePaneShowsBlockingGate(pane) {
			break
		}
		if museTUISufficientlySettled(pane) {
			t.Fatalf("fresh workspace settled with no trust gate; pane:\n%s", pane)
		}
		if time.Now().After(gateDeadline) {
			t.Fatalf("neither gate nor settled TUI in a fresh workspace; pane:\n%s", pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(strings.ToLower(pane), "trust") {
		t.Fatalf("blocking gate is not the trust prompt; pane:\n%s", pane)
	}
	if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "-l", "1").Run(); err != nil {
		t.Fatalf("send trust choice: %v", err)
	}
	if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Enter").Run(); err != nil {
		t.Fatalf("send Enter: %v", err)
	}
	// The Enter reaches Muse before it redraws; museWaitSettled fails fast on
	// a visible gate (production behavior), so first wait for the trust prompt
	// to leave the pane.
	clearDeadline := time.Now().Add(15 * time.Second)
	for {
		p, err := museTmuxCapturePane(ctx, session)
		if err == nil && !musePaneShowsBlockingGate(p) {
			break
		}
		if time.Now().After(clearDeadline) {
			t.Fatalf("trust prompt still shown 15s after choosing trust; pane:\n%s", p)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := museWaitSettled(ctx, session, 90*time.Second); err != nil {
		t.Fatalf("TUI never settled after trusting: %v", err)
	}
	token := "TRUSTOK-" + museRandomHex(t, 3)
	after, logPath := museLiveSubmitTurn(t, ctx, session, "Reply with exactly "+token+" and nothing else.")
	transcript, ok := readMuseTranscriptMessages(logPath, "")
	if !ok {
		t.Fatal("unreadable native transcript")
	}
	final := museLastAssistantText(transcript)
	if !strings.Contains(final, token) {
		t.Fatalf("first turn after trust = %q, want %q", final, token)
	}
	if strings.Contains(strings.ToLower(final), "do you trust") {
		t.Fatalf("gate text leaked into the answer: %q", final)
	}
	if !museTUIAtPrompt(after) {
		t.Fatal("pane not settled after post-trust turn")
	}
}

// museProbeMCPStub is an in-process MCP Streamable-HTTP server with one
// tool, probe_echo(text) -> "STUB_ECHO:<text>". Shape proven against the
// real CLI 2026-09-10: stateless initialize/initialized/list per connection,
// plain-JSON (non-SSE) responses accepted.
func museProbeMCPStub(called *atomic.Int32) *httptest.Server {
	write := func(w http.ResponseWriter, code int, obj any) {
		body, _ := json.Marshal(obj)
		if obj == nil {
			body = nil
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if body != nil {
			_, _ = w.Write(body)
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			write(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
			return
		}
		var msg struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			write(w, 400, map[string]any{"error": "bad json"})
			return
		}
		switch msg.Method {
		case "initialize":
			res := map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "mlp-probe-stub", "version": "0.1"}}
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": res})
		case "notifications/initialized":
			write(w, 202, nil)
		case "tools/list":
			tools := []any{map[string]any{
				"name":        "probe_echo",
				"description": "Echo back the caller's text with a STUB_ECHO prefix. Call this when asked to run the probe echo.",
				"inputSchema": map[string]any{"type": "object",
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
					"required":   []any{"text"}},
			}}
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": tools}})
		case "tools/call":
			text, _ := msg.Params["arguments"].(map[string]any)["text"].(string)
			called.Add(1)
			content := []any{map[string]any{"type": "text", "text": "STUB_ECHO:" + text}}
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"content": content}})
		default:
			write(w, 200, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
		}
	}))
}

// TestMuseCLIRealMCPBridge mounts the in-process stub through the real
// settings merge and runs a Meta turn that must call it: the stub must see
// the call, the wire must carry the tool result, and the final answer must
// be the tool's output. The merge is restored afterwards — verified, not
// trusted. Backs mcp_bridge.
func TestMuseCLIRealMCPBridge(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	settingsPath, err := museSettingsPath()
	if err != nil {
		t.Fatalf("settings path: %v", err)
	}
	before, _ := os.ReadFile(settingsPath)
	// Byte-exact: the merge helper snapshots and rewrites the pre-run bytes
	// verbatim, so anything else means a leak or a clobber.
	t.Cleanup(func() {
		after, _ := os.ReadFile(settingsPath)
		if string(after) != string(before) {
			t.Errorf("settings.json not restored byte-exact after mounted run:\nbefore: %s\nafter: %s", before, after)
		}
	})

	var called atomic.Int32
	stub := museProbeMCPStub(&called)
	defer stub.Close()

	token := "BRIDGE-" + museRandomHex(t, 3)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	streamChan := make(chan llmtypes.StreamChunk, 512)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "You have an MCP tool called probe_echo. Call it with text " + token + ", then reply with ONLY the tool's raw output."}}},
	}, WithMCPConfig(`{"mcpServers": {"probe-stub": {"url": "`+stub.URL+`/mcp"}}}`),
		WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low"),
		llmtypes.WithStreamingChan(streamChan), WithMuseStructuredTransport(true))
	if err != nil {
		t.Fatalf("bridge turn: %v", err)
	}
	t.Logf("bridge turn final: %q", resp.Choices[0].Content)
	if called.Load() == 0 {
		t.Fatal("stub never saw tools/call (mount did not reach the model)")
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, "STUB_ECHO:"+token) {
		t.Fatalf("final = %q, want the stub tool output", final)
	}
	var toolEnds []llmtypes.StreamChunk
	for _, c := range museDrainStream(streamChan) {
		if c.Type == llmtypes.StreamChunkTypeToolCallEnd && strings.Contains(c.ToolName, "probe_echo") {
			toolEnds = append(toolEnds, c)
		}
	}
	if len(toolEnds) == 0 {
		t.Fatal("bridge tool ran but no probe_echo tool_call_end chunk streamed")
	}
	t.Logf("bridge tool_call_end: tool=%q result=%q", toolEnds[0].ToolName, toolEnds[0].ToolResult)
}

// TestMuseCLIRealLargePromptDraftDelivery pins large-payload injection
// against the real TUI (regression: builder-scale prompts failed with
// tmux "command too long" when sent as one send-keys argument). Launch-only,
// chunked-typed, and buffer-pasted drafts must all land visibly. No Enter is
// sent, so no model turn runs; cleanup kills the session.
func TestMuseCLIRealLargePromptDraftDelivery(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	owner := "mlp-draft-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	launch, err := adapter.GenerateContent(ctx, nil, []llmtypes.CallOption{
		WithPersistentInteractiveSession(true),
		WithInteractiveSessionID(owner),
		WithWorkingDir(t.TempDir()),
		llmtypes.WithCodingProviderLaunchOnly(),
	}...)
	if err != nil {
		t.Fatalf("launch-only: %v", err)
	}
	tmuxName := launch.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession
	if tmuxName == "" {
		t.Fatal("launch-only returned no tmux session")
	}

	chunkMarker := "CHUNKMARK-" + museRandomHex(t, 4)
	if err := writeMuseVisibleDraftToTmux(ctx, tmuxName, "first line\n"+chunkMarker+"\nthird line"); err != nil {
		t.Fatalf("chunked draft: %v", err)
	}
	time.Sleep(2 * time.Second)
	pane, err := museTmuxCapturePane(ctx, tmuxName)
	if err != nil {
		t.Fatalf("capture after chunked draft: %v", err)
	}
	if !strings.Contains(pane, chunkMarker) {
		t.Fatalf("chunked draft marker %q not visible in pane", chunkMarker)
	}

	pasteMarker := "PASTEMARK-" + museRandomHex(t, 4)
	big := strings.Repeat("pasted filler line for volume. ", 100) + "\n" + pasteMarker + "\n" + strings.Repeat("trailing pasted filler. ", 40)
	if !musePromptNeedsAtomicPaste(big) {
		t.Fatal("draft prompt should route to atomic paste")
	}
	if err := pasteMuseDraftToTmux(ctx, tmuxName, big); err != nil {
		t.Fatalf("atomic paste: %v", err)
	}
	time.Sleep(2 * time.Second)
	pane2, err := museTmuxCapturePane(ctx, tmuxName)
	if err != nil {
		t.Fatalf("capture after paste: %v", err)
	}
	if n := strings.Count(pane2, pasteMarker); n != 1 {
		t.Fatalf("pasted marker %q appears %d times, want exactly once", pasteMarker, n)
	}
}

// TestMuseCLIRealInteractiveTmuxShortPromptRoundTrip is the one real,
// full-turn certification the interactive tmux lane was missing: every other
// live test in this file either exercises WithTmuxTransport's plumbing in
// isolation (draft typing alone, settle/gate detection via raw tmux) or goes
// through the exec --json lane (WithMuseStructuredTransport). None combined
// a live GenerateContent call with WithTmuxTransport(true) and let
// museSendPrompt -> museWaitIntake -> museWaitTurnTerminal run together as
// one real turn -- which is exactly what silently swallowed a short prompt's
// keystrokes in production (a bare "hi", well under the atomic-paste
// threshold, submitted an empty input box for the full 60s intake deadline;
// see writeVisibleDraftAndConfirm). A short, deterministic prompt here is
// deliberate: it is the case a large-prompt-only cert would never catch.
func TestMuseCLIRealInteractiveTmuxShortPromptRoundTrip(t *testing.T) {
	requireRealMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "hi, reply with exactly PINEAPPLE-TMUX and nothing else"}}},
	}, WithTmuxTransport(true), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("interactive tmux round trip failed (this is the case that silently ate a short prompt in production): %v", err)
	}
	final := strings.TrimSpace(resp.Choices[0].Content)
	if !strings.Contains(final, "PINEAPPLE-TMUX") {
		t.Fatalf("final = %q, want the PINEAPPLE-TMUX marker", final)
	}
	h := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle
	if h.Provider != "muse-cli" || h.Transport != llmtypes.CodingProviderTransportTmux {
		t.Fatalf("session handle = %+v, want provider=muse-cli transport=tmux", h)
	}
	if h.NativeSessionID == "" {
		t.Fatal("expected a native session id from a completed tmux turn")
	}
}

// Copied table rows carry literal tabs, but Muse paints those tabs as spaces.
// The visible-draft guard must recognize the rendered form and submit instead
// of leaving a correct draft in the composer and returning a false 409.
func TestMuseCLIRealInteractiveTmuxTabularPromptRoundTrip(t *testing.T) {
	requireRealMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	prompt := "AIS\tTDS-393(1)[Table: S.\tBusiness receipts\tUPWORK GLOBAL LLC\t205 — reply with exactly TABULAR-TMUX"
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: prompt}}},
	}, WithTmuxTransport(true), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low"))
	if err != nil {
		t.Fatalf("interactive tabular tmux round trip failed: %v", err)
	}
	if final := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(final, "TABULAR-TMUX") {
		t.Fatalf("final = %q, want the TABULAR-TMUX marker", final)
	}
}

// TestMuseCLIRealProjectInstructionOnly certifies file-only mode end to
// end: the system prompt travels solely via the projected AGENTS.md while
// the typed turn is a bare human message. The marker rule proves the TUI
// read the file (not the typed text, which never contains it).
func TestMuseCLIRealProjectInstructionOnly(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	owner := "mlp-agents-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	marker := "RULEWORD-" + museRandomHex(t, 4)

	systemMsg := llmtypes.MessageContent{
		Role: llmtypes.ChatMessageTypeSystem,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{
			Text: "Whenever asked for the code word, reply with exactly " + marker + " and nothing else.",
		}},
	}
	humanMsg := llmtypes.MessageContent{
		Role: llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{
			Text: "What is the code word?",
		}},
	}
	base := []llmtypes.CallOption{
		WithPersistentInteractiveSession(true),
		WithInteractiveSessionID(owner),
		WithWorkingDir(workdir),
		WithProjectInstructionOnly(true),
		WithRestoreProjectFiles(true),
		llmtypes.WithReasoningEffort("low"),
	}
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{systemMsg, humanMsg}, base...)
	if err != nil {
		t.Fatalf("file-only turn: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("file-only turn returned no choices")
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, marker) {
		t.Fatalf("file-only turn answer missing rule marker %q:\n%s", marker, final)
	}
	// The projected file must live until teardown, then be restored away.
	// Kill explicitly here (idempotent with the deferred cleanup) so the
	// absence assertion below actually covers the restore path.
	KillMusePersistentSession(owner)
	if _, err := os.Stat(filepath.Join(workdir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("projected AGENTS.md leaked past KillMusePersistentSession cleanup")
	}
}

// TestMuseCLIRealBoundedProjectInstructionOnly is the bounded-lane twin of
// the persistent file-only cert: a fresh TUI per turn must still read the
// projected AGENTS.md (projected before boot) and tear it down afterwards.
func TestMuseCLIRealBoundedProjectInstructionOnly(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	marker := "RULEWORD-" + museRandomHex(t, 4)

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{
			Text: "Whenever asked for the code word, reply with exactly " + marker + " and nothing else.",
		}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{
			Text: "What is the code word?",
		}}},
	},
		WithTmuxTransport(true),
		WithWorkingDir(workdir),
		WithProjectInstructionOnly(true),
		llmtypes.WithReasoningEffort("low"),
	)
	if err != nil {
		t.Fatalf("bounded file-only turn: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("bounded file-only turn returned no choices")
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, marker) {
		t.Fatalf("bounded file-only answer missing rule marker %q:\n%s", marker, final)
	}
	// Bounded teardown runs inside GenerateContent: the file must be gone
	// on return, not lingering for the next run in this dir.
	if _, err := os.Stat(filepath.Join(workdir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("projected AGENTS.md leaked past bounded session teardown")
	}
}

// museLiveIntentText returns the accepted-turn text of the native log record
// mentioning humanMarker (the typed input as the TUI took it in).
func museLiveIntentText(t *testing.T, logPath, humanMarker string) string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read native log: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, humanMarker) {
			continue
		}
		var e struct {
			PayloadType string `json:"payload_type"`
			Payload     struct {
				RefillBlocks []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"refill_blocks"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.PayloadType != "runtime.user_intent.accepted" {
			continue
		}
		var sb strings.Builder
		for _, b := range e.Payload.RefillBlocks {
			if b.Kind == "text" {
				sb.WriteString(b.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	t.Fatalf("no accepted intent mentioning %q in %s", humanMarker, logPath)
	return ""
}

// TestMuseCLIRealProjectInstructionChange certifies two file-only behaviors
// units cannot see. First, a changed system prompt takes effect on the live
// pooled session (relaunch, never a stale read): turn 2 obeys rule B, and
// AGENTS.md holds B. Second, the typed turn excludes the preamble: the
// accepted intent record carries the human marker but not the system marker.
func TestMuseCLIRealProjectInstructionChange(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	owner := "mlp-chg-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	base := []llmtypes.CallOption{
		WithPersistentInteractiveSession(true),
		WithInteractiveSessionID(owner),
		WithWorkingDir(workdir),
		WithProjectInstructionOnly(true),
		llmtypes.WithReasoningEffort("low"),
	}
	turn := func(system, human string) string {
		t.Helper()
		resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: system}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: human}}},
		}, base...)
		if err != nil {
			t.Fatalf("file-only turn: %v", err)
		}
		if len(resp.Choices) == 0 {
			t.Fatal("file-only turn returned no choices")
		}
		return resp.Choices[0].Content
	}

	markerA := "RULEWORD-A" + museRandomHex(t, 4)
	if final := turn("Whenever asked for the code word, reply with exactly "+markerA+" and nothing else.", "What is the code word?"); !strings.Contains(final, markerA) {
		t.Fatalf("turn 1 answer missing rule marker %q:\n%s", markerA, final)
	}
	startB := time.Now()
	markerB := "RULEWORD-B" + museRandomHex(t, 4)
	humanB := "What is the code word? PLEASE-" + museRandomHex(t, 4)
	if final := turn("Whenever asked for the code word, reply with exactly "+markerB+" and nothing else.", humanB); !strings.Contains(final, markerB) {
		t.Fatalf("turn 2 answer missing new rule marker %q:\n%s", markerB, final)
	}
	if raw, err := os.ReadFile(filepath.Join(workdir, "AGENTS.md")); err != nil {
		t.Fatalf("read projected AGENTS.md mid-session: %v", err)
	} else if !strings.Contains(string(raw), markerB) {
		t.Fatalf("projected AGENTS.md missing turn-2 marker %q", markerB)
	}

	_, logPath, err := museDiscoverSessionSince(museXDGDataHome(), startB, humanB, "")
	if err != nil {
		t.Fatalf("discover turn-2 log: %v", err)
	}
	intent := museLiveIntentText(t, logPath, humanB)
	if !strings.Contains(intent, humanB) {
		t.Fatalf("accepted intent missing human text:\n%s", intent)
	}
	if strings.Contains(intent, markerB) {
		t.Fatalf("accepted intent carries system marker %q — preamble was typed inline:\n%s", markerB, intent)
	}
}

// TestMuseCLIRealInteractiveTmuxDefaultModeFoldsSystemPrompt closes the gap
// TestMuseCLIRealProjectInstructionOnly leaves: that test only certifies the
// opt-in WithProjectInstructionOnly(true) path. Nothing certified live that
// a system message survives the DEFAULT tmux path (instructionOnly left
// unset) -- the exact path that silently dropped every system message
// before museResolveTmuxPrompt was fixed to default to the inline fold
// instead of the bare human turn. TestMuseResolveTmuxPromptDefaultsToFold
// proves the decision function; this proves the fold actually survives
// typing into a real TUI and produces a rule-compliant reply.
func TestMuseCLIRealInteractiveTmuxDefaultModeFoldsSystemPrompt(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	adapter := museLiveAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	marker := "RULEWORD-" + museRandomHex(t, 4)
	workdir := t.TempDir()

	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "Whenever asked for the code word, reply with exactly " + marker + " and nothing else."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
			llmtypes.TextContent{Text: "What is the code word?"}}},
	}, WithTmuxTransport(true), WithWorkingDir(workdir), llmtypes.WithReasoningEffort("low"))
	// Deliberately no WithProjectInstructionOnly: this is the default path.
	if err != nil {
		t.Fatalf("default-mode tmux turn with a system message failed: %v", err)
	}
	if final := resp.Choices[0].Content; !strings.Contains(final, marker) {
		t.Fatalf("final = %q, want the rule marker %q (system prompt was dropped, not folded)", final, marker)
	}
	// The default path types the fold; AGENTS.md must not exist (that file
	// is exclusive to the opt-in project-instruction-only path).
	if _, err := os.Stat(filepath.Join(workdir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("default mode must not project AGENTS.md")
	}
}
