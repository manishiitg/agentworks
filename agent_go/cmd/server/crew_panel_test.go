package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

// PLAT-856-542: a slow section cannot hold the other sections hostage; cached
// data is isolated by caller and access, and an explicit denial removes it.
func TestCrewPanelDeadlineAndCacheAdmission(t *testing.T) {
	cache := crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "crew", authority: "current"}
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
	for _, changed := range []crewPanelCacheKey{{caller: "another", project: key.project, authority: key.authority}, {caller: key.caller, project: key.project, authority: "revoked"}} {
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
	// PLAT-746-513: pending reads back off and stop; stale failures cannot
	// restart a fast polling loop in every connected browser tab.
	for attempt, minimum := range []int64{100, 250, 500, 1000, 0} {
		reply := crewPanelReply{Sections: []crewPanelSection{{ID: "workflow", refreshAt: time.Now()}}}
		applyCrewPanelRefresh(&reply, strconv.Itoa(attempt))
		if attempt == 4 {
			if reply.RefreshAfterMS != 0 || reply.RefreshNote == "" {
				t.Fatal("pending panel retry budget was unlimited")
			}
		} else if reply.RefreshAfterMS < minimum || reply.RefreshAttempt != attempt+1 {
			t.Fatalf("pending panel retry backoff: %+v", reply)
		}
	}
	stale := crewPanelReply{Sections: []crewPanelSection{{ID: "workflow", Stale: true, refreshAt: time.Now().Add(-time.Second)}}}
	applyCrewPanelRefresh(&stale, "0")
	if stale.RefreshAfterMS < 2000 {
		t.Fatal("stale workflow snapshot requested a fast polling loop")
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
	// PLAT-746-513 / Codex1730: one workflow with three timers and a
	// webhook stays one workflow; starts keep their automation classification.
	f.api.scheduler = &SchedulerService{api: f.api}
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	child := crewExecutableBinding{ID: "panel-workflow", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/panel-workflow", Kind: "script"}
	if err := registerCrewExecutable(child); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Example workflow")
	manifest.ID, manifest.CreatedBy = child.ID, "owner"
	manifest.Schedules = []WorkflowSchedule{
		{ID: "timer-one", Name: "First timer", ScheduleType: "cron", CronExpression: "0 9 * * *", Timezone: "UTC", Enabled: true},
		{ID: "timer-two", Name: "Second timer", ScheduleType: "cron", CronExpression: "0 10 * * *", Timezone: "UTC", Enabled: false},
		{ID: "timer-three", Name: "Third timer", ScheduleType: "cron", CronExpression: "0 11 * * *", Timezone: "UTC", Enabled: true},
		{ID: "incoming", Name: "Incoming webhook", ScheduleType: "webhook", Enabled: true, WorkshopMode: "run", Webhook: &WorkflowWebhookConfig{AuthMode: "bearer", EncryptedSecret: "opaque-fixture"}},
	}
	if err := WriteWorkflowManifest(t.Context(), child.Root, manifest); err != nil {
		t.Fatal(err)
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/crews/{project}/panel", f.api.handleCrewPanel).Methods("GET")
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	call := func(c *UserClaims, attempt ...int) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/crews/team/panel", nil)
		if len(attempt) > 0 {
			r.URL.RawQuery = "refresh_attempt=" + strconv.Itoa(attempt[0])
		}
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
	counts := map[string]int{}
	var workflowDetail string
	for _, section := range result.Sections {
		switch section.ID {
		case "workflow":
			counts[section.ID] = len(section.Rows)
			if len(section.Rows) == 1 {
				workflowDetail = section.Rows[0].Detail
			}
		case "schedules", "webhooks":
			if section.Error != "" {
				t.Fatalf("trigger section failed: %+v", section)
			}
			counts[section.ID] = len(section.Rows)
			for _, row := range section.Rows {
				if row.Destination != "workflow" {
					t.Fatalf("workflow start lost its navigation: %+v", row)
				}
			}
		}
	}
	if counts["workflow"] != 1 || counts["schedules"] != 3 || counts["webhooks"] != 1 || result.Details["workflow"] != workflowDetail || result.Details["schedules:schedules"] != "2 on · 1 paused" || result.Details["schedules:triggers"] != "1 on" || result.Details["automations"] != "4" {
		t.Fatalf("workflow starts misclassified: counts=%v details=%v", counts, result.Details)
	}
	t.Logf("PANEL_TRIGGERS workflows=%d timers=%d webhooks=%d timers_detail=%q webhooks_detail=%q", counts["workflow"], counts["schedules"], counts["webhooks"], result.Details["schedules:schedules"], result.Details["schedules:triggers"])
	// A connected session notification inside the cache window must provide
	// a bounded trailing read, rather than freeze a completed run as running.
	setRun := func(status string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"status": status, "started_at": "2026-11-09T00:00:00Z"})
		if err := writeFileToWorkspace(t.Context(), child.Root+"/runs/run-live/run_metadata.json", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	expireWorkflow := func() {
		f.api.crewPanels.mu.Lock()
		defer f.api.crewPanels.mu.Unlock()
		for key, value := range f.api.crewPanels.entries {
			if key.project == "team" && key.section == "workflow" {
				value.at = time.Now().Add(-6 * time.Second)
				f.api.crewPanels.entries[key] = value
			}
		}
	}
	readPanel := func(attempt int) crewPanelReply {
		t.Helper()
		response := call(claims, attempt)
		var panel crewPanelReply
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &panel) != nil {
			t.Fatalf("lifecycle panel: %d %s", response.Code, response.Body)
		}
		return panel
	}
	setRun("running")
	expireWorkflow()
	if running := readPanel(0); !running.Busy || running.RefreshAfterMS != 0 {
		t.Fatalf("fresh running panel: %+v", running)
	}
	setRun("completed")
	publishSessionsChanged()
	cached := readPanel(0)
	if cached.Busy && (cached.RefreshAfterMS <= 0 || cached.RefreshAttempt != 1) {
		t.Fatalf("notification cached running without trailing read: %+v", cached)
	}
	// Advance the server snapshot clock, not a five-second test sleep.
	expireWorkflow()
	settled := readPanel(cached.RefreshAttempt)
	if settled.Busy || settled.RefreshAfterMS != 0 || !strings.Contains(settled.Details["workflow"], "last run finished") {
		t.Fatalf("trailing read did not settle: %+v", settled)
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
