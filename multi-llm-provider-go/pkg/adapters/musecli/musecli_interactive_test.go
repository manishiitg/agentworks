package musecli

import (
	"context"
	"errors"

	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestMuseTmuxSessionNameIsNamespacedAndClean(t *testing.T) {
	seen := map[string]bool{}
	for _, owner := range []string{"", "Workflow/Testing Iteration 0", "abc"} {
		name := museTmuxSessionName(owner)
		if !strings.HasPrefix(name, museTmuxSessionPrefix) {
			t.Fatalf("session %q missing prefix %q", name, museTmuxSessionPrefix)
		}
		if strings.ContainsAny(name, "/ .") {
			t.Fatalf("session %q contains tmux-unsafe characters", name)
		}
		if seen[name] {
			t.Fatalf("duplicate session name %q", name)
		}
		seen[name] = true
	}
}

func TestMuseTmuxTransportRequestedDefaultsOff(t *testing.T) {
	if museTmuxTransportRequested(nil) {
		t.Fatal("nil options must not request tmux")
	}
	opts := &llmtypes.CallOptions{}
	if museTmuxTransportRequested(opts) {
		t.Fatal("empty options must not request tmux")
	}
	WithTmuxTransport(true)(opts)
	if !museTmuxTransportRequested(opts) {
		t.Fatal("WithTmuxTransport(true) not honored")
	}
	WithTmuxTransport(false)(opts)
	if museTmuxTransportRequested(opts) {
		t.Fatal("WithTmuxTransport(false) not honored")
	}
}

// A reused Muse pane no longer contains the fresh-launch banner once a long
// conversation has scrolled it away. It is nevertheless ready for the next
// turn when the prompt and status line are present. The turn path must use
// this retained-session readiness rule instead of waiting 90 seconds for a
// banner that cannot return.
func TestMuseReusedSessionReadinessDoesNotRequireBootBanner(t *testing.T) {
	pane := strings.Join([]string{
		"◆ Previous turn completed successfully.",
		"",
		"❯",
		"muse-spark-1.3-contributor · xhigh · /tmp/workflow",
	}, "\n")

	if museTUISufficientlySettled(pane) {
		t.Fatal("a retained pane without the Muse Code boot banner must not pass the fresh-launch check")
	}
	if !museTUIAtPrompt(pane) {
		t.Fatal("a retained pane with prompt and status chrome must be ready for the next turn")
	}
}

func TestMuseStructuredTransportRequestedDefaultsOff(t *testing.T) {
	if museStructuredTransportRequested(nil) {
		t.Fatal("nil options must not request structured")
	}
	opts := &llmtypes.CallOptions{}
	if museStructuredTransportRequested(opts) {
		t.Fatal("empty options must not request structured (tmux is the default)")
	}
	WithMuseStructuredTransport(true)(opts)
	if !museStructuredTransportRequested(opts) {
		t.Fatal("WithMuseStructuredTransport(true) not honored")
	}
	WithMuseStructuredTransport(false)(opts)
	if museStructuredTransportRequested(opts) {
		t.Fatal("WithMuseStructuredTransport(false) not honored")
	}
}

// TestMusePersistentKeyAndName: pooling without an owner must fail fast
// (two conversations must never share a TUI), and tmux names must be
// stable, safe, and bounded.
func TestMusePersistentKeyAndName(t *testing.T) {
	if _, err := musePersistentKey(""); err == nil {
		t.Fatal("empty owner must fail, not pool anonymously")
	}
	if _, err := musePersistentKey("  "); err == nil {
		t.Fatal("blank owner must fail")
	}
	key, err := musePersistentKey("conv-123")
	if err != nil || key != "conv-123" {
		t.Fatalf("key = %q, %v", key, err)
	}
	a, b := musePersistentTmuxName("conv-123"), musePersistentTmuxName("conv-123")
	if a != b || !strings.HasPrefix(a, "mlp-muse-") {
		t.Fatalf("names not stable/prefixed: %q %q", a, b)
	}
	weird := musePersistentTmuxName("Conv 123/ABC!@#xyz")
	for _, r := range weird {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			t.Fatalf("unsafe tmux name %q", weird)
		}
	}
	if got := musePersistentTmuxName(strings.Repeat("x", 200)); len(got) > 48 {
		t.Fatalf("name not bounded: %d chars", len(got))
	}
	KillMusePersistentSession("test-owner-that-never-existed")
}

func TestMuseLastAssistantTextPicksLatest(t *testing.T) {
	text := func(s string) llmtypes.ContentPart { return llmtypes.TextContent{Text: s} }
	messages := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{text("hi")}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{text("first")}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{text("second")}},
	}
	if got := museLastAssistantText(messages); got != "second" {
		t.Fatalf("last assistant text = %q, want second", got)
	}
	if got := museLastAssistantText(nil); got != "" {
		t.Fatalf("empty transcript = %q, want empty", got)
	}
}

func TestPromptSnippetTruncates(t *testing.T) {
	if got := promptSnippet("  hi  "); got != "hi" {
		t.Fatalf("snippet = %q, want trimmed hi", got)
	}
	long := strings.Repeat("x", 200)
	if got := promptSnippet(long); len(got) != 120 {
		t.Fatalf("snippet len = %d, want 120", len(got))
	}
	if got := promptSnippet("AIS\tTDS-393"); got != "AIS TDS-393" {
		t.Fatalf("tabular snippet = %q, want terminal-safe spaces", got)
	}
	// The production notification had an em dash starting at byte 119.
	unicodePrompt := strings.Repeat("x", 119) + "— delivered"
	if got := promptSnippet(unicodePrompt); got != strings.Repeat("x", 119) {
		t.Fatalf("unicode snippet = %q, want complete UTF-8 prefix", got)
	}
}

// TestMuseDiscoverSessionSince finds the newest matching session log in a
// fixture tree: the newest turn's log wins over older runs and unrelated logs.
func TestMuseDiscoverSessionSince(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "muse", "sessions", "2026", "09", "10")
	oldDir := filepath.Join(day, "sess-old")
	newDir := filepath.Join(day, "sess-new")
	otherDir := filepath.Join(day, "sess-other")
	for _, dir := range []string{oldDir, newDir, otherDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.WriteFile(filepath.Join(oldDir, "session.jsonl"), []byte(museIntakeRow(1, oldTime.UnixMicro(), "pineapple old")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(oldDir, "session.jsonl"), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "session.jsonl"), []byte(museIntakeRow(1, time.Now().UnixMicro(), "unrelated chatter")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "session.jsonl"), []byte(museIntakeRow(1, time.Now().UnixMicro(), "pineapple new turn")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	since := time.Now().Add(-time.Minute)
	id, path, err := museDiscoverSessionSince(home, since, "pineapple", "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if id != "sess-new" {
		t.Fatalf("session id = %q, want sess-new", id)
	}
	if path != filepath.Join(newDir, "session.jsonl") {
		t.Fatalf("log path = %q", path)
	}
	if _, _, err := museDiscoverSessionSince(home, since, "mango", ""); err == nil {
		t.Fatal("expected error when no log mentions the prompt")
	}
}

// TestMuseDiscoverSessionSinceMultilineSnippet pins the intake false
// negative: session.jsonl stores newlines JSON-escaped, so a snippet holding
// a literal newline must match the decoded intake text. Every
// multi-line builder prompt failed intake while single-line test prompts
// always matched.
func TestMuseDiscoverSessionSinceMultilineSnippet(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "muse", "sessions", "2026", "09", "10")
	dir := filepath.Join(day, "sess-multi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := museIntakeRow(1, time.Now().UnixMicro(), "# Workflow Builder Agent\n\nYou design, run, monitor.") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	id, _, err := museDiscoverSessionSince(home, since, "# Workflow Builder Agent\n\nYou design", "")
	if err != nil {
		t.Fatalf("discover multiline snippet: %v", err)
	}
	if id != "sess-multi" {
		t.Fatalf("session id = %q, want sess-multi", id)
	}
}

func TestMuseDiscoverSessionSinceUnicodeIntakeNotPaneEcho(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "muse", "sessions", "2026", "09", "22", "sess-notification")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	prompt := "[AUTO-NOTIFICATION] Agent 'Message sequence item -> Validate Candidates / execute-and-verify (user_message)' completed — status=completed"
	snippet := promptSnippet(prompt)
	if !utf8.ValidString(snippet) {
		t.Fatalf("snippet split a UTF-8 character: %q", snippet)
	}
	since := time.Now().Add(-time.Second)
	old := museIntakeRow(1, since.Add(-time.Second).UnixMicro(), prompt)
	echo := fmt.Sprintf(`{"sequence":2,"recorded_at":%d,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":%q}}}`, time.Now().UnixMicro(), prompt)
	if err := os.WriteFile(path, []byte(old+"\n"+echo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := museDiscoverSessionSince(home, since, snippet, ""); err == nil {
		t.Fatal("stale intake plus fresh assistant echo must not confirm this send")
	}
	if err := os.WriteFile(path, []byte(old+"\n"+echo+"\n"+museIntakeRow(3, time.Now().UnixMicro(), prompt)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subagentDir := filepath.Join(dir, "subagent", "child")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, "session.jsonl"), []byte(museIntakeRow(4, time.Now().UnixMicro(), prompt)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, _, err := museDiscoverSessionSince(home, since, snippet, ""); err != nil || id != "sess-notification" {
		t.Fatalf("new Unicode intake = (%q, %v), want sess-notification", id, err)
	}
}

// TestMusePromptNeedsAtomicPaste pins the prompt-size routing that avoids
// tmux's "command too long" rejection: ordinary prompts type literally in
// bounded chunks, while prompts at/above the cursor-mirrored thresholds go
// through one atomic buffer paste.
func TestMusePromptNeedsAtomicPaste(t *testing.T) {
	largeRunes := strings.Repeat("x", museAtomicPasteMinRunes)
	manyLines := strings.Repeat("line\n", museAtomicPasteMinLines)
	fewLines := strings.Repeat("line\n", museAtomicPasteMinLines-1)
	for _, tc := range []struct {
		name   string
		prompt string
		want   bool
	}{
		{"short prompt types literally", "hello", false},
		{"multiline under threshold types literally", "a\nb\nc", false},
		{"rune threshold pastes atomically", largeRunes, true},
		{"line threshold pastes atomically", manyLines, true},
		{"just under line threshold types literally", strings.TrimSuffix(fewLines, "\n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := musePromptNeedsAtomicPaste(tc.prompt); got != tc.want {
				t.Fatalf("musePromptNeedsAtomicPaste(%q...) = %v, want %v", tc.prompt[:min(20, len(tc.prompt))], got, tc.want)
			}
		})
	}
}

func TestMusePaneContainsVisibleDraftNormalizesRenderedWhitespace(t *testing.T) {
	snippet := "ok also check AIS\tTDS-393(1)[Table: S.\tBusiness"
	pane := "❯ ok also check AIS    TDS-393(1)[Table: S.    Business receipts\n" +
		"muse-spark-1.3-contributor · max · /tmp/workflow"

	if !musePaneContainsVisibleDraft(pane, snippet) {
		t.Fatal("a copied tabular prompt must match the spaces Muse renders in the pane")
	}
	if musePaneContainsVisibleDraft(pane, "a different user prompt") {
		t.Fatal("unrelated pane text must not confirm the draft")
	}
}

// TestMuseSplitPrompt pins the file-only contract kernel for both lanes:
// system-role texts separate from the human turn (tmux lane projects them
// to AGENTS.md; the exec lane concatenates via museInlinePrompt). A missing
// human turn is an error in both lanes.
func TestMuseSplitPrompt(t *testing.T) {
	msgs := func() []llmtypes.MessageContent {
		return []llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "system-one"}}},
			{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "system-two"}}},
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
		}
	}
	system, human, err := museSplitPrompt(msgs())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(system) != 2 || system[0] != "system-one" || system[1] != "system-two" {
		t.Fatalf("system = %q, want both system texts in order", system)
	}
	if human != "hi" {
		t.Fatalf("human = %q, want hi", human)
	}
	// Exec-lane concatenation and the file-only fallback share museInlinePrompt.
	if got := museInlinePrompt(system, human); got != "system-one\n\nsystem-two\n\nhi" {
		t.Fatalf("inline = %q, want concatenated preamble + human", got)
	}
	if got := museInlinePrompt(nil, human); got != "hi" {
		t.Fatalf("inline without system = %q, want bare human", got)
	}
	if _, _, err := museSplitPrompt([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "only system"}}},
	}); err == nil {
		t.Fatal("expected error when no human turn exists")
	}
}

// TestWriteMuseProjectAgentsFile pins the codex-mirrored byte-restore
// contract: a pre-existing operator AGENTS.md comes back byte-for-byte when
// restorePrior is set, and a projected file is removed otherwise.
func TestWriteMuseProjectAgentsFile(t *testing.T) {
	workdir := t.TempDir()
	path := filepath.Join(workdir, "AGENTS.md")
	operator := "# operator rules\n"
	if err := os.WriteFile(path, []byte(operator), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := writeMuseProjectAgentsFile(workdir, "session instructions", false)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "session instructions") || !strings.Contains(string(raw), "# operator rules") {
		t.Fatalf("projected AGENTS.md missing prompt or operator content:\n%s", raw)
	}
	release()
	if raw, _ = os.ReadFile(path); string(raw) != operator {
		t.Fatalf("after cleanup = %q, want operator content byte-for-byte", raw)
	}

	plain := t.TempDir()
	release, err = writeMuseProjectAgentsFile(plain, "session instructions", false)
	if err != nil {
		t.Fatalf("write in an empty folder: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(plain, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("projected AGENTS.md not removed on cleanup")
	}
}

// TestProjectMuseAgentsForTurnFallback pins the never-drop contract: when
// the projection write fails, the caller gets (nil, false) and types the
// preamble inline instead of losing it.
func TestProjectMuseAgentsForTurnFallback(t *testing.T) {
	if restore, projected := projectMuseAgentsForTurn("", []string{"sys"}, true, false); restore != nil || projected {
		t.Fatal("empty workdir must not project")
	}
	// A file where the workdir should be makes MkdirAll/Write fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if restore, projected := projectMuseAgentsForTurn(filepath.Join(blocker, "sub"), []string{"sys"}, true, false); restore != nil || projected {
		t.Fatal("unwritable workdir must fall back to inline, not claim projection")
	}
	if restore, projected := projectMuseAgentsForTurn(t.TempDir(), []string{"sys"}, true, true); restore == nil || !projected {
		t.Fatal("writable workdir must project")
	} else {
		restore()
	}
}

// TestMuseResolveTmuxPromptDefaultsToFold is the regression for a real bug:
// museResolveTmuxPrompt previously started from `human` alone and only
// widened to the inline fold on a fallback condition that never fires when
// instruction-only mode is off (the default) -- silently dropping every
// system message for every default-mode tmux call. No test exercised a
// system message through the tmux lane with instructionOnly left unset
// before this was caught. Every case here is the actual call-site
// contract: wantAgents/agentsProjected reaching false is normal, expected
// traffic (default mode, or a projection attempt that failed), not an edge
// case.
func TestMuseResolveTmuxPromptDefaultsToFold(t *testing.T) {
	system := []string{"Be brief."}
	const human = "say hi"
	const folded = "Be brief.\n\nsay hi"

	cases := []struct {
		name            string
		wantAgents      bool
		agentsProjected bool
		want            string
	}{
		{"default mode (instructionOnly off): must fold, not drop the system message", false, false, folded},
		{"instructionOnly on but projection failed: falls back to folding", true, false, folded},
		{"instructionOnly on and projection succeeded: bare human, AGENTS.md carries it", true, true, human},
		// Not reachable from the real call site (wantAgents requires
		// len(system) > 0 upstream, so it can't be false while projected is
		// true) but pinned anyway: projected alone must not bypass the fold.
		{"agentsProjected true but wantAgents false: still folds", false, true, folded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := museResolveTmuxPrompt(system, human, tc.wantAgents, tc.agentsProjected); got != tc.want {
				t.Errorf("museResolveTmuxPrompt(%v, %q, %v, %v) = %q, want %q",
					system, human, tc.wantAgents, tc.agentsProjected, got, tc.want)
			}
		})
	}
}

// TestMuseResolveTmuxPromptNoSystemMessage confirms the trivial case is
// unaffected by any of this: no system message means the prompt is always
// just the human turn, regardless of the instruction-only flags.
func TestMuseResolveTmuxPromptNoSystemMessage(t *testing.T) {
	const human = "say hi"
	for _, wantAgents := range []bool{false, true} {
		for _, agentsProjected := range []bool{false, true} {
			if got := museResolveTmuxPrompt(nil, human, wantAgents, agentsProjected); got != human {
				t.Errorf("museResolveTmuxPrompt(nil, %q, %v, %v) = %q, want bare human %q",
					human, wantAgents, agentsProjected, got, human)
			}
		}
	}
}

func TestMuseMCPConfigsEquivalentIgnoresOnlyTransientBridgeState(t *testing.T) {
	config := func(scope, ready, tools string) string {
		return `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://api","MCP_SESSION_ID":"session-1","MCP_TOOLS":` + strconv.Quote(tools) + `,"MCP_VIRTUAL_SCOPE_ID":` + strconv.Quote(scope) + `,"MCP_READY_FILE":` + strconv.Quote(ready) + `}}}}`
	}
	first := config("session-1:vt:trace-a", "/tmp/ready-a", "tools-a")
	second := config("session-1:vt:trace-b", "/tmp/ready-b", "tools-a")
	if !museMCPConfigsEquivalent(first, second) {
		t.Fatal("per-turn bridge scope and readiness paths must not invalidate a retained Muse session")
	}
	if museMCPConfigsEquivalent(first, config("session-1:vt:trace-b", "/tmp/ready-b", "tools-b")) {
		t.Fatal("a durable MCP tool-surface change must relaunch the retained Muse session")
	}
	if museMCPConfigsEquivalent("", second) {
		t.Fatal("mounting or removing MCP configuration must relaunch the retained Muse session")
	}
}

// TestCloseMuseCLIInteractiveSessionByTmux is the muse-cli equivalent of the
// pattern picli/cursorcli/codexcli/claudecode all already had:
// Close<Provider>InteractiveSessionByTmux, a teardown-by-tmux-name backstop
// used when the owning session ID is unknown or drifted (workflow
// sub-agents registered under a step-execution owner). Muse had no
// equivalent at all -- mcp-agent-builder-go's provider-agnostic cleanup
// paths (isCodingAgentTmuxSessionName, gracefulCloseCodingCLITmuxByName,
// closeAllCodingCLIInteractiveSessionsForOwner) silently skipped muse
// sessions entirely, so a workshop-mode switch could leave a stale
// persistent muse TUI running instead of relaunching it with fresh content.
func TestCloseMuseCLIInteractiveSessionByTmux(t *testing.T) {
	const tmuxName = "mlp-muse-test-close-by-tmux"
	restored := false
	musePersistentPool.Lock()
	musePersistentPool.m["owner-under-test"] = &musePersistentSession{
		tmuxName:      tmuxName,
		restoreAgents: func() { restored = true },
	}
	musePersistentPool.Unlock()

	CloseMuseCLIInteractiveSessionByTmux(tmuxName, "test")

	musePersistentPool.Lock()
	_, stillPooled := musePersistentPool.m["owner-under-test"]
	musePersistentPool.Unlock()
	if stillPooled {
		t.Fatal("expected the pool entry to be removed")
	}
	if !restored {
		t.Fatal("expected restoreAgents to run as part of teardown")
	}

	// A tmux name with no pooled entry must be a safe no-op, not a panic.
	CloseMuseCLIInteractiveSessionByTmux("mlp-muse-no-such-session", "test")
	CloseMuseCLIInteractiveSessionByTmux("", "test")
}

// TestCloseMuseCLIInteractiveSessionForOwner pins that the owner-keyed close
// (the naming-convention wrapper other providers export) actually removes
// the pooled entry, matching KillMusePersistentSession's contract.
func TestCloseMuseCLIInteractiveSessionForOwner(t *testing.T) {
	musePersistentPool.Lock()
	musePersistentPool.m["owner-for-close-test"] = &musePersistentSession{tmuxName: "mlp-muse-owner-close-test"}
	musePersistentPool.Unlock()

	CloseMuseCLIInteractiveSessionForOwner("owner-for-close-test", "test")

	musePersistentPool.Lock()
	_, stillPooled := musePersistentPool.m["owner-for-close-test"]
	musePersistentPool.Unlock()
	if stillPooled {
		t.Fatal("expected the pool entry to be removed")
	}
}

func TestMuseClosePersistentTmuxAfterCanceledTurn(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	owner := "cancel-cleanup-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	target := musePersistentTmuxName(owner)
	other := musePersistentTmuxName(owner + "-other")
	for _, name := range []string{target, other} {
		if out, err := exec.CommandContext(t.Context(), "tmux", "new-session", "-d", "-s", name, "sleep 60").CombinedOutput(); err != nil {
			t.Fatalf("create test tmux session %q: %v: %s", name, err, out)
		}
		t.Cleanup(func() { _ = museClosePersistentTmux(name) })
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatal("test turn was not canceled")
	}
	if err := museClosePersistentTmux(target); err != nil {
		t.Fatal(err)
	}
	if museTmuxSessionAlive(context.Background(), "="+target) {
		t.Fatal("canceled turn left its untracked tmux session alive")
	}
	if !museTmuxSessionAlive(context.Background(), "="+other) {
		t.Fatal("cleanup affected another conversation's tmux session")
	}
	// Cleanup is also safe when a previous attempt already removed the name.
	if err := museClosePersistentTmux(target); err != nil {
		t.Fatal(err)
	}
}

func TestMusePersistentTurnGateSerializesOwnerAndHonorsCancellation(t *testing.T) {
	releaseFirst, err := museAcquirePersistentTurn(context.Background(), "turn-gate-owner")
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := museAcquirePersistentTurn(context.Background(), "turn-gate-owner")
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second turn acquired before first turn released")
	case <-time.After(25 * time.Millisecond):
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := museAcquirePersistentTurn(canceled, "turn-gate-owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire error = %v, want context.Canceled", err)
	}

	releaseFirst()
	select {
	case releaseSecond := <-acquired:
		releaseSecond()
	case <-time.After(time.Second):
		t.Fatal("second turn did not acquire after first turn released")
	}
}

// TestCleanupMuseCLIInteractiveSessions pins the bulk sweep the workflow P0
// harness calls between matrix providers: every pooled entry is removed and
// its retained restores run, so no live pane (or settings/AGENTS.md mount)
// leaks across providers.
func TestCleanupMuseCLIInteractiveSessions(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required: without it the sweep correctly no-ops")
	}
	mcpRestored, agentsRestored := false, false
	musePersistentPool.Lock()
	musePersistentPool.m["owner-sweep-a"] = &musePersistentSession{
		tmuxName:      "mlp-muse-sweep-a",
		restoreMCP:    func() { mcpRestored = true },
		restoreAgents: func() { agentsRestored = true },
	}
	musePersistentPool.m["owner-sweep-b"] = &musePersistentSession{tmuxName: "mlp-muse-sweep-b"}
	musePersistentPool.Unlock()

	if err := CleanupMuseCLIInteractiveSessions(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	musePersistentPool.Lock()
	remaining := len(musePersistentPool.m)
	musePersistentPool.Unlock()
	if remaining != 0 {
		t.Fatalf("pool still holds %d entries after sweep", remaining)
	}
	if !mcpRestored || !agentsRestored {
		t.Fatal("expected both retained restores to run as part of the sweep")
	}
}

// TestMuseTranscriptLineToChunks pins the tmux event synthesis the layer-2
// streaming certs depend on: assistant commits -> Content, tool commits ->
// paired ToolCallStart, result batches -> ToolCallEnd with real text,
// reasoning deltas -> Reasoning (plumbing noise dropped), effect records as
// start fallback / end backstop, unknown lines ignored.
func TestMuseTranscriptLineToChunks(t *testing.T) {
	seen, ended, started := map[string]bool{}, map[string]bool{}, map[string]time.Time{}
	lines := []string{
		`not json at all`,
		`{"sequence":1,"payload_type":"runtime.session","payload":{"event":{"kind":"status","message":"opening meta model stream attempt 1/10"}}}`,
		`{"sequence":2,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":"The build ID is X."}}}`,
		`{"sequence":3,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_tool_calls_committed","tool_calls":[{"call_id":"call_1","name":"read","args":"{\"path\":\"f\"}"}]}}}`,
		`{"sequence":4,"payload_type":"tool_batch_effect","payload":{"record":{"kind":"started","call_id":"call_1","tool_name":"read"}}}`,
		`{"sequence":5,"payload_type":"runtime.session","payload":{"event":{"kind":"reasoning_summary_delta","text":"Checking the file."}}}`,
		`{"sequence":6,"payload_type":"runtime.session","payload":{"event":{"kind":"reasoning_summary_delta","text":"opening stream attempt 3 completed"}}}`,
		`{"sequence":7,"payload_type":"runtime.session","payload":{"event":{"kind":"tool_result_batch_committed","results":[{"tool_call_id":"call_1","text":"file-bytes"}]}}}`,
		`{"sequence":8,"payload_type":"tool_batch_effect","payload":{"record":{"kind":"terminal","call_id":"call_1"}}}`,
		`{"sequence":9,"payload_type":"tool_batch_effect","payload":{"record":{"kind":"started","call_id":"call_2","tool_name":"shell"}}}`,
		`{"sequence":10,"payload_type":"tool_batch_effect","payload":{"record":{"kind":"terminal","call_id":"call_2"}}}`,
		`{"sequence":11,"payload_type":"runtime.session","payload":{"event":{"kind":"mystery_future_kind","text":"ignore me"}}}`,
	}
	var got []llmtypes.StreamChunk
	for _, l := range lines {
		got = append(got, museTranscriptLineToChunks(l, seen, ended, started)...)
	}
	var content, starts, ends, reasoning int
	var startIDs, endIDs []string
	for _, c := range got {
		switch c.Type {
		case llmtypes.StreamChunkTypeContent:
			content++
			if !strings.Contains(c.Content, "build ID") {
				t.Fatalf("content = %q, want committed text", c.Content)
			}
		case llmtypes.StreamChunkTypeToolCallStart:
			starts++
			startIDs = append(startIDs, c.ToolCallID+":"+c.ToolName)
		case llmtypes.StreamChunkTypeToolCallEnd:
			ends++
			endIDs = append(endIDs, c.ToolCallID)
		case llmtypes.StreamChunkTypeReasoning:
			if c.Metadata["presentation"] != nil {
				t.Fatal("Muse reasoning summary must be compact thinking, not an assistant update (PLAT-781)")
			}
			reasoning++
			if !strings.Contains(c.Content, "Checking") {
				t.Fatalf("reasoning = %q, want delta text", c.Content)
			}
		default:
			t.Fatalf("unexpected chunk type %q", c.Type)
		}
	}
	if content != 1 || reasoning != 1 {
		t.Fatalf("content=%d reasoning=%d, want 1 and 1 (noise + dupes dropped)", content, reasoning)
	}
	if strings.Join(startIDs, ",") != "call_1:read,call_2:shell" {
		t.Fatalf("starts = %q, want committed-first dedup + effect fallback", startIDs)
	}
	if strings.Join(endIDs, ",") != "call_1,call_2" {
		t.Fatalf("ends = %q, want exactly one end per start", endIDs)
	}
	for _, c := range got {
		if c.Type == llmtypes.StreamChunkTypeToolCallEnd && c.ToolCallID == "call_1" && c.ToolResult != "file-bytes" {
			t.Fatalf("call_1 result = %q, want real result text", c.ToolResult)
		}
	}
}

// TestMuseTranscriptStreamStatePollsBySequence pins the no-replay tailing:
// records at/below the primed max never emit, newer ones emit once in order.
func TestMuseTranscriptStreamStatePollsBySequence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	write := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	line := func(seq int, text string) string {
		return `{"sequence":` + strconv.Itoa(seq) + `,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":"` + text + `"}}}`
	}
	write(line(1, "history"))
	st := newMuseTranscriptStreamState(path, "", true, false)
	if st.lastSeq != 1 {
		t.Fatalf("primed lastSeq = %d, want 1", st.lastSeq)
	}
	ch := make(chan llmtypes.StreamChunk, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st.poll(ctx, ch)
	if len(ch) != 0 {
		t.Fatal("primed history must not emit")
	}
	write(line(1, "history"), line(2, "fresh-two"), line(3, "fresh-three"))
	st.poll(ctx, ch)
	close(ch)
	var got []string
	for c := range ch {
		got = append(got, c.Content)
	}
	if strings.Join(got, "|") != "fresh-two|fresh-three" {
		t.Fatalf("polled = %q, want only post-prime records in order", got)
	}
}

func TestMuseTranscriptStreamIncludesFastCommitAfterPreSubmitBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	rows := []string{
		`{"sequence":1,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":"old answer"}}}`,
		`{"sequence":2,"payload_type":"runtime.session","payload":{"event":{"kind":"assistant_message_committed","text":"fast current answer"}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The current answer may commit before intake discovers the path.
	state := newMuseTranscriptStreamStateAt(path, "", true, false, 1)
	ch := make(chan llmtypes.StreamChunk, 4)
	state.poll(context.Background(), ch)
	if len(ch) != 1 || (<-ch).Content != "fast current answer" {
		t.Fatal("fast current commit must stream exactly once without old history")
	}
}

// TestMuseTUIApprovalArgv pins the mounted-turn approval posture both launch
// paths share: --approval-mode never must stay present — --disable-approval
// alone lets MCP tools park in approval_wait and hang the turn (proven live).
func TestMuseTUIApprovalArgv(t *testing.T) {
	argv := museTUIApprovalArgv()
	has := func(flag string) bool {
		for _, a := range argv {
			if a == flag {
				return true
			}
		}
		return false
	}
	if !has("--disable-approval") || !has("--approval-mode") || !has("never") {
		t.Fatalf("approval argv = %q, want --disable-approval --approval-mode never", argv)
	}
}

func TestMuseNativeContainmentArgvHardDisablesShellAndWrites(t *testing.T) {
	argv := museNativeContainmentArgv()
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--disable-shell", "--disable-write"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("containment argv = %q, missing %s", argv, want)
		}
	}
}

// TestMuseDiscoverSessionSincePrefersWorkdir pins concurrent-turn isolation:
// two fresh logs mentioning the identical prompt must resolve to the one
// whose workspace_root matches the caller's workdir — otherwise worker 1's
// turn attributes worker 0's log (proven live: worker 1 answered with
// worker 0's build id).
func TestMuseDiscoverSessionSincePrefersWorkdir(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "muse", "sessions", "2026", "09", "10")
	mklog := func(sess, root, snippet string) {
		t.Helper()
		dir := filepath.Join(day, sess)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"sequence":9,"payload_type":"runtime.session.metadata","payload":{"record":{"workspace_root":` + strconv.Quote(root) + `}}}` + "\n" +
			museIntakeRow(10, time.Now().UnixMicro(), snippet) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workA := filepath.Join(t.TempDir(), "workA")
	workB := filepath.Join(t.TempDir(), "workB")
	mklog("sess-a", workA, "Reply ONLY with the build id.")
	mklog("sess-b", workB, "Reply ONLY with the build id.")
	// Make the wrong log strictly fresher: only the workdir filter may
	// still resolve sess-b.
	fresh := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(day, "sess-a", "session.jsonl"), fresh, fresh); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	id, _, err := museDiscoverSessionSince(home, since, "Reply ONLY with the build id.", workB)
	if err != nil {
		t.Fatalf("discover with workdir: %v", err)
	}
	if id != "sess-b" {
		t.Fatalf("session id = %q, want sess-b (workdir match beats freshness ties)", id)
	}
	if _, _, err := museDiscoverSessionSince(home, since, "Reply ONLY with the build id.", filepath.Join(t.TempDir(), "workC")); err == nil {
		t.Fatal("expected error when no log records the workdir")
	}
}

// TestMuseDiscoverSessionSinceWorkdirCaseInsensitive pins a live-proven bug:
// on a case-insensitive-but-case-preserving filesystem (macOS APFS default),
// muse can canonicalize the workdir it records in workspace_root to a
// different case than the caller passed in (observed live: caller passed
// ".../AgentWorks/...", muse recorded ".../agentworks/..." — the true
// on-disk casing). An exact-case match rejected the one log that actually
// answered the prompt, timing every turn against that workdir out as
// "never took in the prompt" despite muse working correctly.
func TestMuseDiscoverSessionSinceWorkdirCaseInsensitive(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "muse", "sessions", "2026", "09", "10")
	callerWorkdir := filepath.Join(t.TempDir(), "AgentWorks", "state")
	recordedRoot := strings.ToLower(callerWorkdir)
	dir := filepath.Join(day, "sess-case")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"sequence":9,"payload_type":"runtime.session.metadata","payload":{"record":{"workspace_root":` + strconv.Quote(recordedRoot) + `}}}` + "\n" +
		museIntakeRow(10, time.Now().UnixMicro(), "hi there") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	id, _, err := museDiscoverSessionSince(home, since, "hi there", callerWorkdir)
	if err != nil {
		t.Fatalf("discover with case-differing workdir: %v", err)
	}
	if id != "sess-case" {
		t.Fatalf("session id = %q, want sess-case", id)
	}
}

// TestSendMuseInteractiveInputNoSession pins the steer-path failure mode:
// unknown owners fail loudly instead of typing into the void.
// TestMuseTranscriptStreamStateFirstScreenPollEmits pins a real bug: the
// first-ever pollScreen call used to "prime" lastScreen without emitting, to
// avoid replaying the pre-turn pane. But a turn fast enough to finish (or
// render its reply) before museScreenStreamPollInterval's first tick made
// that discarded sample the ALREADY-COMPLETE pane, not the pre-turn one --
// permanently emptying the "main terminal" UI panel for that turn despite a
// live tmux session backing it (observed live 2026-09-11). The fix matches
// cursor-cli's streamCursorTerminalSnapshot exactly: lastScreen starts at
// its zero value with no priming step, so the very first capture always
// qualifies as "changed" and emits -- run()'s ctx.Done() final flush relies
// on this to publish a turn's only pane state when it never ticks even once.
func TestMuseTranscriptStreamStateFirstScreenPollEmits(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required: without it screen streaming can't be exercised")
	}
	session := "mlp-muse-test-final-screen-" + museRandomSessionSuffix()
	launch := exec.CommandContext(context.Background(), "tmux", "new-session", "-d", "-s", session,
		"-x", "80", "-y", "24", "sh", "-c", "echo settled-output; sleep 30")
	if out, err := launch.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})
	time.Sleep(300 * time.Millisecond) // let the pane settle before the state ever samples it

	st := newMuseTranscriptStreamState(filepath.Join(t.TempDir(), "session.jsonl"), session, false, true)
	ch := make(chan llmtypes.StreamChunk, 4)

	// The very first call, exactly as run()'s ctx.Done() final flush would
	// make it for a turn that finished before any periodic tick fired.
	st.pollScreen(ch)

	select {
	case chunk := <-ch:
		if chunk.Type != llmtypes.StreamChunkTypeTerminal {
			t.Fatalf("chunk type = %v, want StreamChunkTypeTerminal", chunk.Type)
		}
		if !strings.Contains(chunk.Content, "settled-output") {
			t.Fatalf("chunk content = %q, want it to contain the pane's actual output", chunk.Content)
		}
	default:
		t.Fatal("expected the first-ever poll to emit the pane's state instead of silently priming")
	}
}

// TestMuseEnterTookEffect pins the submit-verification guard against a real
// (non-muse) tmux pane: proof the pane changing at all after Enter is what
// distinguishes a keystroke the TUI acted on from one it silently swallowed
// -- observed live 2026-09-10 as a confirmed-visible "hi" that never
// submitted, with tmux reporting send-keys success the whole time.
func TestMuseEnterTookEffect(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required: without it this pane-behavior guard can't be exercised")
	}
	session := "mlp-muse-test-enter-effect-" + museRandomSessionSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	launch := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session, "-x", "80", "-y", "24", "sh")
	if out, err := launch.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})

	// Let the shell's own startup rendering settle first: a pane can still
	// change on its own for a beat right after launch (the same boot-render
	// race the rest of this package guards against), which would otherwise
	// read as a false "took effect".
	time.Sleep(500 * time.Millisecond)
	before, err := museTmuxCapturePane(ctx, session)
	if err != nil {
		t.Fatalf("capture pane: %v", err)
	}

	if museEnterTookEffect(ctx, session, before) {
		t.Fatal("expected no effect while the pane never changes (the swallowed-keystroke case)")
	}

	send := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "echo hello-from-test", "Enter")
	if out, err := send.CombinedOutput(); err != nil {
		t.Fatalf("tmux send-keys: %v\n%s", err, out)
	}
	if !museEnterTookEffect(ctx, session, before) {
		t.Fatal("expected the real output change to be detected")
	}
}

func TestSendMuseInteractiveInputNoSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := "mlp-nonexistent-" + museRandomSessionSuffix()
	if err := SendMuseInteractiveInput(ctx, owner, "hello"); err == nil {
		t.Fatal("expected error for unknown owner session")
	}
	if err := SendMuseInteractiveControlKey(ctx, owner, "Escape"); err == nil {
		t.Fatal("expected error for unknown owner session")
	}
}

// A new Muse session can take over a minute to accept its first prompt
// (66s and 135s measured on 2026-09-24/25). museSessionStartedSince is what
// lets the intake wait keep going once Muse has started a session for this
// workdir, instead of failing the turn while Muse goes on to run it.
func TestMuseSessionStartedSinceSeesAStartedSessionForTheWorkdir(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	day := filepath.Join(home, "muse", "sessions", "2026", "09", "25")
	write := func(id, workspaceRoot string, mod time.Time) {
		t.Helper()
		dir := filepath.Join(day, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "session.jsonl")
		row := `{"payload_type":"runtime.session.metadata","payload":{"workspace_root":` + strconv.Quote(workspaceRoot) + `}}`
		if err := os.WriteFile(path, []byte(row+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Now().Add(-time.Minute)

	if museSessionStartedSince(home, since, workdir) {
		t.Fatal("no session yet: must not extend the wait")
	}
	write("other-workdir", t.TempDir(), time.Now())
	write("before-turn", workdir, time.Now().Add(-time.Hour))
	if museSessionStartedSince(home, since, workdir) {
		t.Fatal("another workdir's session or one from before the turn must not count")
	}
	write("this-turn", workdir, time.Now())
	if !museSessionStartedSince(home, since, workdir) {
		t.Fatal("a session for this workdir started after the turn should count")
	}
	if museSessionStartedSince(home, since, "") {
		t.Fatal("without a workdir the session cannot be attributed")
	}
}

func TestMuseOversizedProjectRulesUseFullInlineFallback(t *testing.T) {
	workdir := t.TempDir()
	system := strings.Repeat("界", museProjectRulesMaxBytes/3) + "required final instruction"
	if cleanup, projected := projectMuseAgentsForTurn(workdir, []string{system}, true, false); cleanup != nil || projected {
		t.Fatal("oversized rules must use inline delivery")
	}
	if _, err := os.Stat(filepath.Join(workdir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("oversized projected file left behind: %v", err)
	}
	if got := museInlinePrompt([]string{system}, "hello"); got != system+"\n\nhello" {
		t.Fatal("inline fallback lost instructions")
	}
	// The operator's own rules count against the same startup limit.
	path := filepath.Join(workdir, "AGENTS.md")
	operator := strings.Repeat("x", museProjectRulesMaxBytes-100)
	if err := os.WriteFile(path, []byte(operator), 0o600); err != nil {
		t.Fatal(err)
	}
	if cleanup, projected := projectMuseAgentsForTurn(workdir, []string{strings.Repeat("y", 101)}, true, false); cleanup != nil || projected {
		t.Fatal("operator content was excluded from the startup budget")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != operator {
		t.Fatalf("oversized projection altered operator rules: %v", err)
	}
}

// The pane found on the owner's machine after Muse updated itself (1.4.8 -> 1.4.9): it blocked every launch for 90 s.
const museHooksReviewPane = "Muse Code updated 1.4.8-R6256.1 -> 1.4.9-R6297.1\nReview plugin hooks\n3 new or changed hooks need your trust.\n" +
	"Hooks run commands on your machine when their event matches.\nApproval follows the declaration, including after script updates.\n\n\n" +
	"⟩ 1. Review hooks\n  2. Trust all and continue\n  3. Continue without trusting\nUp/Down to choose, Enter to confirm. Esc continues without trusting.\n"

func TestMuseHooksReviewScreenIsRecognisedAndNamed(t *testing.T) {
	if !museHooksReviewScreen(museHooksReviewPane) {
		t.Fatal("the plugin hooks review screen was not recognised")
	}
	if museHooksReviewScreen("Muse Code 1.4.9\n❯ \nmeta · ~/work\nYou can review plugin hooks later.\n") {
		t.Fatal("an ordinary pane was taken for the hooks screen")
	}
	if got := museScreenCategory(museHooksReviewPane); got != "a plugin hooks review screen" {
		t.Fatalf("category = %q", got)
	}
	var e *llmerrors.Error
	if !errors.As(museHooksReviewError(), &e) || e.Kind != llmerrors.KindUserInputRequired {
		t.Fatal("an uncleared hooks screen must be a never-retry error")
	}
}

func TestMuseUnknownLaunchScreenIsRetriedOnlyOnce(t *testing.T) {
	pane := "Some new Muse screen nobody has seen\nPress any key\n"
	if museSameLaunchScreenTwice(pane) {
		t.Fatal("the first timeout on a screen must be retried")
	}
	if !museSameLaunchScreenTwice(pane) {
		t.Fatal("the second timeout on the same screen must end the turn")
	}
}
