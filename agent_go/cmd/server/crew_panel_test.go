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
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacereads"
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
	if got := cache.collect(t.Context(), key, []crewPanelSource{denied}, time.Second); len(got[0].Rows) != 0 || got[0].Stale || got[0].Error != "" {
		t.Fatal("denial reused cache or asked to try again") // a 403 every panel read looped "Try again" (18 Nov)
	}
	cache.collect(t.Context(), key, []crewPanelSource{quick}, time.Second)
	denied.allowed = false
	if got := cache.collect(t.Context(), key, []crewPanelSource{denied}, time.Second); len(got) != 0 {
		t.Fatal("a section not allowed here is sent, so the menu shows an empty row for it (Webhooks on the Mac)")
	}
	// #3206: a stamped section is read again only when its stamp changes (a schedule edit), never served across
	// another caller's scope.
	reads, version := 0, "1/1"
	stamped := quick
	stamped.id = "schedules"
	stamped.stamp = func(context.Context) (string, bool) { return version, true }
	stamped.read = func(context.Context) (crewPanelSection, error) {
		reads++
		return crewPanelSection{Rows: []crewPanelRow{{ID: "schedule:" + version}}}, nil
	}
	cache.collect(t.Context(), key, []crewPanelSource{stamped}, time.Second)
	cache.collect(t.Context(), key, []crewPanelSource{stamped}, time.Second)
	if reads != 1 {
		t.Fatalf("unchanged stamp re-read the section %d times", reads)
	}
	version = "2/1"
	if got := cache.collect(t.Context(), key, []crewPanelSource{stamped}, time.Second); reads != 2 || got[0].Rows[0].ID != "schedule:2/1" {
		t.Fatalf("a changed stamp served the old section: reads=%d %+v", reads, got)
	}
	cache.collect(t.Context(), crewPanelCacheKey{caller: "another", project: key.project, authority: key.authority}, []crewPanelSource{stamped}, time.Second)
	if reads != 3 {
		t.Fatal("a stamped snapshot crossed caller scope")
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
	shareCrewBotForTest(t, externalCommandCrewRoot, "other")
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
	if counts["workflow"] != 1 || counts["schedules"] != 3 || counts["webhooks"] != 1 || result.Details["workflow"] != workflowDetail+" · 1 webhook" || result.Details["schedules:schedules"] != "2 on · 1 paused" || result.Details["schedules:triggers"] != "1 on" || result.Details["automations"] != "4" {
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
	stopCrewBotForTest(t, externalCommandCrewRoot)
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
		// A Watcher is a kind of schedule (owner, 19 Nov): it counts in the Schedules line.
		{ID: "watchers", Rows: []crewPanelRow{{Destination: "schedules:conditions", Detail: "On"}}},
		{ID: "webhooks", Error: "unavailable"},
	}
	got := crewPanelDetails(sections)
	if got["schedules:schedules"] != "2 on · 1 paused · next 9 Nov 09:00 IST" {
		t.Fatalf("schedules line: %q", got["schedules:schedules"])
	}
	if empty := crewPanelDetails([]crewPanelSection{{ID: "schedules"}, {ID: "watchers"}}); empty["schedules:schedules"] != "None yet" {
		t.Fatalf("an empty loaded list should say so: %q", empty["schedules:schedules"])
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

// PLAT-746-840: a known Crew's activity must not invalidate every expanded Crew.
func TestSessionNoticeScopesKnownCrewAndKeepsUnknownRefresh(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	f.api.activeSessions = map[string]*ActiveSessionInfo{"known": {WorkspacePath: externalCommandCrewRoot}}
	sub := livefeed.Default.Subscribe()
	defer livefeed.Default.Unsubscribe(sub)
	f.api.publishSessionChanged("known")
	notices, resync := sub.Drain()
	if resync || len(notices) != 1 || notices[0].Kind != livefeed.Sessions || notices[0].Project != "team" {
		t.Fatalf("known Crew notice: %+v resync=%v", notices, resync)
	}
	f.api.publishSessionChanged("unknown")
	notices, resync = sub.Drain()
	if resync || len(notices) != 1 || notices[0].Project != "" || notices[0].Kind != livefeed.Sessions {
		t.Fatalf("unknown session lost broad refresh: %+v resync=%v", notices, resync)
	}
}

// A background section shows its last snapshot at once once it is older than refreshFor, reads the new one off the
// request, and never starts a second read while one is running (the Git status over every checkout took ~5 s per panel read).
func TestCrewPanelBackgroundSectionServesLastSnapshotWhileRefreshing(t *testing.T) {
	cache := &crewPanelCache{}
	key := crewPanelCacheKey{caller: "u", project: "p", authority: "a"}
	release, reads := make(chan struct{}), make(chan int, 8)
	label := "old"
	source := crewPanelSource{id: "changes", title: "Changes", allowed: true, refreshFor: time.Minute, background: true, timeout: 5 * time.Second,
		read: func(ctx context.Context) (crewPanelSection, error) {
			reads <- 1
			if label == "new" {
				<-release
			}
			return crewPanelSection{Rows: []crewPanelRow{{ID: "r", Label: label}}}, nil
		}}
	cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second) // first read waits and fills the cache
	key.section = "changes"
	cache.mu.Lock()
	old := cache.entries[key]
	old.at = time.Now().Add(-2 * time.Minute)
	cache.entries[key] = old
	cache.mu.Unlock()
	key.section = ""
	label = "new"
	for i := 0; i < 3; i++ {
		start := time.Now()
		got := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
		if time.Since(start) > time.Second || len(got[0].Rows) != 1 || got[0].Rows[0].Label != "old" {
			t.Fatalf("read %d did not serve the last snapshot at once: %+v after %s", i, got, time.Since(start))
		}
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
		if got[0].Rows[0].Label == "new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background read never replaced the snapshot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(reads); n != 2 { // the first read and exactly one background read
		t.Fatalf("source read %d times, want 2", n)
	}
	// A Git action or turn end after the snapshot drops it: the next read shows a current one, not the old rows.
	label = "committed"
	crewPanelChangesDirty.Store(key.project, time.Now())
	t.Cleanup(func() { crewPanelChangesDirty.Delete(key.project) })
	if got := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second); got[0].Rows[0].Label != "committed" {
		t.Fatalf("a snapshot older than the last Git action was shown: %+v", got[0].Rows)
	}
}

// #2989: a live notice must recheck current access without reading chat
// bindings and expanding runtime jobs. Use real workspace HTTP handlers.
func TestCrewPanelNoticeAdmissionAvoidsRuntimeExpansionAndKeepsCurrentAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	shareCrewBotForTest(t, externalCommandCrewRoot, "other")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	other := &UserClaims{UserID: "other", Username: "other"}
	notice := livefeed.Notice{Kind: livefeed.CrewPanel, Project: "team"}
	ctx, reads := workspacereads.WithRequest(t.Context())
	if !f.api.crewPanelNoticeAllowed(ctx, owner, notice) {
		t.Fatal("owner notice refused")
	}
	// The measured bug made many workspace reads per notice. Only target
	// metadata is needed; byte-cache hits may reduce this to zero.
	if reads.Reads() > 1 {
		t.Fatalf("notice expanded workspace state: %d reads", reads.Reads())
	}
	if !f.api.crewPanelNoticeAllowed(t.Context(), other, notice) {
		t.Fatal("shared Crew notice refused")
	}
	bounded := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"team"}}}
	if !f.api.crewPanelNoticeAllowed(t.Context(), bounded, notice) {
		t.Fatal("scoped allowed Crew refused")
	}
	bounded.AccessToken.CrewIDs = []string{"another"}
	if f.api.crewPanelNoticeAllowed(t.Context(), bounded, notice) {
		t.Fatal("out-of-scope Crew leaked")
	}
	withProjectSharing(t, false)
	if !f.api.crewPanelNoticeAllowed(t.Context(), owner, notice) {
		t.Fatal("sharing off did not retain the owner's access")
	}
	withProjectSharing(t, true)
	stopCrewBotForTest(t, externalCommandCrewRoot)
	if f.api.crewPanelNoticeAllowed(t.Context(), other, notice) || !f.api.crewPanelNoticeAllowed(t.Context(), owner, notice) {
		t.Fatal("sharing revocation was not immediate")
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"product":"work","id":"another"}`)
	if f.api.crewPanelNoticeAllowed(t.Context(), owner, notice) {
		t.Fatal("mismatched manifest identity admitted")
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"product":"work","id":"team"}`)
	if !f.api.crewPanelNoticeAllowed(t.Context(), owner, notice) {
		t.Fatal("restored metadata remained refused")
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"],"disabled":true}]}`)
	if f.api.crewPanelNoticeAllowed(t.Context(), owner, notice) {
		t.Fatal("disabled account received Crew notice")
	}
}

// #4181: a panic in one panel section must fail only that section (and release workflowRead), never crash the server.
func TestCrewPanelCollectRecoversSectionPanic(t *testing.T) {
	cache := crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "crew-panic", authority: "current"}
	sources := []crewPanelSource{
		{id: "workflow", title: "Workflow", allowed: true, read: func(context.Context) (crewPanelSection, error) {
			panic("boom")
		}},
		{id: "dashboards", title: "Artifacts", allowed: true, read: func(context.Context) (crewPanelSection, error) {
			return crewPanelSection{Rows: []crewPanelRow{{ID: "d1", Label: "Overview"}}}, nil
		}},
	}
	out := cache.collect(t.Context(), key, sources, time.Second)
	if len(out) != 2 || out[0].Error == "" || out[1].Error != "" || len(out[1].Rows) != 1 {
		t.Fatalf("unexpected collect result after panic: %+v", out)
	}
	// workflowRead must be unlocked after the panic so subsequent workflow reads succeed.
	if !cache.workflowRead.TryLock() {
		t.Fatal("workflowRead mutex remained locked after panicking workflow section")
	}
	cache.workflowRead.Unlock()
}

// #4181: a slow background refresh started before markCrewPanelChangesDirty must not overwrite newer dirty state,
// and a panic in background refresh must still clear the in-flight refreshing flag.
func TestCrewPanelBackgroundRefreshDirtyRaceAndPanicRecovery(t *testing.T) {
	project := "crew-bg-dirty-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	defer crewPanelChangesDirty.Delete(project)

	cache := crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: project, section: "changes", authority: "v1"}
	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	staleSource := crewPanelSource{
		id: "changes", title: "Uncommitted", allowed: true, background: true,
		read: func(context.Context) (crewPanelSection, error) {
			close(startedCh)
			<-releaseCh
			return crewPanelSection{Rows: []crewPanelRow{{ID: "stale", Label: "Stale"}}}, nil
		},
	}
	cache.refreshInBackground(t.Context(), key, staleSource, time.Second)
	<-startedCh
	// Mark dirty after the scan began, then let the stale scan finish.
	time.Sleep(5 * time.Millisecond)
	crewPanelChangesDirty.Store(project, time.Now())
	close(releaseCh)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		busy := cache.refreshing[key]
		cache.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, found := cache.previous(key); found {
		t.Fatal("stale background refresh overwrote dirty project state")
	}

	panicSource := crewPanelSource{
		id: "changes", title: "Uncommitted", allowed: true, background: true,
		read: func(context.Context) (crewPanelSection, error) { panic("bg boom") },
	}
	cache.refreshInBackground(t.Context(), key, panicSource, time.Second)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		busy := cache.refreshing[key]
		cache.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background refresh left refreshing flag stuck after panic")
}

// #4181: crewPanelCache bounds its entries map and evicts oldest snapshots first.
func TestCrewPanelCacheBoundsEntries(t *testing.T) {
	cache := crewPanelCache{maxEntries: 3}
	base := time.Now().Add(-time.Minute)
	for i := 0; i < 5; i++ {
		k := crewPanelCacheKey{caller: "owner", project: "p", section: "s", authority: strconv.Itoa(i)}
		cache.putAt(k, crewPanelSection{Detail: strconv.Itoa(i)}, base.Add(time.Duration(i)*time.Second))
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 3 {
		t.Fatalf("expected 3 bounded cache entries, got %d", len(cache.entries))
	}
	for _, evicted := range []string{"0", "1"} {
		k := crewPanelCacheKey{caller: "owner", project: "p", section: "s", authority: evicted}
		if _, ok := cache.entries[k]; ok {
			t.Fatalf("expected oldest entry %s to be evicted", evicted)
		}
	}
}

// #4198: embedded section structs on crewPanelRow must serialize to the exact same flat JSON wire format.
func TestCrewPanelRowEmbeddedJSONWireFormat(t *testing.T) {
	row := crewPanelRow{
		ID:          "repo-1",
		Label:       "app",
		Detail:      "main",
		Destination: "files",
		Connected:   true,
		Group:       "org/app",
		Count:       "up to date",
		crewPanelGitRow: crewPanelGitRow{
			CommitAt:     1700000000,
			Commit:       "initial",
			FetchedAt:    1700000100,
			GitHubRepo:   "org/app",
			Changed:      true,
			ChangedFiles: 2,
		},
		crewPanelGitHubRow: crewPanelGitHubRow{
			Login: "dev",
			Orgs:  []string{"org"},
		},
		crewPanelDashboardRow: crewPanelDashboardRow{
			Motion:   true,
			Artifact: true,
		},
		crewPanelActivityRow: crewPanelActivityRow{
			Steps:     []string{"New", "Done"},
			Rules:     "auto",
			DoneCount: 3,
		},
		crewPanelProjectTasksRow: crewPanelProjectTasksRow{
			Lead:          "Lead",
			TasksMode:     "auto",
			Paused:        true,
			NeedsYouCount: 1,
			OpenCount:     4,
		},
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"id", "label", "detail", "destination", "connected", "group", "count",
		"commit_at", "commit", "fetched_at", "github_repo", "changed", "changed_files",
		"login", "orgs", "motion", "artifact",
		"steps", "rules", "done_count",
		"lead", "tasks_mode", "paused", "needs_you_count", "open_count",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("expected flat JSON key %q in %s", key, string(raw))
		}
	}
	// Ensure no nested wrapper keys leaked.
	for _, unexpected := range []string{"crewPanelGitRow", "crewPanelGitHubRow", "crewPanelDashboardRow", "crewPanelActivityRow", "crewPanelProjectTasksRow"} {
		if _, ok := decoded[unexpected]; ok {
			t.Fatalf("unexpected nested struct key %q in %s", unexpected, string(raw))
		}
	}
}

// #4198: scheduleHandler must fail cleanly (500) instead of sending an empty body if JSON marshaling fails.
func TestScheduleHandlerRejectsUnmarshalableBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	called := false
	status, _ := scheduleHandler(req, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}, http.MethodPost, "/test", nil, nil, make(chan int))
	if called || status != http.StatusInternalServerError {
		t.Fatalf("expected 500 without calling handler, got called=%v status=%d", called, status)
	}
}

// #4198: direct gitHubVaultSummaryForContext preserves the exact 403 guard for missing/token claims.
func TestGitHubVaultSummaryForContextGuardsClaims(t *testing.T) {
	if _, status, err := gitHubVaultSummaryForContext(t.Context(), "Crew/demo"); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 for unauthenticated context, got status=%d err=%v", status, err)
	}
	tokenCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{
		UserID:      "owner",
		AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}},
	})
	if _, status, err := gitHubVaultSummaryForContext(tokenCtx, "Crew/demo"); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 for access-token claims, got status=%d err=%v", status, err)
	}
}

// #4198: a fresh cached background section (like Git status) must not set refreshAt
// and trigger spurious 30s refresh_after_ms timers on the frontend.
func TestCrewPanelBackgroundSectionDoesNotEmitTrailingRefresh(t *testing.T) {
	cache := &crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "bg-no-timer", authority: "v1"}
	source := crewPanelSource{
		id:         "changes",
		title:      "Uncommitted",
		allowed:    true,
		refreshFor: crewPanelCacheTTL,
		background: true,
		read: func(context.Context) (crewPanelSection, error) {
			return crewPanelSection{Detail: "1 repository", Rows: []crewPanelRow{{ID: "r1", Label: "app"}}}, nil
		},
	}
	// First call populates the cache; second call hits c.previous(k, source.refreshFor).
	cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	cached := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	reply := crewPanelReply{Sections: cached}
	applyCrewPanelRefresh(&reply, "0")
	if reply.RefreshAfterMS != 0 || reply.RefreshAttempt != 0 {
		t.Fatalf("cached background section emitted trailing refresh timer: %+v", reply)
	}
}

// #4479: the access section serves stale cached snapshots immediately while
// refreshing in the background, and file-change dirty marks do not evict it.
func TestCrewPanelAccessSectionServesStaleWhileRevalidating(t *testing.T) {
	cache := &crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "access-swr", authority: "v1"}
	defer crewPanelChangesDirty.Delete(key.project)

	var calls int
	block := make(chan struct{})
	refreshStarted := make(chan struct{}, 2)

	source := crewPanelSource{
		id:         "access",
		title:      "Online",
		allowed:    true,
		refreshFor: 15 * time.Second,
		background: true,
		read: func(ctx context.Context) (crewPanelSection, error) {
			calls++
			if calls == 1 {
				return crewPanelSection{Detail: "On this Mac · v1"}, nil
			}
			select {
			case refreshStarted <- struct{}{}:
			default:
			}
			select {
			case <-block:
			case <-ctx.Done():
				return crewPanelSection{}, ctx.Err()
			}
			return crewPanelSection{Detail: "On this Mac · v2"}, nil
		},
	}

	// 1. Cold read populates cache synchronously.
	first := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	if len(first) != 1 || first[0].Detail != "On this Mac · v1" {
		t.Fatalf("cold read = %+v", first)
	}

	// 2. A file-change dirty mark must not evict the access section.
	crewPanelChangesDirty.Store(key.project, time.Now().Add(time.Minute))
	warm := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	if len(warm) != 1 || warm[0].Detail != "On this Mac · v1" || calls != 1 {
		t.Fatalf("dirty mark evicted access section: sections=%+v calls=%d", warm, calls)
	}

	// 3. Age the entry past refreshFor: collect must return the stale snapshot
	// immediately and refresh in the background without blocking.
	secKey := key
	secKey.section = "access"
	cache.mu.Lock()
	entry := cache.entries[secKey]
	entry.at = time.Now().Add(-30 * time.Second)
	cache.entries[secKey] = entry
	cache.mu.Unlock()

	start := time.Now()
	stale := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("stale access section blocked for %v", elapsed)
	}
	if len(stale) != 1 || stale[0].Detail != "On this Mac · v1" {
		t.Fatalf("stale read = %+v", stale)
	}

	<-refreshStarted
	close(block)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
		if len(got) == 1 && got[0].Detail == "On this Mac · v2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background refresh did not update access section in time")
}
