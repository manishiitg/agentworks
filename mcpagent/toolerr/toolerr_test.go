package toolerr

import (
	"encoding/json"
	"strings"
	"testing"
)

// These structured payload facts are strong enough to override a successful
// outer transport. Arbitrary prose is deliberately tested separately below.
func TestSuspiciousToolResultCatchesStructuredMaskedFailures(t *testing.T) {
	cases := []struct {
		name   string
		result string
	}{
		{
			name:   "shell folder-guard denial under exit_code 0",
			result: `{"stdout":"","stderr":"access denied: shell command references absolute host path","exit_code":0}`,
		},
		{
			name:   "non-zero exit code inside a successful envelope",
			result: `{"stdout":"","stderr":"rg: no matches","exit_code":2}`,
		},
		{
			name:   "harness failure envelope returned as stdout",
			result: `tool execution failed: layer=custom_tool_handler tool=diff_patch_workspace_file session=abc`,
		},
		{
			name:   "JSON success flag contradicting the transport",
			result: `{"success": false, "error": "tools_unavailable: unknown=[foo]"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signal, suspicious := Suspicious(tc.result)
			if !suspicious {
				t.Fatalf("Suspicious(%q) = false, want true", tc.result)
			}
			if signal == "" {
				t.Fatal("suspicious result reported no signal; the log line would not say why it fired")
			}
		})
	}
}

func TestCanonicalFailureUnwrapsHighConfidenceNestedFailures(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "nested error prefix", text: `{"content":[{"type":"text","text":"{\"stdout\":\"ERROR: invalid API token\",\"stderr\":\"\",\"exit_code\":0}"}]}`},
		{name: "nonzero shell exit", text: `{"content":[{"text":"{\"stdout\":\"\",\"stderr\":\"bad query\",\"exit_code\":14}"}]}`},
		{name: "permission denial", text: `{"stdout":"","stderr":"sh: file: Operation not permitted","exit_code":0}`},
		{name: "HTTP status", text: `{"result":"{\"status_code\":403,\"error\":\"forbidden\"}"}`},
		{name: "explicit failure", text: `{"success":false,"error":"tool not available"}`},
		{name: "MCP isError", text: `{"content":[{"text":"denied"}],"isError":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if signal, failed := CanonicalFailure(tt.text); !failed {
				t.Fatalf("CanonicalFailure(%q) = (%q, false), want failure", tt.text, signal)
			}
		})
	}
}

func TestCanonicalFailureDoesNotPromoteDiscussionOrDomainData(t *testing.T) {
	tests := []string{
		`The review discusses ERROR: invalid API token from an older run.`,
		`{"result":{"findings":[{"status":"failed","detail":"historical record"}]}}`,
		`{"stdout":"the string permission denied appears in a report","stderr":"","exit_code":0}`,
		`{"success":true,"result":"all checks passed"}`,
	}
	for _, text := range tests {
		if signal, failed := CanonicalFailure(text); failed {
			t.Errorf("CanonicalFailure(%q) = (%q, true), want success", text, signal)
		}
	}
}

// TestCanonicalFailureDoesNotDecodeStdoutAsANestedEnvelope reproduces the
// confida-login live finding: `cat` on a file whose CONTENT happened to be a
// captured API error response was misclassified as the shell command itself
// failing, even though the real exit_code was 0. The bug was
// canonicalFailureValue JSON-decoding stdout's arbitrary text and treating
// whatever structure it happened to contain (here, {"success":false,...} —
// data about a captured API response, not about this shell command) as a
// nested transport signal.
func TestCanonicalFailureDoesNotDecodeStdoutAsANestedEnvelope(t *testing.T) {
	tests := []string{
		// A raw captured API error response, catted verbatim.
		`{"stdout":"{\"success\":false,\"error\":\"invalid token\"}","stderr":"","exit_code":0}`,
		// Same shape via stderr, in case a similar capture lands there.
		`{"stdout":"","stderr":"{\"status\":\"failed\",\"reason\":\"expired\"}","exit_code":0}`,
	}
	for _, text := range tests {
		if signal, failed := CanonicalFailure(text); failed {
			t.Errorf("CanonicalFailure(%q) = (%q, true), want success — the shell command's real exit_code was 0; the JSON inside stdout/stderr is arbitrary content, not a transport signal", text, signal)
		}
	}
}

func TestCanonicalFailureForToolSuppressesProblemReportingPayloads(t *testing.T) {
	if signal, failed := CanonicalFailureForTool("query_workflow_db", `[{"status":"failed"}]`); failed {
		t.Fatalf("query result classified as failure: %q", signal)
	}
	if _, failed := CanonicalFailureForTool("execute_shell_command", `{"exit_code":14,"stderr":"denied"}`); !failed {
		t.Fatal("shell failure was suppressed")
	}
}

// Over-matching is the accepted trade, but a few things must stay quiet or the
// marker becomes noise that nobody greps.
func TestSuspiciousToolResultStaysQuietOnOrdinarySuccess(t *testing.T) {
	for _, result := range []string{
		"",
		"   ",
		"ok",
		`{"success": true, "data": {"rows": 3}}`,
		`{"stdout":"hello\n","stderr":"","exit_code":0}`,
		`{"stdout":"rg found historical prose: forbidden, failed, not found","stderr":"","exit_code":0}`,
		`attached skill "agent-browser" not found; available skills: builder-reference`,
		"Traceback (most recent call last):\n  File \"historical-report.txt\", line 1",
		"Wrote 3 files to Workflow/demo/output",
		`{"exit_code": 0}`,
	} {
		if signal, suspicious := Suspicious(result); suspicious {
			t.Errorf("Suspicious(%q) = true (signal %q), want false", result, signal)
		}
	}
}

// exit_code parsing decides severity, so its edges are worth pinning: a leading
// zero is still zero, and a non-numeric value must not be read as a failure.
func TestNonZeroExitCodeSignal(t *testing.T) {
	cases := []struct {
		result string
		want   bool
	}{
		{result: `"exit_code":0`, want: false},
		{result: `"exit_code": 0`, want: false},
		{result: `"exit_code":00`, want: false},
		{result: `"exit_code":1`, want: true},
		{result: `"exit_code": 127`, want: true},
		{result: `"exit_code":"2"`, want: true},
		{result: `"exit_code":null`, want: false},
		{result: `"exit_code":`, want: false},
	}
	for _, tc := range cases {
		_, got := nonZeroExitCodeSignal(tc.result)
		if got != tc.want {
			t.Errorf("nonZeroExitCodeSignal(%q) = %v, want %v", tc.result, got, tc.want)
		}
	}
}

func TestTruncateToolResultForLogBoundsPayload(t *testing.T) {
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	got := TruncateForLog(string(long))
	if len(got) > 400+len("...(truncated)") {
		t.Fatalf("truncated length = %d, want bounded", len(got))
	}
	if short := TruncateForLog("small"); short != "small" {
		t.Fatalf("short result was altered: %q", short)
	}
}

// Domain records and documentation are semantic content, not transport state.
func TestSuspiciousForToolSuppressesProblemReportingTools(t *testing.T) {
	problemText := `{"findings":[{"title":"urls.md not found","detail":"permission denied"}]}`

	for _, tool := range []string{
		"read_skill", "get_pulse_finding_backlog", "get_pulse_module_state",
		"get_pulse_review_result", "query_workflow_db",
	} {
		if signal, suspicious := SuspiciousForTool(tool, problemText); suspicious {
			t.Errorf("SuspiciousForTool(%q, ...) = true (signal %q), want suppressed", tool, signal)
		}
	}

	// The same prose remains successful for unrelated tools too. Classification
	// is structural now, not a growing list of tool-specific lexical exemptions.
	for _, tool := range []string{"execute_shell_command", "agent_browser", "diff_patch_workspace_file", ""} {
		if signal, suspicious := SuspiciousForTool(tool, problemText); suspicious {
			t.Errorf("SuspiciousForTool(%q, ...) = true (signal %q), want content left to the agent", tool, signal)
		}
	}
}

// The masked failure that motivated the whole detector, captured verbatim from
// a live run: a tool failure re-wrapped as shell stdout under exit_code 0.
func TestSuspiciousForToolCatchesHarnessEnvelopeWrappedInShellSuccess(t *testing.T) {
	live := `{"stdout":"ERROR: tool execution failed: layer=custom_tool_handler tool=get_pulse_review_result session=schedule-cron--46a9b350: sql: no rows in result set","stderr":"","exit_code":0,"execution_time_ms":25}`
	signal, suspicious := SuspiciousForTool("execute_shell_command", live)
	if !suspicious {
		t.Fatal("live masked failure was not flagged")
	}
	if signal != "tool execution failure envelope" {
		t.Fatalf("signal = %q, want canonical harness envelope", signal)
	}
}

// The production case: a diff_patch denial whose args were logged head-first,
// so the long `diff` consumed the whole budget and `filepath` — the only field
// that explained the failure — never appeared.
func TestTruncateArgsForLogKeepsShortFieldsBesideLongOnes(t *testing.T) {
	longDiff := "--- /dev/null\n+++ b/x\n" + strings.Repeat("+padding line\n", 400)
	args := map[string]string{
		"diff":     longDiff,
		"filepath": "Workflow/social-media/builder/improve.html",
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}

	got := TruncateArgsForLog(string(encoded))

	if !strings.Contains(got, "Workflow/social-media/builder/improve.html") {
		t.Fatalf("filepath was lost to truncation; got:\n%s", got)
	}
	if !strings.Contains(got, "diff=") {
		t.Fatalf("diff key missing; got:\n%s", got)
	}
	if len(got) > argTotalBudget+128 {
		t.Fatalf("output length %d exceeds the budget", len(got))
	}

	// Head-first truncation is what this replaces: it drops filepath entirely.
	if strings.Contains(TruncateForLog(string(encoded)), "improve.html") {
		t.Fatal("plain truncation kept filepath; the regression this guards would be untestable")
	}
}

func TestTruncateArgsForLogHandlesNonObjectInput(t *testing.T) {
	for _, in := range []string{"", "   ", "not json at all", `["a","b"]`, `{`} {
		got := TruncateArgsForLog(in)
		if len(got) > 400+len("...(truncated)") {
			t.Fatalf("TruncateArgsForLog(%q) = %d bytes, want bounded", in, len(got))
		}
	}
	if got := TruncateArgsForLog(`{"a":1,"b":true,"c":null}`); !strings.Contains(got, "a=1") || !strings.Contains(got, "b=true") {
		t.Fatalf("non-string values not rendered: %s", got)
	}
}

func TestToolNameFromResult(t *testing.T) {
	cases := []struct {
		name      string
		result    string
		wantTool  string
		wantFound bool
	}{
		{
			name:      "the exact harness phrase observed across the 2026-08-04 scan",
			result:    `{"stdout":"ERROR: tool execution failed: layer=custom_tool_handler tool=record_pulse_worklist session=schedule-cron--8b09fba0_1785794415383828000: decisions[0] contains unknown field"}`,
			wantTool:  "record_pulse_worklist",
			wantFound: true,
		},
		{
			name:      "virtual_tool_handler layer, same phrase shape",
			result:    `{"stdout":"ERROR: tool execution failed: layer=virtual_tool_handler tool=get_api_spec session=msgseq-iteration-0"}`,
			wantTool:  "get_api_spec",
			wantFound: true,
		},
		{
			name:      "ordinary output that merely contains the word tool must not match",
			result:    `{"stdout":"the workflow tool ran and produced 3 findings", "exit_code": 0}`,
			wantFound: false,
		},
		{
			name:      "an unrelated tool= assignment outside the harness phrase must not match",
			result:    `{"stdout":"config: tool=legacy_probe deprecated, ignoring"}`,
			wantFound: false,
		},
		{
			name:      "empty result",
			result:    "",
			wantFound: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ToolNameFromResult(tc.result)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v (got %q)", found, tc.wantFound, got)
			}
			if found && got != tc.wantTool {
				t.Fatalf("tool = %q, want %q", got, tc.wantTool)
			}
		})
	}
}

// PLAT-127. agent_browser's wait action reports {"waited":"timeout"} when its
// poll deadline elapsed without the awaited condition firing -- a documented,
// successful outcome, not a failure. The literal payload shape below is from a
// live social-media run: 142 of 524 suspects on that one workflow were this
// exact field with "success":true alongside it, none a real failure.
func TestSuspiciousDoesNotFlagAgentBrowserWaitTimeoutOutcome(t *testing.T) {
	live := `{"success":true,"data":{"lifecycle":{"effectiveTargetedBrowser":false,"restartedBackground":false,"restoreStatus":"not_configured","reused":true,"saveStatus":"not_attempted"},"ms":1500,"waited":"timeout"},"error":null}`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want a documented wait outcome to stay quiet", signal)
	}
}

// Free-text timeout discussion is not a platform failure fact. A real timeout
// must arrive as an executor error or a canonical failure envelope.
func TestSuspiciousDoesNotPromoteTimeoutProseBesideAWaitOutcome(t *testing.T) {
	mixed := `{"success":true,"waited":"timeout","note":"connection to browser context deadline exceeded"}`
	if signal, suspicious := Suspicious(mixed); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want prose left to the agent", signal)
	}
}

// Spacing variant: "waited": "timeout" (with a space after the colon) is the
// same field mcpagent's own JSON marshaling can produce; the pattern must not
// be brittle to that.
func TestSuspiciousDoesNotFlagAgentBrowserWaitTimeoutOutcomeWithSpacing(t *testing.T) {
	live := `{"success": true, "waited": "timeout"}`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want a documented wait outcome to stay quiet", signal)
	}
}

// PLAT-127. get_route_description (no route_id: a full route-catalog dump)
// hit the exact shape that justified the original problemReportingTools list
// -- prose describing routes, not a tool failure. Measured live on
// social-media: 12 of 12 suspects, zero real failures.
func TestSuspiciousForToolSuppressesRouteDescriptionCatalogDump(t *testing.T) {
	problemText := `{"findings":[{"title":"urls.md not found","detail":"permission denied"}]}`
	if signal, suspicious := SuspiciousForTool("get_route_description", problemText); suspicious {
		t.Errorf("SuspiciousForTool(get_route_description, ...) = true (signal %q), want suppressed", signal)
	}
	if signal, suspicious := SuspiciousForTool("agent_browser", problemText); suspicious {
		t.Errorf("unrelated tool's domain prose was promoted to failure: %q", signal)
	}
}

// Live example, 2026-08-19: a successful notify_user send, relayed through
// execute_shell_command (a python/curl wrapper hitting the same endpoint),
// flagged purely because "failed":{} -- zero channels failed -- contains the
// word "failed". exit_code 0 and status "delivered" were both already present
// and ignored by the substring scan.
func TestSuspiciousDoesNotFlagNotifyUserEmptyFailedField(t *testing.T) {
	live := `{"stdout":"{\"delivered\":[\"gmail\"],\"failed\":{},\"skipped\":[\"whatsapp\"],\"status\":\"delivered\"}","stderr":"","exit_code":0,"execution_time_ms":1412}`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want a documented empty-failed-list outcome to stay quiet", signal)
	}
}

// Spacing variant of the precedent test above.
func TestSuspiciousDoesNotFlagNotifyUserEmptyFailedFieldWithSpacing(t *testing.T) {
	live := `{"delivered": ["gmail"], "failed": {}, "status": "delivered"}`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want a documented empty-failed-list outcome to stay quiet", signal)
	}
}

// Partial notification delivery is a domain result for the agent to interpret;
// the tool call itself completed and did not return a canonical failure fact.
func TestSuspiciousDoesNotPromoteNotifyUserPartialDelivery(t *testing.T) {
	live := `{"delivered":["gmail"],"failed":{"whatsapp":"connection refused"},"status":"partial"}`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want partial delivery left to the agent", signal)
	}
}

// Live tab list, 2026-08-19 (agent_browser, ICICI-BANK-PARSING run): a genuine
// third-party page happened to be titled "Sorry, you have been logged out !".
// Verified against pkg/browser/cdp_tabs.go's actual output format, not
// invented -- each line is `- <tabID>[ active][ label=%q][ title=%q][ url=%q]`.
func TestSuspiciousDoesNotFlagBrowserTabListPageTitleContent(t *testing.T) {
	live := "- t1 title=\"Shiv Nadar School - Student Portal\" url=\"https://portals.veracross.com/sns/student\"\n" +
		"- t2 title=\"Sorry, you have been logged out !\" url=\"https://cibnext.icici.bank.in/corp/Finacle;jsessionid=0000qz2f40aweClE7NNlLtplejY:1aeuba78B\"\n" +
		"- t4 title=\"Feedback page Income tax portal, government of India\" url=\"https://eportal.incometax.gov.in/iec/foservices/\"\n" +
		"- t10 active label=\"mahimakh_icici\" title=\"ICICI Bank- Net Banking\" url=\"https://retailnetbanking.icici.bank.in/bank-account/view-statements\""
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want arbitrary page title/url content to stay quiet", signal)
	}
}

// A title containing a literal double quote is %q-escaped by Go as \" --
// pkg/browser/cdp_tabs.go's own quoting behavior, confirmed directly
// (fmt.Printf("title=%q", ...)). The pattern must still match the whole
// value rather than stopping at the escaped quote's trailing ".
func TestSuspiciousDoesNotFlagBrowserTabTitleWithEscapedQuote(t *testing.T) {
	live := `- t1 title="Sorry, \"not found\" - please try again" url="https://example.com/"`
	if signal, suspicious := Suspicious(live); suspicious {
		t.Errorf("Suspicious(...) = true (signal %q), want an escaped quote inside the title to not break the match", signal)
	}
}

// A real agent_browser failure (an element genuinely not found by a click/find
// action, not tab-list page metadata) must still be caught -- the suppression
// is scoped to the title=/url= attribute shape, not the word "not found"
// generally.
func TestSuspiciousStillFlagsARealBrowserElementNotFoundError(t *testing.T) {
	live := `{"success":false,"error":"element not found: selector '#submit-button' matched 0 nodes"}`
	if _, suspicious := Suspicious(live); !suspicious {
		t.Error("Suspicious(...) = false, want a real element-not-found error outside title=/url= to still be flagged")
	}
}

func TestCanonicalFailureRecognisesErrorEnvelopeMessageKeys(t *testing.T) {
	denied := `{"error":{"errorMessage":"Built-in filesystem/shell/edit/search/delegation tools are disabled in this session by the orchestrator."}}`
	signal, failed := CanonicalFailure(denied)
	if !failed {
		t.Fatalf("error envelope with errorMessage must classify as a failure: %s", denied)
	}
	if signal != "error.message" {
		t.Fatalf("unexpected signal %q", signal)
	}
	// The glob shape kept working.
	if _, failed := CanonicalFailure(`{"error":{"error":"Built-in tools are disabled"}}`); !failed {
		t.Fatal("nested error string must still classify as a failure")
	}
	// A domain record carrying a message under a non-error key stays quiet.
	if _, failed := CanonicalFailure(`{"result":{"message":"3 files matched","status":"ok"}}`); failed {
		t.Fatal("result.message must not be promoted to a failure")
	}
	// An empty error envelope is not a signal on its own.
	if _, failed := CanonicalFailure(`{"error":{"errorMessage":""}}`); failed {
		t.Fatal("empty errorMessage must not classify as a failure")
	}
}

// A failed secret call must not write the secret into the log.
func TestTruncateArgsForLogRedactsSecretValues(t *testing.T) {
	for _, in := range []string{
		`{"operation":"set_value","values":{"API_KEY":"s3cr3t-value"}}`,
		`{"operation":"set_secret","name":"API_KEY","value":"s3cr3t-value"}`,
		`{"name":"example","api_key":"s3cr3t-value"}`,
		`{"arguments":{"credential":"s3cr3t-value"}}`,
		`{"name":"example","client_id":"app","client_secret":"s3cr3t-value"}`,
		`{"workflow_id":"w","secrets":{"set":{"API_KEY":"s3cr3t-value"}}}`,
		`{"api_key":"s3cr3t-value`,
		`{"arguments":{"credential":"s3cr3t-value"`,
		`{"client_secret":"s3cr3t-value",`,
		`{"value":"s3cr3t-value`,
		`{"values":{"API_KEY":"s3cr3t-value"}`,
	} {
		out := TruncateArgsForLog(in)
		if strings.Contains(out, "s3cr3t-value") || !strings.Contains(out, "[redacted]") {
			t.Errorf("secret value reached the log line or the marker is missing: %s", out)
		}
	}
	if out := TruncateArgsForLog(`{"command":"ls","name":"API_KEY"}`); !strings.Contains(out, "API_KEY") || strings.Contains(out, "redacted") {
		t.Errorf("unrelated arguments were changed: %s", out)
	}
}
