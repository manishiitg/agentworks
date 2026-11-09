package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// PLAT-856-542: a slow section cannot hold the other sections hostage; cached
// data is isolated by caller/chat and an explicit denial removes it.
func TestCrewPanelDeadlineAndCacheAdmission(t *testing.T) {
	cache := crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "crew", chat: "private-chat", authority: "current"}
	quick := crewPanelSource{id: "changes", title: "Changes", allowed: true, read: func(context.Context) (crewPanelSection, error) {
		return crewPanelSection{Rows: []crewPanelRow{{ID: "repo", Label: "Project"}}}, nil
	}}
	seed := cache.collect(t.Context(), key, []crewPanelSource{quick}, time.Second)
	if len(seed[0].Rows) != 1 {
		t.Fatal(seed)
	}
	slow := quick
	slow.read = func(ctx context.Context) (crewPanelSection, error) {
		<-ctx.Done()
		return crewPanelSection{}, ctx.Err()
	}
	// Empty sections must remain JSON arrays on a last-good response; null
	// breaks the panel's row iteration after a source timeout.
	empty := quick
	empty.id = "empty"
	empty.read = func(context.Context) (crewPanelSection, error) { return crewPanelSection{}, nil }
	cache.collect(t.Context(), key, []crewPanelSource{empty}, time.Second)
	empty.read = slow.read
	if got := cache.collect(t.Context(), key, []crewPanelSource{empty}, time.Millisecond); !got[0].Stale || got[0].Rows == nil {
		t.Fatal("empty last-good section lost its row array")
	}
	other := quick
	other.id = "dashboards"
	start := time.Now()
	out := cache.collect(t.Context(), key, []crewPanelSource{slow, other}, 20*time.Millisecond)
	if time.Since(start) > time.Second || !out[0].Stale || len(out[0].Rows) != 1 || out[1].Error != "" {
		t.Fatalf("fanout: %+v", out)
	}
	for _, changed := range []crewPanelCacheKey{{caller: "another", project: key.project, chat: key.chat, authority: key.authority}, {caller: key.caller, project: key.project, chat: "another-chat", authority: key.authority}, {caller: key.caller, project: key.project, chat: key.chat, authority: "revoked"}} {
		got := cache.collect(t.Context(), changed, []crewPanelSource{slow}, time.Millisecond)
		if len(got[0].Rows) != 0 {
			t.Fatal("cached private data crossed scope")
		}
	}
	uncacheable := slow
	uncacheable.noCache = true
	if got := cache.collect(t.Context(), key, []crewPanelSource{uncacheable}, time.Millisecond); len(got[0].Rows) != 0 || got[0].Stale {
		t.Fatal("Vault permissions require a fresh read")
	}
	cache.collect(t.Context(), key, []crewPanelSource{quick}, time.Second)
	denied := quick
	denied.read = func(context.Context) (crewPanelSection, error) {
		return crewPanelSection{}, crewPanelReadError{http.StatusForbidden}
	}
	if got := cache.collect(t.Context(), key, []crewPanelSource{denied}, time.Second); len(got[0].Rows) != 0 || got[0].Stale {
		t.Fatal("denial reused cache")
	}
	cache.collect(t.Context(), key, []crewPanelSource{quick}, time.Second)
	denied.allowed = false
	if got := cache.collect(t.Context(), key, []crewPanelSource{denied}, time.Second); len(got[0].Rows) != 0 {
		t.Fatal("section not admitted")
	}
}

// Exercise the actual HTTP route and normal Crew resolver, including a warm
// panel followed by revoked sharing. No extra knowledge of a private Crew is
// exposed by its panel or live-feed notice.
func TestCrewPanelHTTPAndNoticesUseCurrentCrewAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f.write(t, externalCommandCrewRoot+"/db/reports/example.html", "<html><title>Example result</title></html>")
	router := mux.NewRouter()
	router.HandleFunc("/api/crews/{project}/panel", f.api.handleCrewPanel).Methods("GET")
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	call := func(c *UserClaims) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/crews/team/panel", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, c))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := call(claims)
	if w.Code != 200 {
		t.Fatalf("owner panel: %d %s", w.Code, w.Body.String())
	}
	var result crewPanelReply
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range result.Sections {
		if section.ID == "dashboards" {
			for _, row := range section.Rows {
				found = found || row.Label == "Example result"
			}
		}
	}
	if !found {
		t.Fatalf("native catalog lost: %s", w.Body.String())
	}
	// The dashboard sits in the Share tree (owner, 5 Nov), opening the listed document.
	dashboardLine := false
	for _, section := range result.Sections {
		if section.ID == "access" && section.Access != nil {
			for _, line := range section.Access.Tree {
				dashboardLine = dashboardLine || (line.Kind == "dashboard" && strings.HasSuffix(line.Path, "example.html"))
			}
		}
	}
	if !dashboardLine {
		t.Fatalf("Share tree lacks the dashboard line: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "selected_secrets") || strings.Contains(w.Body.String(), "private-chat") {
		t.Fatal("non-display state leaked")
	}
	notice := livefeed.Notice{Kind: livefeed.CrewPanel, Project: "team"}
	if !f.api.crewPanelNoticeAllowed(t.Context(), claims, notice) {
		t.Fatal("owner notice refused")
	}
	other := &UserClaims{UserID: "other", Username: "other"}
	if !f.api.crewPanelNoticeAllowed(t.Context(), other, notice) {
		t.Fatal("shared Crew notice refused")
	}
	// Registry is the sharing authority; deny the same previously allowed user.
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	if got := call(other); got.Code != 403 {
		t.Fatalf("private Crew panel status %d", got.Code)
	}
	if f.api.crewPanelNoticeAllowed(t.Context(), other, notice) {
		t.Fatal("revoked Crew notice leaked")
	}
}

// PLAT-856-542: moving labels to the server must retain the next run beside
// the count, skipping past/paused jobs and using the earliest future job.
func TestCrewPanelAutomationSummaryKeepsNextRun(t *testing.T) {
	now := time.Now()
	rows := []crewPanelRow{
		{Destination: "schedules:schedules", Detail: "Paused"},
		{Destination: "schedules:schedules", Detail: "Past", nextRun: now.Add(-time.Hour)},
		{Destination: "schedules:schedules", Detail: "Later", nextRun: now.Add(2 * time.Hour)},
		{Destination: "schedules:schedules", Detail: "Next", nextRun: now.Add(time.Hour)},
	}
	got := crewPanelDetails([]crewPanelSection{{Rows: rows}})
	if got["automations"] != "4 · Next" {
		t.Fatalf("lost next run: %v", got)
	}
}

// The menu's Automations rows each say what is inside in plain words, and an empty list says so.
func TestCrewPanelAutomationKindSummaries(t *testing.T) {
	now := time.Now()
	sections := []crewPanelSection{
		{ID: "schedules", Rows: []crewPanelRow{
			{Destination: "schedules:schedules", Detail: "Next · 9 Nov 09:00 IST", nextRun: now.Add(time.Hour)},
			{Destination: "schedules:schedules", Detail: "Paused"},
		}},
		{ID: "watchers"},
		{ID: "webhooks", Error: "unavailable"},
	}
	got := crewPanelDetails(sections)
	if got["schedules:schedules"] != "1 on · 1 paused · next 9 Nov 09:00 IST" {
		t.Fatalf("schedules line: %q", got["schedules:schedules"])
	}
	if got["schedules:conditions"] != "None yet" {
		t.Fatalf("an empty loaded list should say so: %q", got["schedules:conditions"])
	}
	if got["schedules:triggers"] != "Could not load" {
		t.Fatalf("a list that failed to load must say so, not claim it is empty: %v", got)
	}
}

// The Automations menu: a workflow row and the Inbox line (On counts as an automation, Off does not), and the workflow
// line says steps and the newest run in plain words.
func TestCrewPanelWorkflowAndInboxSummaries(t *testing.T) {
	now := time.Now()
	done := now.Add(-2 * time.Hour)
	steps := map[string]todo_creation_human.PythonStepEvidence{"a": {}, "b": {}, "c": {}, "d": {}}
	line := crewPanelWorkflowLine([]RunFolderInfo{{Name: "run-2", Metadata: &RunMetadata{Status: "completed", CompletedAt: &done, PythonSteps: steps}}}, 0, now)
	if line != "4 steps · last run finished 2h ago" {
		t.Fatalf("workflow line: %q", line)
	}
	if got := crewPanelWorkflowLine(nil, 0, now); got != "Not run yet" {
		t.Fatalf("no runs: %q", got)
	}
	off := crewPanelDetails([]crewPanelSection{{ID: "inbox", Rows: []crewPanelRow{{Destination: "schedules:email", Detail: "Off"}}}})
	if off["schedules:email"] != "Off" || off["automations"] != "" {
		t.Fatalf("an Off inbox must be listed but not counted: %v", off)
	}
	on := crewPanelDetails([]crewPanelSection{{ID: "inbox", Rows: []crewPanelRow{{Destination: "schedules:email", Detail: "On · a@example.com · 0 messages this week", Connected: true}}}, {ID: "workflow", Rows: []crewPanelRow{{Destination: "workflow", Detail: line}}}})
	if on["automations"] != "On · a@example.com · 0 messages this week" || on["workflow"] != line {
		t.Fatalf("on inbox and workflow: %v", on)
	}
}

// The Inbox switch is owner-only, and the menu row follows it (Off, then On with the address).
func TestCrewInboxSwitchIsOwnerOnlyAndPanelFollows(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	env, ctx, root := agentEmailCrewFixture(t)
	env.mock.mu.Lock()
	env.mock.files["config/gmail-config.json"] = `{"connections":[{"id":"personal","personal":true,"owner_id":"owner","agent_inbox":true,"email":"owner@example.com","allow_read_access":true,"enabled":true}]}`
	env.mock.mu.Unlock()
	previous := services.GetGmailService()
	t.Cleanup(func() { services.SetGmailService(previous) })
	if _, err := services.InitGmailService(); err != nil {
		t.Fatal(err)
	}
	store, err := gmailinbound.Open(filepath.Join(t.TempDir(), "mail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	env.api.gmailInbound = &gmailinbound.Service{Store: store}
	post := func(c context.Context, enabled bool) int {
		body, _ := json.Marshal(map[string]any{"workspace_path": root, "enabled": enabled})
		r := httptest.NewRequest("POST", "/api/agent-email/inbox", strings.NewReader(string(body))).WithContext(c)
		w := httptest.NewRecorder()
		env.api.agentEmailInboxEnabledHTTP(w, r)
		return w.Code
	}
	other := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "other"})
	if code := post(other, false); code != http.StatusForbidden {
		t.Fatalf("a Run person turned the Crew's email off: %d", code)
	}
	if code := post(ctx, false); code != 200 {
		t.Fatalf("owner could not turn email off: %d", code)
	}
	if row, err := env.api.crewPanelInboxRow(ctx, root); err != nil || row.Detail != "Off" || row.Connected {
		t.Fatalf("row after off: %+v %v", row, err)
	}
	if code := post(other, true); code != http.StatusForbidden {
		t.Fatalf("a Run person turned the Crew's email on: %d", code)
	}
	if code := post(ctx, true); code != 200 {
		t.Fatalf("owner could not turn email on: %d", code)
	}
	if row, err := env.api.crewPanelInboxRow(ctx, root); err != nil || !row.Connected || row.Detail != "On · 0 messages this week" {
		t.Fatalf("row after on: %+v %v", row, err)
	}
}
