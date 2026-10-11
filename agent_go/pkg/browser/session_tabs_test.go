package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// fakeTabBrowser is a stateful stand-in for one agent-browser session: it
// keeps real tab IDs and a current tab, and records which tab each page
// command ran against.
type fakeTabBrowser struct {
	mu      sync.Mutex
	tabs    []cdpTabInfo
	active  string
	next    int
	calls   []string // "<command> <args...>" after --session <name>
	pageRun []fakePageRun
}

type fakePageRun struct {
	command string
	tab     string
	url     string
}

func newFakeTabBrowser() *fakeTabBrowser {
	// A freshly launched managed browser has one untouched blank tab.
	return &fakeTabBrowser{tabs: []cdpTabInfo{{TabID: "t1", URL: "about:blank"}}, active: "t1", next: 2}
}

func (f *fakeTabBrowser) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if strings.Contains(req.Command, "--cdp") {
			t.Errorf("headless per-conversation tab call must not pass --cdp: %s", req.Command)
		}
		_, rest, ok := strings.Cut(req.Command, " --session ")
		if !ok {
			t.Errorf("command without --session: %s", req.Command)
		}
		fields := strings.Fields(rest)
		args := make([]string, 0, len(fields))
		for _, field := range fields[1:] { // fields[0] is the session name
			if field == "--json" {
				continue
			}
			args = append(args, strings.Trim(field, "'"))
		}
		stdout, exit := f.run(args)
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: stdout, ExitCode: exit}})
	}
}

func (f *fakeTabBrowser) find(ref string) int {
	for i, tab := range f.tabs {
		if tab.TabID == ref || (tab.Label != "" && tab.Label == ref) {
			return i
		}
	}
	return -1
}

func (f *fakeTabBrowser) run(args []string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	tabJSON := func(tab cdpTabInfo) string {
		raw, _ := json.Marshal(map[string]interface{}{"success": true, "data": map[string]interface{}{"tabId": tab.TabID, "label": tab.Label, "url": tab.URL, "active": tab.TabID == f.active}})
		return string(raw)
	}
	if len(args) == 0 {
		return `{"success":false}`, 1
	}
	switch args[0] {
	case "tab":
		switch {
		case len(args) == 1:
			listed := make([]cdpTabInfo, len(f.tabs))
			for i, tab := range f.tabs {
				tab.Active = tab.TabID == f.active
				listed[i] = tab
			}
			raw, _ := json.Marshal(map[string]interface{}{"success": true, "data": map[string]interface{}{"tabs": listed}})
			return string(raw), 0
		case args[1] == "new":
			tab := cdpTabInfo{TabID: fmt.Sprintf("t%d", f.next), URL: "about:blank"}
			f.next++
			for i := 2; i < len(args); i++ {
				if args[i] == "--label" && i+1 < len(args) {
					tab.Label = args[i+1]
					i++
				} else {
					tab.URL = args[i]
				}
			}
			f.tabs = append(f.tabs, tab)
			f.active = tab.TabID
			return tabJSON(tab), 0
		case args[1] == "close":
			ref := f.active
			if len(args) > 2 {
				ref = args[2]
			}
			i := f.find(ref)
			if i < 0 {
				return `{"success":false,"error":"tab not found"}`, 1
			}
			closed := f.tabs[i]
			f.tabs = append(f.tabs[:i], f.tabs[i+1:]...)
			if f.active == closed.TabID {
				f.active = ""
				if len(f.tabs) > 0 {
					f.active = f.tabs[0].TabID
				}
			}
			return tabJSON(closed), 0
		default:
			i := f.find(args[1])
			if i < 0 {
				return `{"success":false,"error":"tab not found"}`, 1
			}
			f.active = f.tabs[i].TabID
			return tabJSON(f.tabs[i]), 0
		}
	case "close":
		f.tabs = nil
		f.active = ""
		return `{"success":true}`, 0
	default:
		run := fakePageRun{command: args[0], tab: f.active}
		if i := f.find(f.active); i >= 0 && isBrowserOpenCommand(args[0]) && len(args) > 1 {
			f.tabs[i].URL = args[1]
			run.url = args[1]
		}
		f.pageRun = append(f.pageRun, run)
		return `{"success":true}`, 0
	}
}

func (f *fakeTabBrowser) snapshot() ([]string, []fakePageRun, []cdpTabInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]fakePageRun(nil), f.pageRun...), append([]cdpTabInfo(nil), f.tabs...)
}

const testSharedCrewBrowser = "crew-shared-browser"

func setupSessionTabTest(t *testing.T) (*fakeTabBrowser, *Executor) {
	t.Helper()
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	clearSessionTabScope(testSharedCrewBrowser)
	t.Cleanup(func() {
		clearSessionTabScope(testSharedCrewBrowser)
		GetSessionTracker().Remove(testSharedCrewBrowser)
	})
	fake := newFakeTabBrowser()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	return fake, NewExecutor(NewClient(server.URL))
}

func runAs(t *testing.T, e *Executor, owner, command string, args ...string) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, owner)
	return e.HandleAgentBrowser(ctx, map[string]interface{}{"command": command, "session": testSharedCrewBrowser, "args": args})
}

func mustRunAs(t *testing.T, e *Executor, owner, command string, args ...string) string {
	t.Helper()
	out, err := runAs(t, e, owner, command, args...)
	if err != nil {
		t.Fatalf("%s %s %v: %v", owner, command, args, err)
	}
	return out
}

func TestSessionTabsGiveEachConversationItsOwnTab(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	const crewChat, askCall = "work:project:crew-1", "product-1a23e2fe"

	mustRunAs(t, e, crewChat, "open", "https://crew.example/")
	mustRunAs(t, e, askCall, "open", "https://ask.example/")
	mustRunAs(t, e, crewChat, "snapshot", "-i")
	mustRunAs(t, e, askCall, "click", "@e3")
	mustRunAs(t, e, crewChat, "get", "url")

	calls, runs, tabs := fake.snapshot()
	if len(runs) != 5 {
		t.Fatalf("page commands = %d, want 5 (%v)", len(runs), calls)
	}
	crewTab, askTab := runs[0].tab, runs[1].tab
	if crewTab == "" || askTab == "" || crewTab == askTab {
		t.Fatalf("conversations must get distinct tabs, got crew=%q ask=%q", crewTab, askTab)
	}
	// The first conversation adopts the fresh browser's only blank tab.
	if crewTab != "t1" {
		t.Fatalf("first conversation should adopt the untouched blank tab t1, got %q", crewTab)
	}
	for i, want := range []string{crewTab, askTab, crewTab, askTab, crewTab} {
		if runs[i].tab != want {
			t.Fatalf("page command %d (%s) ran on %q, want %q", i, runs[i].command, runs[i].tab, want)
		}
	}
	// Every page command is immediately preceded by selecting its owner's tab.
	pageIdx := 0
	for i, call := range calls {
		fields := strings.Fields(call)
		if fields[0] == "tab" {
			continue
		}
		want := "tab " + runs[pageIdx].tab
		if i == 0 || calls[i-1] != want {
			t.Fatalf("call %d %q was preceded by %q, want %q (calls: %v)", i, call, calls[max(i-1, 0)], want, calls)
		}
		pageIdx++
	}
	// Each tab kept its own page.
	urls := map[string]string{}
	for _, tab := range tabs {
		urls[tab.TabID] = tab.URL
	}
	if urls[crewTab] != "https://crew.example/" || urls[askTab] != "https://ask.example/" {
		t.Fatalf("one conversation moved another's page: %v", urls)
	}
}

func TestHeadlessScreenshotUsesItsTrustedBrowserArtifactScope(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	const owner = "preview-artifact-regression"
	common.SetSessionBrowserNamespace(owner, "workflow-bbbbbbbbbbbbbbbb")
	common.SetSessionFolderGuard(owner, []string{"Workflow/demo"}, []string{"Workflow/demo"})
	session := common.SandboxBrowserSession(owner)
	staging := browserconfig.ArtifactDirForSession(session)
	t.Cleanup(func() {
		common.ClearSessionShellConfig(owner)
		clearSessionTabScope(session)
		GetSessionTracker().Remove(session)
		_ = os.RemoveAll(staging)
		browserconfig.RemoveEmptySessionSocketDirs(session)
	})
	// The model-supplied session must not select a different artifact scope.
	mustRunAs(t, e, owner, "screenshot", "Workflow/demo/db/reports/preview/dark.png")
	calls, _, _ := fake.snapshot()
	for _, call := range calls {
		if strings.HasPrefix(call, "screenshot ") {
			if !strings.Contains(call, staging+"/") || strings.Contains(call, browserArtifactStagingDir()+"/") {
				t.Fatalf("screenshot used an unshared or untrusted staging directory: %s", call)
			}
			return
		}
	}
	t.Fatalf("screenshot did not reach the browser: %v", calls)
}

func TestSessionTabsCloseOnlyClosesOwnTab(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	mustRunAs(t, e, "conv-a", "open", "https://a.example/")
	mustRunAs(t, e, "conv-b", "open", "https://b.example/")
	_, runs, _ := fake.snapshot()
	tabA, tabB := runs[0].tab, runs[1].tab

	out := mustRunAs(t, e, "conv-a", "close")
	if !strings.Contains(out, "stays open") {
		t.Fatalf("scoped close result = %q", out)
	}
	calls, _, tabs := fake.snapshot()
	for _, call := range calls {
		if call == "close" {
			t.Fatalf("conversation A's close closed the whole shared browser: %v", calls)
		}
	}
	if len(tabs) != 1 || tabs[0].TabID != tabB {
		t.Fatalf("after A's close tabs = %v, want only B's %q", tabs, tabB)
	}
	if !GetSessionTracker().HasSession(testSharedCrewBrowser) {
		t.Fatal("shared browser was untracked although B still uses it")
	}

	mustRunAs(t, e, "conv-b", "snapshot")
	_, runs, _ = fake.snapshot()
	if last := runs[len(runs)-1]; last.tab != tabB {
		t.Fatalf("B's command ran on %q after A's close, want %q", last.tab, tabB)
	}

	// A comes back: it gets a fresh tab, never B's.
	mustRunAs(t, e, "conv-a", "open", "https://a2.example/")
	_, runs, _ = fake.snapshot()
	if last := runs[len(runs)-1]; last.tab == tabB || last.tab == tabA || last.tab == "" {
		t.Fatalf("A reopened on %q (B=%q, old A=%q)", last.tab, tabB, tabA)
	}

	// Closing another conversation's tab explicitly is refused.
	if _, err := runAs(t, e, "conv-a", "tab", "close", tabB); err == nil || !strings.Contains(err.Error(), "another conversation") {
		t.Fatalf("closing B's tab from A = %v, want refusal", err)
	}
}

func TestSessionTabsResetRefusedWhileAnotherConversationIsActive(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	mustRunAs(t, e, "conv-a", "open", "https://a.example/")
	mustRunAs(t, e, "conv-b", "open", "https://b.example/")

	_, err := runAs(t, e, "conv-a", "reset")
	if err == nil || !strings.Contains(err.Error(), "conv-b") || !strings.Contains(err.Error(), "refusing to reset") {
		t.Fatalf("reset with another active conversation = %v, want refusal naming conv-b", err)
	}
	_, _, tabs := fake.snapshot()
	if len(tabs) != 2 {
		t.Fatalf("refused reset changed the browser: %v", tabs)
	}
	if getScopedTabSelection(sessionTabScope(testSharedCrewBrowser), sessionTabOwnerKey("conv-b")) == "" {
		t.Fatal("refused reset wiped conversation B's tab state")
	}
}

func TestSessionTabsReplaceVanishedTabWithNote(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	mustRunAs(t, e, "conv-a", "open", "https://a.example/")
	mustRunAs(t, e, "conv-b", "open", "https://b.example/")
	_, runs, _ := fake.snapshot()
	tabA, tabB := runs[0].tab, runs[1].tab

	// The page closes A's tab by itself.
	fake.mu.Lock()
	fake.tabs = []cdpTabInfo{fake.tabs[fake.find(tabB)]}
	fake.active = tabB
	fake.mu.Unlock()

	out := mustRunAs(t, e, "conv-a", "snapshot")
	if !strings.Contains(out, "AGENTWORKS_BROWSER_TAB") || !strings.Contains(out, tabA) {
		t.Fatalf("missing vanished-tab note: %q", out)
	}
	_, runs, _ = fake.snapshot()
	if last := runs[len(runs)-1]; last.tab == tabB || last.tab == tabA {
		t.Fatalf("A's command ran on %q, want a fresh tab (not B's %q)", last.tab, tabB)
	}
}

func TestReleaseSessionTabOwnerClosesTabOnlyWhileOthersRemain(t *testing.T) {
	fake, e := setupSessionTabTest(t)
	mustRunAs(t, e, "conv-a", "open", "https://a.example/")
	mustRunAs(t, e, "conv-b", "open", "https://b.example/")
	_, runs, _ := fake.snapshot()
	tabB := runs[1].tab

	ReleaseSessionTabOwner("conv-a", e.Client)
	_, _, tabs := fake.snapshot()
	if len(tabs) != 1 || tabs[0].TabID != tabB {
		t.Fatalf("after A ended tabs = %v, want only B's", tabs)
	}

	// B is now the last user: its page is kept and handed to the next
	// conversation (for example the next workflow step).
	ReleaseSessionTabOwner("conv-b", e.Client)
	_, _, tabs = fake.snapshot()
	if len(tabs) != 1 {
		t.Fatalf("last conversation's tab should be kept, tabs = %v", tabs)
	}
	mustRunAs(t, e, "conv-c", "snapshot")
	_, runs, _ = fake.snapshot()
	if last := runs[len(runs)-1]; last.tab != tabB {
		t.Fatalf("next conversation ran on %q, want handed-off tab %q", last.tab, tabB)
	}
}
