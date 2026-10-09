package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Pausing and resuming all schedules leaves a durable record of who did it
// and through what client, which survives the resume and log rotation.
func TestSchedulerPauseChangesAreRecorded(t *testing.T) {
	newScheduleRunWorkspaceStub(t)
	handler := updateSchedulerConfigHandler(nil)
	put := func(body, agent string) SchedulerConfigResponse {
		req := httptest.NewRequest(http.MethodPut, "/api/scheduler/config", strings.NewReader(body))
		req.Header.Set("User-Agent", agent)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: "u1", Username: "alice"}))
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var resp SchedulerConfigResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	put(`{"globally_paused":true,"paused_by":"frontend-user"}`, "Mozilla/5.0")
	resp := put(`{"globally_paused":false}`, "curl/8.7.1")
	if resp.GloballyPaused {
		t.Fatal("resume did not take effect")
	}
	events := recentSchedulerPauseEvents(context.Background(), 10)
	if len(events) != 2 {
		t.Fatalf("want a pause and a resume recorded, got %+v", events)
	}
	resumed, paused := events[0], events[1]
	if paused.Action != "paused" || paused.Username != "alice" || paused.Via != "frontend-user" || paused.UserAgent != "Mozilla/5.0" {
		t.Fatalf("pause event: %+v", paused)
	}
	if resumed.Action != "resumed" || resumed.UserAgent != "curl/8.7.1" || resumed.PausedSince == nil {
		t.Fatalf("resume event must say who and since when: %+v", resumed)
	}
	if len(resp.RecentPauseEvents) != 2 {
		t.Fatalf("the response carries recent pause history, got %d", len(resp.RecentPauseEvents))
	}
	// Saving without a change records nothing new.
	put(`{"globally_paused":false}`, "curl/8.7.1")
	if n := len(recentSchedulerPauseEvents(context.Background(), 10)); n != 2 {
		t.Fatalf("an unchanged save must not add an event, got %d", n)
	}
}

// PLAT-782: the scheduler pause is per product. The pause of everything holds every product, a product's own pause
// holds only that product, and an older client that sends only globally_paused cannot wipe the per-product list.
func TestSchedulerPauseIsPerProduct(t *testing.T) {
	cfg := sanitizeSchedulerConfig(&SchedulerConfig{PausedProducts: []string{"work", "Crew", "work", " CODE "}})
	if got := strings.Join(cfg.PausedProducts, ","); got != "work,code" {
		t.Fatalf("paused products = %q, want work,code (unknown names dropped, duplicates merged)", got)
	}
	for product, want := range map[string]bool{"work": true, "code": true, "agentworks": false, "relays": false} {
		if got := cfg.ProductPaused(product); got != want {
			t.Errorf("ProductPaused(%q) = %v, want %v", product, got, want)
		}
	}
	all := &SchedulerConfig{GloballyPaused: true}
	for _, product := range schedulerPauseProducts {
		if !all.ProductPaused(product) {
			t.Errorf("the pause of everything does not hold %q", product)
		}
	}
	if (*SchedulerConfig)(nil).ProductPaused("work") {
		t.Error("no config must mean not paused")
	}
	if workflowPauseProduct("relay") != "relays" || workflowPauseProduct("") != "agentworks" {
		t.Error("a workflow's timed runs belong to Goals, a Relay's to Relays")
	}
}
