package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
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
