package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestParseTabSelection(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantTab   string
		wantClear bool
		wantErr   bool
	}{
		{name: "list tabs", args: nil},
		{name: "select existing tab", args: []string{"t1"}, wantTab: "t1"},
		{name: "select labeled tab", args: []string{"daily-post"}, wantTab: "daily-post"},
		{name: "new labeled tab", args: []string{"new", "--label", "daily-post", "https://example.com"}, wantTab: "daily-post"},
		{name: "new labeled tab canonicalizes url before label", args: []string{"new", "https://example.com", "--label", "daily-post"}, wantTab: "daily-post"},
		{name: "new tab requires label", args: []string{"new", "https://example.com"}, wantErr: true},
		{name: "new tab requires absolute url", args: []string{"new", "--label", "daily-post", "example.com"}, wantErr: true},
		{name: "close selected tab", args: []string{"close", "daily-post"}, wantTab: "daily-post", wantClear: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTab, gotClear, err := parseTabSelection(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotTab != tt.wantTab || gotClear != tt.wantClear {
				t.Fatalf("parseTabSelection() = (%q, %v), want (%q, %v)", gotTab, gotClear, tt.wantTab, tt.wantClear)
			}
		})
	}
}

func TestCanonicalNewCDPTabArgsControlsArgumentOrder(t *testing.T) {
	request, err := parseNewCDPTabRequest([]string{"new", "https://example.com/path", "--label", "daily-post"})
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalNewCDPTabArgs(request)
	want := []string{"new", "--label", "daily-post", "https://example.com/path"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("canonical args = %#v, want %#v", got, want)
	}
}

func TestStripRedundantTabCommandArg(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
	}{
		{name: "tab command repeated before new", command: "tab", args: []string{"tab", "new", "--label", "daily-post", "https://example.com"}, want: []string{"new", "--label", "daily-post", "https://example.com"}},
		{name: "tab command repeated before select", command: "tab", args: []string{"tab", "daily-post"}, want: []string{"daily-post"}},
		{name: "multiple repeated tab tokens", command: "tab", args: []string{"tab", "tab", "new", "--label", "daily-post"}, want: []string{"new", "--label", "daily-post"}},
		{name: "single tab token remains selectable", command: "tab", args: []string{"tab"}, want: []string{"tab"}},
		{name: "other command unchanged", command: "snapshot", args: []string{"tab", "daily-post", "-i"}, want: []string{"tab", "daily-post", "-i"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripRedundantTabCommandArg(tt.command, tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNormalizeAgentBrowserCommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
	}{
		{name: "wait command repeated with duration", command: "wait", args: []string{"wait", "6s"}, want: []string{"6000"}},
		{name: "snapshot command repeated is stripped only", command: "snapshot", args: []string{"snapshot", "-i"}, want: []string{"-i"}},
		{name: "unscoped snapshot is unchanged", command: "snapshot", args: nil, want: nil},
		{name: "explicit snapshot depth is unchanged", command: "snapshot", args: []string{"--depth", "9", "-i"}, want: []string{"--depth", "9", "-i"}},
		{name: "selector scoped snapshot is unchanged", command: "snapshot", args: []string{"--selector", "#results"}, want: []string{"--selector", "#results"}},
		{name: "wait text option unchanged", command: "wait", args: []string{"--text", "Welcome"}, want: []string{"--text", "Welcome"}},
		{name: "single wait token unchanged", command: "wait", args: []string{"wait"}, want: []string{"wait"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAgentBrowserCommandArgs(tt.command, tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// An oversized snapshot used to return an error and discard the tree entirely,
// costing a round trip and forcing the agent to pick a narrower --selector
// without having seen the page (measured live 2026-08-17 on customer-login: 4
// blind retries at ~30.4k runes against the 24k cap). It now returns the head
// with an explicit incompleteness banner. The banner is the load-bearing part:
// a silently truncated accessibility tree is indistinguishable from a page
// where the element is genuinely absent, and a QA step must never record that
// negative from partial evidence.
func TestOversizedSnapshotReturnsTruncatedHeadWithIncompletenessBanner(t *testing.T) {
	e := &Executor{}

	short := "- button Submit"
	output := short
	handled, err := e.handleOversizedSnapshot(context.Background(), &output, false)
	if err != nil || handled {
		t.Fatalf("small snapshot: handled=%v err=%v, want handled=false err=nil", handled, err)
	}
	if output != short {
		t.Fatalf("small snapshot was modified: %q", output)
	}

	large := strings.Repeat("x", maxInlineSnapshotOutputRunes+100)
	output = large
	handled, err = e.handleOversizedSnapshot(context.Background(), &output, false)
	if err != nil || !handled {
		t.Fatalf("large snapshot: handled=%v err=%v, want handled=true err=nil", handled, err)
	}
	for _, want := range []string{
		"SNAPSHOT_TRUNCATED", "THIS TREE IS INCOMPLETE", "do NOT record it as absent",
		"--selector", "--compact", "--interactive", "--depth", fullSnapshotFlag,
		// PLAT-200: --depth only shrinks a deeply nested tree, not a wide/flat
		// one -- live-reproduced (a 500-sibling flat page returned a
		// byte-identical snapshot at --depth 2 vs. unlimited). The guidance
		// must warn the agent instead of listing --depth as an equal-weight
		// option, or it burns a retry on a no-op exactly as the customer-login
		// harness finding observed (byte-identical 30365-rune retries).
		"WIDE/FLAT page", "byte-identical result and", "only helps if the head below looks deeply nested, not wide",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("truncated snapshot missing %q:\n%s", want, output[:min(len(output), 600)])
		}
	}
	// The evidence itself must still be there, not just the banner.
	if !strings.Contains(output, strings.Repeat("x", 100)) {
		t.Fatal("truncated snapshot dropped the tree content entirely")
	}
}

// PLAT-200: the default truncated path used to discard the full tree with no
// way to recover it short of a second, full-cost snapshot call. When the
// caller has wired a spiller (which it only does after confirming, via the
// session's own granted folder guard, that the target is readable back --
// see workspace_browser_tools.go), the banner must point at the real saved
// path instead of only offering a re-run.
func TestOversizedSnapshotUsesConfiguredSpillerWhenAvailable(t *testing.T) {
	large := strings.Repeat("z", maxInlineSnapshotOutputRunes+100)

	t.Run("spiller succeeds", func(t *testing.T) {
		var gotContent string
		e := &Executor{
			SpillOversized: func(ctx context.Context, content string) (string, error) {
				gotContent = content
				return "Workflow/testing/tool_output_folder/agent_browser_snapshot_123.txt", nil
			},
		}
		output := large
		handled, err := e.handleOversizedSnapshot(context.Background(), &output, false)
		if err != nil || !handled {
			t.Fatalf("handled=%v err=%v, want handled=true err=nil", handled, err)
		}
		if gotContent != large {
			t.Fatalf("spiller did not receive the full untruncated tree (got %d chars, want %d)", len(gotContent), len(large))
		}
		if !strings.Contains(output, "Workflow/testing/tool_output_folder/agent_browser_snapshot_123.txt") {
			t.Fatalf("truncated snapshot did not surface the spilled path:\n%s", output[:min(len(output), 600)])
		}
		if !strings.Contains(output, "execute_shell_command") || !strings.Contains(output, "sed -n '1,240p'") {
			t.Fatalf("truncated snapshot did not advertise the available chunked-read recovery command:\n%s", output[:min(len(output), 600)])
		}
	})

	t.Run("no spiller configured falls back to the re-run option, no crash", func(t *testing.T) {
		e := &Executor{}
		output := large
		handled, err := e.handleOversizedSnapshot(context.Background(), &output, false)
		if err != nil || !handled {
			t.Fatalf("handled=%v err=%v, want handled=true err=nil", handled, err)
		}
		if !strings.Contains(output, fullSnapshotFlag) {
			t.Fatalf("missing fallback re-run option when no spiller is configured:\n%s", output[:min(len(output), 600)])
		}
	})

	t.Run("spiller error falls back to the re-run option, error is not propagated", func(t *testing.T) {
		e := &Executor{
			SpillOversized: func(ctx context.Context, content string) (string, error) {
				return "", errors.New("write denied: session has no tool_output_folder grant")
			},
		}
		output := large
		handled, err := e.handleOversizedSnapshot(context.Background(), &output, false)
		if err != nil || !handled {
			t.Fatalf("handled=%v err=%v, want handled=true err=nil (a spill failure must not fail the whole call)", handled, err)
		}
		if !strings.Contains(output, fullSnapshotFlag) {
			t.Fatalf("missing fallback re-run option when the spiller errors:\n%s", output[:min(len(output), 600)])
		}
	})
}

// --full-snapshot is the agent's explicit opt-in to pay the context cost.
func TestFullSnapshotFlagReturnsWholeTreeAndIsStrippedFromCLIArgs(t *testing.T) {
	e := &Executor{}
	large := strings.Repeat("y", maxInlineSnapshotOutputRunes+100)
	output := large
	handled, err := e.handleOversizedSnapshot(context.Background(), &output, true)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v, want handled=true err=nil", handled, err)
	}
	if !strings.Contains(output, "SNAPSHOT_FULL") {
		t.Fatalf("full snapshot missing SNAPSHOT_FULL marker:\n%s", output[:min(len(output), 300)])
	}
	if !strings.Contains(output, large) {
		t.Fatal("full snapshot did not return the complete tree")
	}
	if strings.Contains(output, "SNAPSHOT_TRUNCATED") {
		t.Fatal("full snapshot was truncated despite the explicit opt-in")
	}

	// agent-browser has no --full-snapshot flag; passing it through would fail
	// with "Unknown subcommand". It must be consumed by AgentWorks.
	found, cleaned := extractFullSnapshotArg([]string{"--cdp", "http://localhost:9222", fullSnapshotFlag, "tab", "t12"})
	if !found {
		t.Fatal("extractFullSnapshotArg did not detect the flag")
	}
	for _, arg := range cleaned {
		if arg == fullSnapshotFlag {
			t.Fatalf("flag survived into CLI args: %v", cleaned)
		}
	}
	if len(cleaned) != 4 {
		t.Fatalf("cleaned args = %v, want the other 4 preserved", cleaned)
	}

	if found, _ := extractFullSnapshotArg([]string{"tab", "t12"}); found {
		t.Fatal("extractFullSnapshotArg reported the flag when absent")
	}
}

func TestMissingCDPPageActionTabErrorShowsWaitRetry(t *testing.T) {
	t.Setenv("CDP_HOST", "localhost")
	err := missingCDPPageActionTabError(9222, "wait", []string{"wait", "6s"}, "Selected CDP tab: t12")
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	for _, want := range []string{
		`agent_browser(command="wait", args=["--cdp","http://localhost:9222","tab","<tab-id-or-label>","6000"])`,
		`agent_browser(command="status", args=[], session="<same-session>")`,
		"Do not put the command name inside args",
		"Selected CDP tab: t12",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestSelectedCDPTabMessageUsesKnownSelection(t *testing.T) {
	port := 22922
	t.Setenv("CDP_HOST", "localhost")
	owner := "owner-for-selected-message-test"
	clearCDPTabSelectionsForPort(port)
	t.Cleanup(func() { clearCDPTabSelectionsForPort(port) })

	if got := selectedCDPTabMessage(port, owner); !strings.Contains(got, "No selected CDP tab") {
		t.Fatalf("selectedCDPTabMessage() without selection = %q, want no-selection guidance", got)
	}

	setCDPTabSelection(port, owner, "upwork")
	setCDPTabAlias(port, owner, "upwork", "t23")
	got := selectedCDPTabMessage(port, owner)
	for _, want := range []string{"Selected CDP tab: t23", `args=["--cdp", "http://localhost:22922", "tab", "t23", "-i"]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("selectedCDPTabMessage() missing %q:\n%s", want, got)
		}
	}
}

func TestFallbackCDPTabListMessageUsesRememberedSelection(t *testing.T) {
	port := 23922
	owner := "owner-for-fallback-message-test"
	clearCDPTabSelectionsForPort(port)
	t.Cleanup(func() { clearCDPTabSelectionsForPort(port) })

	setCDPTabSelection(port, owner, "upwork")
	setCDPTabAlias(port, owner, "upwork", "t23")
	got := fallbackCDPTabListMessage(port, owner, errors.New("command timed out after 15s with a very long diagnostic message "+strings.Repeat("x", 400)))
	for _, want := range []string{
		"Could not refresh the real CDP tab list",
		"command timed out after 15s",
		"Selected CDP tab: t23",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallbackCDPTabListMessage() missing %q:\n%s", want, got)
		}
	}
	if len(got) > 800 {
		t.Fatalf("fallback message too large (%d bytes):\n%s", len(got), got)
	}
}

func TestCDPTabAliasCache(t *testing.T) {
	port := 20922
	owner := "owner-for-alias-test"
	clearCDPTabSelectionsForPort(port)
	t.Cleanup(func() { clearCDPTabSelectionsForPort(port) })

	output := `{"success":true,"data":{"tabs":[{"active":true,"label":"upwork_proposal","tabId":"t12","title":"Submit a Proposal","url":"https://www.upwork.com/nx/proposals/job/~02/apply/"}]},"error":null}`
	if got := findCDPTabID(output, "upwork_proposal"); got != "t12" {
		t.Fatalf("findCDPTabID() = %q, want t12", got)
	}
	if got := findCDPTabID(output, "t12"); got != "t12" {
		t.Fatalf("findCDPTabID(tab id) = %q, want t12", got)
	}
	direct := `{"success":true,"data":{"active":true,"label":"daily-post","tabId":"t14","url":"https://example.com/"}}`
	if got := findCDPTabID(direct, "daily-post"); got != "t14" {
		t.Fatalf("findCDPTabID(direct tab-new output) = %q, want t14", got)
	}

	setCDPTabAlias(port, owner, "upwork_proposal", "t12")
	setCDPTabSelection(port, owner, "upwork_proposal")
	setCDPActiveTab(port, "t12")
	clearCDPActiveTabForPort(port)
	if got := getCDPActiveTab(port); got != "" {
		t.Fatalf("active tab after daemon reset clear = %q, want empty", got)
	}
	if got := getCDPTabAlias(port, owner, "upwork_proposal"); got != "t12" {
		t.Fatalf("getCDPTabAlias() = %q, want t12", got)
	}
	if got := getCDPTabSelection(port, owner); got != "upwork_proposal" {
		t.Fatalf("selection after daemon reset clear = %q, want upwork_proposal", got)
	}
	if got := getCDPTabAlias(port, owner, "t12"); got != "" {
		t.Fatalf("tab ids should not resolve as aliases, got %q", got)
	}

	clearCDPTabAlias(port, owner, "upwork_proposal")
	if got := getCDPTabAlias(port, owner, "upwork_proposal"); got != "" {
		t.Fatalf("alias after clear = %q, want empty", got)
	}
}

func TestFormatCDPTabListForPromptCompactsRawJSON(t *testing.T) {
	output := `{"success":true,"data":{"tabs":[{"active":true,"label":"upwork_proposal","tabId":"t12","title":"` + strings.Repeat("Submit a Proposal ", 20) + `","url":"https://www.upwork.com/nx/proposals/job/~02/apply/?` + strings.Repeat("filter=abcdefghij&", 20) + `"},{"active":false,"label":"","tabId":"t13","title":"Inbox","url":"https://mail.example.com/inbox"}]},"error":null}`

	got := formatCDPTabListForPrompt(output)
	for _, want := range []string{
		"- t12 active",
		`label="upwork_proposal"`,
		`title="Submit a Proposal`,
		"- t13",
		`url="https://www.upwork.com/nx/proposals/job/~02/apply/"`,
		`url="https://mail.example.com/inbox"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	for _, raw := range []string{`"success"`, `"data"`, `"tabs"`, `"error"`} {
		if strings.Contains(got, raw) {
			t.Fatalf("summary should not include raw JSON key %q:\n%s", raw, got)
		}
	}
	if strings.Contains(got, "filter=abcdefghij") {
		t.Fatalf("summary should strip noisy URL query strings:\n%s", got)
	}
	if len(got) > 500 {
		t.Fatalf("summary too large (%d bytes):\n%s", len(got), got)
	}
}

func TestStripCDPArgs(t *testing.T) {
	got := stripCDPArgs([]string{"--cdp", "http://localhost:9222", "new", "--label", "daily-post"})
	want := []string{"new", "--label", "daily-post"}
	if len(got) != len(want) {
		t.Fatalf("stripCDPArgs() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stripCDPArgs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractInlineCDPTab(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantTab     string
		wantCleaned []string
		wantErr     bool
	}{
		{name: "tab prefix", args: []string{"tab", "t1", "-i"}, wantTab: "t1", wantCleaned: []string{"-i"}},
		{name: "tab flag", args: []string{"--tab", "upwork", "https://example.com"}, wantTab: "upwork", wantCleaned: []string{"https://example.com"}},
		{name: "missing tab", args: []string{"-i"}, wantCleaned: []string{"-i"}},
		{name: "tab missing value", args: []string{"tab"}, wantErr: true},
		{name: "multiple tabs", args: []string{"tab", "t1", "--tab", "t2"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTab, gotCleaned, err := extractInlineCDPTab(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotTab != tt.wantTab {
				t.Fatalf("tab = %q, want %q", gotTab, tt.wantTab)
			}
			if len(gotCleaned) != len(tt.wantCleaned) {
				t.Fatalf("cleaned len = %d, want %d (%v)", len(gotCleaned), len(tt.wantCleaned), gotCleaned)
			}
			for i := range tt.wantCleaned {
				if gotCleaned[i] != tt.wantCleaned[i] {
					t.Fatalf("cleaned[%d] = %q, want %q", i, gotCleaned[i], tt.wantCleaned[i])
				}
			}
		})
	}
}

func TestStripInlineTabFromOpenArgs(t *testing.T) {
	tab, cleaned, ok, err := stripInlineTabFromOpenArgs([]string{"tab", "t1", "https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || tab != "t1" || len(cleaned) != 1 || cleaned[0] != "https://example.com" {
		t.Fatalf("stripInlineTabFromOpenArgs() = (%q, %v, %v), want t1 URL true", tab, cleaned, ok)
	}

	_, cleaned, ok, err = stripInlineTabFromOpenArgs([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || len(cleaned) != 1 || cleaned[0] != "https://example.com" {
		t.Fatalf("expected URL-only open args to stay unchanged, got cleaned=%v ok=%v", cleaned, ok)
	}

	if _, _, _, err := stripInlineTabFromOpenArgs([]string{"tab", "t1"}); err == nil {
		t.Fatalf("expected malformed tab-prefixed open args to fail")
	}

	tab, cleaned, ok, err = stripInlineTabFromOpenArgs([]string{"t9", "https://example.com/path"})
	if err != nil {
		t.Fatalf("unexpected legacy tab-id normalization error: %v", err)
	}
	if !ok || tab != "t9" || len(cleaned) != 1 || cleaned[0] != "https://example.com/path" {
		t.Fatalf("legacy tab-id normalization = (%q, %v, %v), want t9 URL true", tab, cleaned, ok)
	}

	if _, _, _, err := stripInlineTabFromOpenArgs([]string{"t9"}); err == nil {
		t.Fatalf("expected bare tab id to be rejected as a navigation target")
	}
	if _, _, _, err := stripInlineTabFromOpenArgs([]string{"not-a-url"}); err == nil {
		t.Fatalf("expected relative navigation target to be rejected")
	}
}

func TestNormalizeOpenCommandArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantTab     string
		wantCleaned []string
		wantOK      bool
	}{
		{
			name:        "url only",
			args:        []string{"https://example.com"},
			wantCleaned: []string{"https://example.com"},
		},
		{
			name:        "inline tab",
			args:        []string{"tab", "t1", "https://example.com"},
			wantTab:     "t1",
			wantCleaned: []string{"https://example.com"},
			wantOK:      true,
		},
		{
			name:        "redundant command then inline tab",
			args:        []string{"open", "tab", "t1", "https://example.com"},
			wantTab:     "t1",
			wantCleaned: []string{"https://example.com"},
			wantOK:      true,
		},
		{
			name:        "redundant command then url",
			args:        []string{"open", "https://example.com"},
			wantCleaned: []string{"https://example.com"},
		},
		{
			name:        "legacy omitted tab marker",
			args:        []string{"t9", "https://example.com"},
			wantTab:     "t9",
			wantCleaned: []string{"https://example.com"},
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTab, gotCleaned, gotOK, err := normalizeOpenCommandArgs("open", tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotTab != tt.wantTab || gotOK != tt.wantOK {
				t.Fatalf("tab/ok = %q/%v, want %q/%v", gotTab, gotOK, tt.wantTab, tt.wantOK)
			}
			if len(gotCleaned) != len(tt.wantCleaned) {
				t.Fatalf("cleaned len = %d, want %d (%v)", len(gotCleaned), len(tt.wantCleaned), gotCleaned)
			}
			for i := range tt.wantCleaned {
				if gotCleaned[i] != tt.wantCleaned[i] {
					t.Fatalf("cleaned[%d] = %q, want %q", i, gotCleaned[i], tt.wantCleaned[i])
				}
			}
		})
	}
}

func TestExtractCDPArg(t *testing.T) {
	info, cleaned, err := extractCDPArg([]string{"--cdp", "http://localhost:9222", "tab", "t1", "-i"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.found || info.url != "http://localhost:9222" || info.port != 9222 {
		t.Fatalf("cdp info = %+v, want found localhost:9222 port 9222", info)
	}
	want := []string{"tab", "t1", "-i"}
	if len(cleaned) != len(want) {
		t.Fatalf("cleaned len = %d, want %d (%v)", len(cleaned), len(want), cleaned)
	}
	for i := range want {
		if cleaned[i] != want[i] {
			t.Fatalf("cleaned[%d] = %q, want %q", i, cleaned[i], want[i])
		}
	}

	info, cleaned, err = extractCDPArg([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.found || len(cleaned) != 1 || cleaned[0] != "https://example.com" {
		t.Fatalf("expected no cdp arg and unchanged args, got info=%+v cleaned=%v", info, cleaned)
	}
}

func TestCDPOwnerIDUsesStableBrowserSessionOverride(t *testing.T) {
	agentSession := "agent-session-for-cdp-owner-test"
	workflowSession := "workflow-session-for-cdp-owner-test"
	browserSession := "workflow-browser-stable-owner"
	common.SetSessionBrowserSessionID(agentSession, browserSession)
	defer common.ClearSessionShellConfig(agentSession)

	got := cdpOwnerID(workflowSession, agentSession, "shared-cdp-9222", "shared-cdp-9222")
	if got != browserSession {
		t.Fatalf("cdpOwnerID() = %q, want %q", got, browserSession)
	}
}

// PLAT-322. When no per-workflow browser session was ever bound (no
// SetSessionBrowserSessionID call for either candidate), cdpOwnerID must not
// fall back to returning the shared connection identity that every workflow
// on the same CDP port has by construction (sharedCDPSessionName) -- doing
// so pooled unrelated workflows' tabs and cleanup leases under one key.
func TestCDPOwnerIDNeverReturnsTheSharedConnectionIdentity(t *testing.T) {
	const sharedIdentity = "shared-cdp-9222"

	got := cdpOwnerID("", "", sharedIdentity, sharedIdentity)
	if got == sharedIdentity {
		t.Fatalf("cdpOwnerID() = %q, must never equal the shared connection identity %q", got, sharedIdentity)
	}
	if got == "" {
		t.Fatal("cdpOwnerID() = \"\", must return a non-empty identity even when unresolved")
	}
}

// Two calls with no resolvable identity are two different, genuinely
// unidentified callers as far as this function can tell -- they must not
// collide with each other either, or the same pooling bug just moves from
// the shared connection name to a different fixed fallback value.
func TestCDPOwnerIDUnidentifiedFallbacksDoNotCollideAcrossCalls(t *testing.T) {
	const sharedIdentity = "shared-cdp-9222"

	first := cdpOwnerID("", "", sharedIdentity, sharedIdentity)
	second := cdpOwnerID("", "", sharedIdentity, sharedIdentity)
	if first == second {
		t.Fatalf("two independently-unidentified callers resolved to the same owner %q, would still pool their tabs together", first)
	}
}

// A non-CDP caller's session is a genuine identity, not a shared connection
// name -- passing "" for sharedConnectionIdentity (as the real non-CDP call
// site does) must let it through the fallback exactly as before this fix.
func TestCDPOwnerIDStillUsesSessionAsFallbackOutsideCDPMode(t *testing.T) {
	got := cdpOwnerID("", "", "headless-session-42", "")
	if got != "headless-session-42" {
		t.Fatalf("cdpOwnerID() = %q, want the non-CDP session to still be used as a fallback identity", got)
	}
}

func TestCDPActiveTabTracksPortSelection(t *testing.T) {
	port := 9922
	clearCDPTabSelectionsForPort(port)
	t.Cleanup(func() { clearCDPTabSelectionsForPort(port) })

	if got := getCDPActiveTab(port); got != "" {
		t.Fatalf("active tab = %q, want empty", got)
	}

	setCDPActiveTab(port, "workflow-tab")
	if got := getCDPActiveTab(port); got != "workflow-tab" {
		t.Fatalf("active tab = %q, want workflow-tab", got)
	}

	clearCDPActiveTab(port, "other-tab")
	if got := getCDPActiveTab(port); got != "workflow-tab" {
		t.Fatalf("active tab = %q, want workflow-tab after clearing unrelated tab", got)
	}

	clearCDPActiveTab(port, "workflow-tab")
	if got := getCDPActiveTab(port); got != "" {
		t.Fatalf("active tab = %q, want empty after clearing active tab", got)
	}
}

func TestIsCDPTabActiveResolvesOwnerAlias(t *testing.T) {
	port := 19334
	ownerID := "workflow-session"
	t.Cleanup(func() { clearCDPTabSelectionsForPort(port) })

	setCDPTabAlias(port, ownerID, "social-media", "t7")
	setCDPActiveTab(port, "t7")

	if !isCDPTabActive(port, ownerID, "social-media") {
		t.Fatal("expected the selected label to match its active resolved tab id")
	}
	if isCDPTabActive(port, "other-workflow", "social-media") {
		t.Fatal("tab aliases must remain scoped to their owning workflow")
	}
}

func TestAcquireSharedCDPLockHonorsContext(t *testing.T) {
	port := 19922
	unlock, err := acquireSharedCDPLock(context.Background(), port)
	if err != nil {
		t.Fatalf("first acquireSharedCDPLock() error = %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if secondUnlock, err := acquireSharedCDPLock(ctx, port); err == nil {
		secondUnlock()
		t.Fatalf("second acquireSharedCDPLock() unexpectedly succeeded while lock was held")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquireSharedCDPLock() error = %v, want context deadline", err)
	}
}

func TestFindCDPRecordingTabPrefersFreshActiveTab(t *testing.T) {
	before := []cdpTabInfo{{TabID: "t1", Active: true, URL: "https://example.test"}}
	after := []cdpTabInfo{
		{TabID: "t1", Active: false, URL: "https://example.test"},
		{TabID: "t2", Active: true, URL: "https://example.test"},
	}

	got, err := findCDPRecordingTab(before, after, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "t2" {
		t.Fatalf("recording tab = %q, want t2", got)
	}
}

func TestCDPRecordingHandoffRequiresSnapshotAndRestoresOriginal(t *testing.T) {
	resetCDPRegistryForTest(t)
	const (
		port  = 19222
		owner = "recording-owner"
	)

	setCDPRecordingHandoff(port, owner, cdpRecordingHandoff{
		OriginalTab:   "t1",
		RecordingTab:  "t2",
		NeedsSnapshot: true,
	})
	if got := getCDPTabSelection(port, owner); got != "t2" {
		t.Fatalf("selection during recording = %q, want t2", got)
	}
	handoff, ok := getCDPRecordingHandoff(port, owner)
	if !ok || !handoff.NeedsSnapshot {
		t.Fatalf("handoff before snapshot = %#v, %v", handoff, ok)
	}

	markCDPRecordingSnapshotReady(port, owner)
	handoff, ok = getCDPRecordingHandoff(port, owner)
	if !ok || handoff.NeedsSnapshot {
		t.Fatalf("handoff after snapshot = %#v, %v", handoff, ok)
	}

	cleared, ok := clearCDPRecordingHandoff(port, owner)
	if !ok || cleared.OriginalTab != "t1" || cleared.RecordingTab != "t2" {
		t.Fatalf("cleared handoff = %#v, %v", cleared, ok)
	}
	if got := getCDPTabSelection(port, owner); got != "t1" {
		t.Fatalf("selection after stop = %q, want t1", got)
	}
}
