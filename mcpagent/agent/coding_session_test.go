package mcpagent

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestDecideDelivery pins the pure steer-vs-query routing truth table — the
// decision the whole abstraction hangs on — with no live session involved.
func TestDecideDelivery(t *testing.T) {
	cases := []struct {
		name             string
		turnInFlight     bool
		supportsSteering bool
		want             deliveryDecision
	}{
		{"idle steerable -> start turn", false, true, decideStartTurn},
		{"idle query-only -> start turn", false, false, decideStartTurn},
		{"busy steerable -> steer", true, true, decideSteer},
		{"busy query-only -> queue", true, false, decideQueue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideDelivery(tc.turnInFlight, tc.supportsSteering); got != tc.want {
				t.Fatalf("decideDelivery(busy=%v, steerable=%v) = %q; want %q", tc.turnInFlight, tc.supportsSteering, got, tc.want)
			}
		})
	}
}

// TestMemoryCodingSessionStore covers the process-local store: cold load, round
// trip, isolation from later mutation, and delete.
func TestMemoryCodingSessionStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCodingSessionStore()

	if h, err := store.Load(ctx, "conv-1"); err != nil || h != nil {
		t.Fatalf("cold Load = (%v, %v); want (nil, nil)", h, err)
	}

	handle := &AgentSessionHandle{
		SessionID: "conv-1",
		Scope:     "coding_agent",
		Provider:  llmtypes.CodingProviderSessionHandle{Provider: "claude_code", NativeSessionID: "native-abc"},
	}
	if err := store.Save(ctx, "conv-1", handle); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Mutating the original after Save must not affect the stored copy.
	handle.Provider.NativeSessionID = "MUTATED"

	got, err := store.Load(ctx, "conv-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil || got.Provider.NativeSessionID != "native-abc" {
		t.Fatalf("Load = %+v; want stored copy with native-abc (store aliased the caller's handle)", got)
	}

	if err := store.Delete(ctx, "conv-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if h, err := store.Load(ctx, "conv-1"); err != nil || h != nil {
		t.Fatalf("post-Delete Load = (%v, %v); want (nil, nil)", h, err)
	}
}

// TestFileCodingSessionStore covers the durable store, including that a FRESH
// store instance (simulating a process restart) reads back a handle written by
// an earlier one — the property that makes --resume-after-restart work.
func TestFileCodingSessionStore(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "sessions")

	store := NewFileCodingSessionStore(dir)
	if h, err := store.Load(ctx, "chat/../weird id"); err != nil || h != nil {
		t.Fatalf("cold Load = (%v, %v); want (nil, nil)", h, err)
	}

	handle := &AgentSessionHandle{
		SessionID: "chat/../weird id",
		Provider:  llmtypes.CodingProviderSessionHandle{Provider: "codex_cli", NativeSessionID: "thread-42", WorkingDir: "/work"},
	}
	if err := store.Save(ctx, "chat/../weird id", handle); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Simulate a process restart: a brand-new store over the same dir must read
	// back the handle the old instance persisted.
	restarted := NewFileCodingSessionStore(dir)
	got, err := restarted.Load(ctx, "chat/../weird id")
	if err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	if got == nil || got.Provider.NativeSessionID != "thread-42" || got.Provider.WorkingDir != "/work" {
		t.Fatalf("Load after restart = %+v; want thread-42 / /work", got)
	}

	// Distinct ids must not collide (hash suffix), even with the same sanitized prefix.
	other := &AgentSessionHandle{SessionID: "chat/../weird_id", Provider: llmtypes.CodingProviderSessionHandle{Provider: "codex_cli", NativeSessionID: "thread-99"}}
	if err := store.Save(ctx, "chat/../weird_id", other); err != nil {
		t.Fatalf("Save other: %v", err)
	}
	if got, _ := restarted.Load(ctx, "chat/../weird id"); got == nil || got.Provider.NativeSessionID != "thread-42" {
		t.Fatalf("collision: first id's handle was overwritten by a look-alike id: %+v", got)
	}

	if err := restarted.Delete(ctx, "chat/../weird id"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if h, err := store.Load(ctx, "chat/../weird id"); err != nil || h != nil {
		t.Fatalf("post-Delete Load = (%v, %v); want (nil, nil)", h, err)
	}
}

// TestSupportsSteeringMatchesContract proves SupportsSteering is driven by the
// provider contract (all tmux CLI providers steerable today; a non-coding
// provider is not).
func TestSupportsSteeringMatchesContract(t *testing.T) {
	// PLAT-872-19: native steering completes the current Run; marking it as a
	// tmux follow-up leaves the host waiting for a second completion.
	if (&Agent{provider: llm.ProviderCodexCLI}).liveInputUsesTmux() {
		t.Fatal("app-server incorrectly uses tmux follow-up completion")
	}

	for _, provider := range []llm.Provider{
		llm.ProviderClaudeCode, llm.ProviderCodexCLI, llm.ProviderCursorCLI, llm.ProviderPiCLI,
	} {
		a := &Agent{provider: provider}
		contract, ok := llm.GetCodingAgentProviderContract(provider, "")
		if !ok {
			t.Fatalf("%s: expected a coding-agent contract", provider)
		}
		if got := a.supportsSteering(); got != contract.SupportsLiveInput {
			t.Fatalf("%s: SupportsSteering()=%v; contract.SupportsLiveInput=%v", provider, got, contract.SupportsLiveInput)
		}
	}
	// A non-coding provider has no contract, so steering is unsupported.
	if (&Agent{provider: llm.ProviderOpenAI}).supportsSteering() {
		t.Fatalf("non-coding provider must not report steering support")
	}
}

// TestSupportsSteeringFalseOnStructuredTransport proves the transport-aware half
// of the steering decision: the SAME coding-agent provider that is steerable on
// tmux must report SupportsSteering()==false the moment it runs over the
// structured/JSON transport (a one-shot query-only process with no live pane).
// This is the regression guard for the transport-blind bug where SupportsSteering
// keyed only off the provider contract and would have tried to tmux-inject into a
// `--json` run. Covers all three structured-capable providers, via each of the
// three ways a call ends up structured, with a tmux control for contrast.
func TestSupportsSteeringFalseOnStructuredTransport(t *testing.T) {
	cases := []struct {
		name  string
		agent *Agent
		want  bool
	}{
		// Control: no structured flag → steerable (tmux), same as the contract test.
		{"codex tmux (control)", &Agent{provider: llm.ProviderCodexCLI}, true},
		{"cursor tmux (control)", &Agent{provider: llm.ProviderCursorCLI}, true},
		{"pi tmux (control)", &Agent{provider: llm.ProviderPiCLI}, true},

		// An explicit structured transport choice flips ANY coding-agent
		// provider to a one-shot process, so none of them are steerable.
		{"codex --json", &Agent{provider: llm.ProviderCodexCLI, codingAgentTransport: llm.CodingAgentTransportStructured}, false},
		{"cursor --print", &Agent{provider: llm.ProviderCursorCLI, codingAgentTransport: llm.CodingAgentTransportStructured}, false},
		{"pi --mode json", &Agent{provider: llm.ProviderPiCLI, codingAgentTransport: llm.CodingAgentTransportStructured}, false},
		{"claude -p stream-json", &Agent{provider: llm.ProviderClaudeCode, codingAgentTransport: llm.CodingAgentTransportStructured}, false},

		// An explicit tmux choice keeps them steerable (same as the control).
		{"codex explicit tmux", &Agent{provider: llm.ProviderCodexCLI, codingAgentTransport: llm.CodingAgentTransportTmux}, true},
		{"claude explicit tmux", &Agent{provider: llm.ProviderClaudeCode, codingAgentTransport: llm.CodingAgentTransportTmux}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.agent.supportsSteering(); got != tc.want {
				t.Fatalf("SupportsSteering()=%v; want %v", got, tc.want)
			}
			if got := AgentSupportsSteering(tc.agent); got != tc.want {
				t.Fatalf("exported AgentSupportsSteering()=%v; want %v (must match the unexported method it wraps)", got, tc.want)
			}
		})
	}
	if AgentSupportsSteering(nil) {
		t.Fatal("AgentSupportsSteering(nil) = true; want false — supportsSteering itself panics on a nil receiver")
	}
}

// TestDeliverQueuesOnStructuredCodingAgent exercises the full Deliver path for a
// coding-agent provider running structured: even though the provider's contract
// says it supports live input (true on tmux), a busy structured turn must QUEUE
// the message, not attempt a live tmux injection into a one-shot process. This is
// the end-to-end (still no live session) counterpart to
// TestDeliverQueuesWhenBusyAndNotSteerable, which only covered a non-coding
// provider.
func TestDeliverQueuesOnStructuredCodingAgent(t *testing.T) {
	a := &Agent{provider: llm.ProviderCodexCLI, codingAgentTransport: llm.CodingAgentTransportStructured}
	a.setTurnInFlight(true)

	got, err := a.deliver(context.Background(), "conv-json", "please also add tests", nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got.Mode != DeliveryModeQueued {
		t.Fatalf("Deliver mode = %q; want %q (a busy structured coding-agent turn must queue, not steer)", got.Mode, DeliveryModeQueued)
	}
	drained := a.drainSteerMessages()
	if len(drained) != 1 || drained[0].Text != "please also add tests" {
		t.Fatalf("queued messages = %v; want the delivered message", drained)
	}
}

// TestEnablePersistentInteractiveForProvider proves each coding-CLI provider's
// keep-alive flag is the one flipped on (and a non-CLI provider flips none).
func TestEnablePersistentInteractiveForProvider(t *testing.T) {
	cases := []struct {
		provider llm.Provider
		get      func(*Agent) bool
	}{
		{llm.ProviderClaudeCode, func(a *Agent) bool { return a.claudeCodePersistentInteractiveSession }},
		{llm.ProviderCodexCLI, func(a *Agent) bool { return a.codexPersistentInteractiveSession }},
		{llm.ProviderCursorCLI, func(a *Agent) bool { return a.cursorPersistentInteractiveSession }},
		{llm.ProviderPiCLI, func(a *Agent) bool { return a.piPersistentInteractiveSession }},
	}
	for _, tc := range cases {
		a := &Agent{provider: tc.provider}
		a.enablePersistentInteractiveForProvider()
		if !tc.get(a) {
			t.Fatalf("%s: keep-alive flag not enabled", tc.provider)
		}
		// No other provider's flag should have been touched.
		for _, other := range cases {
			if other.provider == tc.provider {
				continue
			}
			if other.get(a) {
				t.Fatalf("%s: enabling keep-alive wrongly set %s's flag too", tc.provider, other.provider)
			}
		}
	}
}

// TestDeliverQueuesWhenBusyAndNotSteerable exercises the real Deliver path for
// the query-only branch end-to-end (no live session needed): a non-steerable
// agent with a turn in flight must QUEUE the message via the steer buffer.
func TestDeliverQueuesWhenBusyAndNotSteerable(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI} // no coding contract => not steerable
	a.setTurnInFlight(true)

	got, err := a.deliver(context.Background(), "conv-x", "please also add tests", nil)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got.Mode != DeliveryModeQueued {
		t.Fatalf("Deliver mode = %q; want %q", got.Mode, DeliveryModeQueued)
	}
	drained := a.drainSteerMessages()
	if len(drained) != 1 || drained[0].Text != "please also add tests" {
		t.Fatalf("queued messages = %v; want the delivered message", drained)
	}
}

// TestDeliverEmptyMessageRejected guards the empty-message contract.
func TestDeliverEmptyMessageRejected(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI}
	if _, err := a.deliver(context.Background(), "conv-x", "   ", nil); err == nil {
		t.Fatalf("expected an error for an empty message")
	}
}

// TestTryClaimTurnInFlightIsAtomic pins the core guarantee the Deliver/
// ContinueConversation race fix rests on: of any number of concurrent
// callers, EXACTLY ONE ever wins the claim. Before this fix, Deliver's
// "should I start a turn?" check (a.isTurnInFlight()) and the actual claim
// (setTurnInFlight(true), deep inside ContinueConversation, after real I/O
// like store.Load) were two separate, non-atomic steps — two concurrent
// Deliver calls could both observe "not in flight" and both proceed to start
// overlapping turns. Run with -race to also catch any unsynchronized access.
func TestTryClaimTurnInFlightIsAtomic(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI}

	const n = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, n)
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // maximize actual overlap rather than sequential scheduling
			results[i] = a.tryClaimTurnInFlight()
		}()
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, won := range results {
		if won {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("tryClaimTurnInFlight: %d of %d concurrent callers won the claim; want exactly 1", wins, n)
	}
}

// TestContinueConversationReturnsErrTurnAlreadyInFlightWhenBusy proves
// ContinueConversation checks the claim BEFORE doing any work (I/O, state
// mutation) rather than after — the specific ordering bug this fix closes.
// A turn is already marked in flight; ContinueConversation must refuse
// immediately with ErrTurnAlreadyInFlight, never silently proceed to run a
// second, overlapping turn on top of it.
func TestContinueConversationReturnsErrTurnAlreadyInFlightWhenBusy(t *testing.T) {
	a := &Agent{provider: llm.ProviderOpenAI}
	if !a.tryClaimTurnInFlight() {
		t.Fatal("setup: expected the first claim to succeed")
	}

	_, err := a.continueConversation(context.Background(), "conv-x", "hello", nil)
	if !errors.Is(err, ErrTurnAlreadyInFlight) {
		t.Fatalf("ContinueConversation error = %v; want ErrTurnAlreadyInFlight", err)
	}
	// Must not have released a claim it never held.
	if !a.isTurnInFlight() {
		t.Fatal("the original claim must still be held — ContinueConversation should not touch a claim it lost")
	}
}
