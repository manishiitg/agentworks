package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/llm"
)

// coding_session.go is the mcpagent-owned coding-agent conversation abstraction.
// It centralizes the two decisions a product layer (AgentWorks, sparkquill, …)
// should NOT have to reimplement:
//
//  1. CONTINUITY (keep-alive vs --resume): a chat conversation reuses a live tmux
//     session on the fast path, and transparently relaunches + --resumes from the
//     provider-native session id when that live session is gone. Products differ
//     only in how DURABLY they persist the handle — that is what CodingSessionStore
//     abstracts (in-memory for a single process, on-disk for restart survival).
//
//  2. DELIVERY (steer vs query): a user message that arrives WHILE a turn is
//     running is steered straight into the running turn on transports that accept
//     live input (all tmux CLI providers today), queued for the next boundary on
//     query-only transports (future API/JSON streaming), and a message that
//     arrives IDLE simply starts the next (resumed) turn.
//
// The continuity fast path (warm tmux reuse) and the relaunch+--resume fallback
// already live in the provider adapters; ContinueConversation is the thin,
// store-backed wrapper that makes them usable across process restarts. The
// delivery routing is decideDelivery + Deliver.

// CodingSessionStore persists an AgentSessionHandle per conversation so a coding
// agent chat can be continued later — including after the host process restarts.
// Implementations must be safe for concurrent use. Load returns (nil, nil) when
// no handle has been stored yet for the conversation (a cold start).
type CodingSessionStore interface {
	Load(ctx context.Context, conversationID string) (*AgentSessionHandle, error)
	Save(ctx context.Context, conversationID string, h *AgentSessionHandle) error
	Delete(ctx context.Context, conversationID string) error
}

// --- in-memory store ---------------------------------------------------------

type memoryCodingSessionStore struct {
	mu      sync.RWMutex
	handles map[string]*AgentSessionHandle
}

// NewMemoryCodingSessionStore returns a process-local CodingSessionStore. It
// keeps conversation continuity alive for the lifetime of the process (the tmux
// keep-alive fast path); it does NOT survive a restart. Use it when the live
// session and the store share a lifetime — e.g. a single long-running server
// that never needs to --resume a cold conversation.
func NewMemoryCodingSessionStore() CodingSessionStore {
	return &memoryCodingSessionStore{handles: make(map[string]*AgentSessionHandle)}
}

func (s *memoryCodingSessionStore) Load(_ context.Context, conversationID string) (*AgentSessionHandle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handles[strings.TrimSpace(conversationID)]
	if !ok || h == nil {
		return nil, nil
	}
	clone := *h // AgentSessionHandle is all value fields — a copy fully detaches it
	return &clone, nil
}

func (s *memoryCodingSessionStore) Save(_ context.Context, conversationID string, h *AgentSessionHandle) error {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return fmt.Errorf("coding session store: conversationID is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h == nil {
		delete(s.handles, id)
		return nil
	}
	clone := *h
	s.handles[id] = &clone
	return nil
}

func (s *memoryCodingSessionStore) Delete(_ context.Context, conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.handles, strings.TrimSpace(conversationID))
	return nil
}

// --- on-disk store -----------------------------------------------------------

type fileCodingSessionStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileCodingSessionStore returns a CodingSessionStore that persists each
// conversation's handle as JSON under dir, so a conversation can be --resumed
// after the host process (and its tmux sessions) are gone. This is the durable
// path for products that outlive their live sessions (a restarting server, a
// scheduled worker). The directory is created lazily on first Save.
func NewFileCodingSessionStore(dir string) CodingSessionStore {
	return &fileCodingSessionStore{dir: dir}
}

// fileName maps a conversation id to a safe, collision-resistant filename: a
// sanitized human-readable prefix (for eyeballing the directory) plus a stable
// fnv-64a hash of the raw id (so distinct ids never collide and no id can
// escape dir via path separators or "..").
func (s *fileCodingSessionStore) fileName(conversationID string) string {
	id := strings.TrimSpace(conversationID)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	prefix := b.String()
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return fmt.Sprintf("%s-%016x.json", prefix, h.Sum64())
}

func (s *fileCodingSessionStore) path(conversationID string) string {
	return filepath.Join(s.dir, s.fileName(conversationID))
}

func (s *fileCodingSessionStore) Load(_ context.Context, conversationID string) (*AgentSessionHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(conversationID)) //nolint:gosec // path is derived from a sanitized+hashed id, never raw input
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // cold start — no handle stored yet
		}
		return nil, fmt.Errorf("coding session store: read handle: %w", err)
	}
	var h AgentSessionHandle
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("coding session store: decode handle: %w", err)
	}
	return &h, nil
}

func (s *fileCodingSessionStore) Save(_ context.Context, conversationID string, h *AgentSessionHandle) error {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return fmt.Errorf("coding session store: conversationID is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h == nil {
		if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("coding session store: clear handle: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("coding session store: create dir: %w", err)
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("coding session store: encode handle: %w", err)
	}
	// Write-and-rename so a concurrent Load never sees a half-written file.
	tmp := s.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("coding session store: write handle: %w", err)
	}
	if err := os.Rename(tmp, s.path(id)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("coding session store: commit handle: %w", err)
	}
	return nil
}

func (s *fileCodingSessionStore) Delete(_ context.Context, conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(conversationID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("coding session store: delete handle: %w", err)
	}
	return nil
}

// --- continuity: ContinueConversation ---------------------------------------

// ContinueConversation runs the next turn of a coding-agent conversation,
// carrying forward the provider-native session so the model keeps its history.
//
// It loads the conversation's stored handle (if any) and applies it — restoring
// both the keep-alive identity (a.SessionID) and the provider-native resume id
// (a.<Provider>SessionID). The tmux keep-alive fast path reuses a still-live
// session; if that session is gone, the adapter relaunches and --resumes from
// the native id transparently. This is the layered keep-alive-vs-resume decision,
// owned here so products don't reimplement it. On success the refreshed handle
// is saved back so the NEXT turn (even after a process restart) can continue.
//
// conversationID is the durable identity of the chat; it doubles as the tmux
// keep-alive session key. store may be nil for a fire-and-forget turn with no
// persistence.
func (a *Agent) continueConversation(ctx context.Context, conversationID, message string, store CodingSessionStore) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", fmt.Errorf("ContinueConversation: conversationID is empty")
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("ContinueConversation: message is empty")
	}

	// Claim the turn-in-flight slot ATOMICALLY, before any I/O or shared-state
	// mutation below (store.Load is real I/O — a real, non-trivial-latency race
	// window, not a nanosecond one; a.SessionID / a.enablePersistentInteractive...
	// mutate shared *Agent fields with no lock of their own). A prior version
	// only set the flag well after this point via setTurnInFlight(true), so two
	// concurrent callers (e.g. two Deliver calls that both observed
	// TurnInFlight()==false before either had started) could both pass the
	// caller-side check and both proceed to run overlapping turns here,
	// corrupting shared Agent state along the way. See Deliver's doc comment
	// for how it uses ErrTurnAlreadyInFlight to recover from losing this race.
	if !a.tryClaimTurnInFlight() {
		return "", ErrTurnAlreadyInFlight
	}
	defer a.setTurnInFlight(false)

	if store != nil {
		if h, err := store.Load(ctx, conversationID); err != nil {
			if a.logger != nil {
				a.logger.Warn(fmt.Sprintf("ContinueConversation: load handle failed (starting cold): conversation=%q err=%v", conversationID, err))
			}
		} else if h != nil && !h.Empty() {
			a.applyAgentSessionHandle(h)
		}
	}

	// The conversation id IS the keep-alive identity: the adapter reuses a live
	// tmux session keyed by it (fast path), or relaunches + --resumes from the
	// native session id restored above (fallback).
	a.sessionID = conversationID
	a.enablePersistentInteractiveForProvider()

	answer, err := a.ask(ctx, message)
	if err != nil {
		return answer, err
	}

	if store != nil {
		if h := a.currentAgentSessionHandle(); h != nil {
			if saveErr := store.Save(ctx, conversationID, h); saveErr != nil && a.logger != nil {
				a.logger.Warn(fmt.Sprintf("ContinueConversation: save handle failed (continuity may be lost): conversation=%q err=%v", conversationID, saveErr))
			}
		}
	}
	return answer, nil
}

// enablePersistentInteractiveForProvider turns on the tmux keep-alive mode for
// the agent's coding-CLI provider so the session stays warm for the fast path
// between turns. It is a no-op for non-coding-CLI providers.
func (a *Agent) enablePersistentInteractiveForProvider() {
	switch a.provider {
	case llm.ProviderClaudeCode:
		a.claudeCodePersistentInteractiveSession = true
	case llm.ProviderCodexCLI:
		a.codexPersistentInteractiveSession = true
	case llm.ProviderCursorCLI:
		a.cursorPersistentInteractiveSession = true
	case llm.ProviderPiCLI:
		a.piPersistentInteractiveSession = true
	case llm.ProviderMuseCLI:
		a.musePersistentInteractiveSession = true
	case llm.ProviderAgyCLI:
		a.agyPersistentInteractiveSession = true
	}
}

// ErrTurnAlreadyInFlight is returned by ContinueConversation when another
// turn is already running on this Agent. ContinueConversation claims the
// turn-in-flight slot ATOMICALLY at entry (a single mutex-guarded
// compare-and-set), so of any two concurrent callers exactly one wins the
// claim and proceeds; the other gets this error immediately, before doing any
// work. Deliver uses this to distinguish "lost the race, fall back to
// steer/queue" from a real failure inside the turn itself.
var ErrTurnAlreadyInFlight = errors.New("mcpagent: a turn is already in flight on this agent")

// tryClaimTurnInFlight atomically transitions turnInFlight false->true under
// one lock acquisition, reporting whether THIS call won the claim. Two
// concurrent callers can never both receive true.
func (a *Agent) tryClaimTurnInFlight() bool {
	a.turnInFlightMu.Lock()
	defer a.turnInFlightMu.Unlock()
	if a.turnInFlight {
		return false
	}
	a.turnInFlight = true
	a.openSteerQueue()
	return true
}

func (a *Agent) setTurnInFlight(v bool) {
	a.turnInFlightMu.Lock()
	a.turnInFlight = v
	if v {
		a.openSteerQueue()
	}
	a.turnInFlightMu.Unlock()
}

// TurnInFlight reports whether a ContinueConversation turn is currently running
// for this agent.
func (a *Agent) isTurnInFlight() bool {
	a.turnInFlightMu.Lock()
	defer a.turnInFlightMu.Unlock()
	return a.turnInFlight
}

// --- delivery: steer vs query -----------------------------------------------

// usesStructuredTransport reports whether THIS agent instance will run its
// coding-agent turns over the structured/JSON transport (`codex exec --json`,
// `cursor-agent --print`, `pi --print --mode json`) rather than tmux. It is the
// union of the three ways a call can end up structured, and must stay in lockstep
// with the actual execution condition in StartCodingAgentTransportSession /
// executeLLMInner (a.ForceStructuredCodingAgent || contract.Transport != tmux),
// plus the per-provider opt-in flags that flip a normally-tmux provider:
//
//   - ForceStructuredCodingAgent — the workflow step's transport="structured"
//   - CodexStructuredTransport / CursorStructuredTransport / PiStructuredTransport / MuseStructuredTransport
//   - a provider whose native contract transport simply isn't tmux
//
// Returns false for non-coding-agent providers (contract lookup fails) — steering
// is already false for those via SupportsSteering's own contract check.
func (a *Agent) usesStructuredTransport() bool {
	if a == nil {
		return false
	}
	if a.wantsStructuredTransport() {
		return true
	}
	// Muse follows its declared tmux contract now that the adapter runs the
	// tmux lane by default: structured only via the explicit
	// withCodingAgentTransport opt-in (which appends
	// WithMuseStructuredTransport alongside). A persistent-interactive muse
	// chat therefore steers and records tmux handles like cursor.
	if contract, ok := a.codingContract(); ok {
		return contract.Transport == llm.CodingAgentTransportStructured
	}
	return false
}

// wantsStructuredTransport reports whether the CALLER explicitly asked for the
// structured transport (withCodingAgentTransport). It is the single decision
// point every transport-dependent site consults — the four provider
// integrations before appending their own structured call option, the
// interactive-session-id skip, the terminal-launch guard, and
// usesStructuredTransport (steering).
//
// Routing all of them through one resolver is the point: this decision used to
// be expressible four different ways (a generic force flag plus four
// per-provider flags), the generic one silently did not work, and the steering
// predicate disagreed with what actually ran. One resolver makes that class of
// drift impossible.
//
// Empty CodingAgentTransport means "no explicit choice" — the provider
// contract's declared transport applies (see usesStructuredTransport).
func (a *Agent) wantsStructuredTransport() bool {
	if a == nil {
		return false
	}
	return a.codingAgentTransport == llm.CodingAgentTransportStructured
}

// SupportsSteering reports whether the agent's transport accepts live input into
// a running turn (true mid-turn steering). Only a tmux coding-agent session has a
// live pane to inject into; a structured/JSON run is a one-shot query-only
// process, so it must NOT be steered — Deliver queues for the next turn boundary
// instead. This is why the check is transport-aware, not just contract-aware: the
// same provider (e.g. Codex) is steerable on tmux but query-only on `--json`.
func (a *Agent) supportsSteering() bool {
	if a.usesStructuredTransport() {
		return false
	}
	contract, ok := a.codingContract()
	return ok && contract.SupportsLiveInput
}

// DeliveryMode reports how Deliver routed a message.
type DeliveryMode string

const (
	// DeliveryModeStartedTurn: no turn was in flight, so the message started the
	// next (warm-reused or --resumed) turn, which ran to completion.
	DeliveryModeStartedTurn DeliveryMode = "started_turn"
	// DeliveryModeSteered: a turn was in flight and the transport accepts live
	// input, so the message was injected straight into the running turn.
	DeliveryModeSteered DeliveryMode = "steered"
	// DeliveryModeQueued: a turn was in flight on a query-only transport, so the
	// message was queued for the next turn boundary.
	DeliveryModeQueued DeliveryMode = "queued"
)

// Delivered is the outcome of Deliver.
type Delivered struct {
	Mode DeliveryMode
	// Answer is the completed turn's final text; populated only for
	// DeliveryModeStartedTurn (steer/queue return immediately, before any answer).
	Answer string
}

type deliveryDecision string

const (
	decideStartTurn deliveryDecision = "start_turn"
	decideSteer     deliveryDecision = "steer"
	decideQueue     deliveryDecision = "queue"
)

// decideDelivery is the pure steer-vs-query routing rule, factored out so it is
// deterministically testable without a live session:
//
//	idle                    -> start the next (resumed) turn
//	busy + steerable        -> steer into the running turn
//	busy + not steerable    -> queue for the next boundary
func decideDelivery(turnInFlight, supportsSteering bool) deliveryDecision {
	if !turnInFlight {
		return decideStartTurn
	}
	if supportsSteering {
		return decideSteer
	}
	return decideQueue
}

// Deliver routes a user message using the steer-vs-query decision. If no turn is
// in flight it starts the next turn via ContinueConversation (blocking until the
// turn completes, and returning its answer). If a turn is in flight it steers the
// message into the running turn (steerable transports) or queues it for the next
// boundary (query-only transports), returning immediately.
//
// Deliver is safe to call from a different goroutine than the one running the
// in-flight turn — that is the point of steering while busy. It is ALSO safe
// against two Deliver calls racing each other: the a.isTurnInFlight() check
// below is only a fast-path hint (skip the attempt when we're confident it's
// busy) — correctness comes from ContinueConversation's own atomic claim.
// If two goroutines both see TurnInFlight()==false and both attempt to start
// a turn, exactly one wins ContinueConversation's claim; the other gets
// ErrTurnAlreadyInFlight back and falls through to the normal steer/queue
// path below using freshly-confirmed (not stale) in-flight state — it never
// silently starts a second, overlapping turn.
func (a *Agent) deliver(ctx context.Context, conversationID, message string, store CodingSessionStore) (Delivered, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return Delivered{}, fmt.Errorf("Deliver: message is empty")
	}

	if !a.isTurnInFlight() {
		answer, err := a.continueConversation(ctx, conversationID, message, store)
		if !errors.Is(err, ErrTurnAlreadyInFlight) {
			return Delivered{Mode: DeliveryModeStartedTurn, Answer: answer}, err
		}
		// Lost the race to another Deliver/ContinueConversation call — fall
		// through to steer/queue below, now with certainty a turn is in flight.
	}

	switch decideDelivery(true, a.supportsSteering()) {
	case decideSteer:
		steered, err := a.deliverUserMessage(ctx, UserMessageDeliveryRequest{
			SessionID: strings.TrimSpace(conversationID),
			Message:   message,
			Intent:    UserMessageDeliveryIntentLiveInput,
		})
		if err != nil {
			return Delivered{}, err
		}
		if steered.DeliveryStatus == UserMessageDeliveryStatusQueuedForInjection {
			return Delivered{Mode: DeliveryModeQueued}, nil
		}
		return Delivered{Mode: DeliveryModeSteered}, nil
	default: // decideQueue
		if !a.addSteerMessage(message) {
			return Delivered{}, &CodingAgentDeliveryError{Kind: DeliveryErrorKindNoSession, Provider: a.provider, Reason: "the turn has finished accepting input; start a new turn"}
		}
		return Delivered{Mode: DeliveryModeQueued}, nil
	}
}
