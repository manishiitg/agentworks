package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
)

func TestUsageHourlyOptInRestartAndOff(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := privateLiveDir(root); err != nil {
		t.Fatal(err)
	}
	var reports []struct {
		Action  string             `json:"action"`
		Reading agentusage.Reading `json:"reading"`
	}
	var allowClear atomic.Bool
	allowClear.Store(true)
	reported := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" || r.URL.Path != "/api/external/v1/agents/usage" {
			t.Error("wrong reporting connection")
		}
		var report struct {
			Action  string             `json:"action"`
			Reading agentusage.Reading `json:"reading"`
		}
		if json.NewDecoder(r.Body).Decode(&report) != nil {
			t.Error("invalid reading")
		}
		reports = append(reports, report)
		if report.Action == "off" && !allowClear.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stored":true}`))
		reported <- report.Action
	}))
	defer srv.Close()
	config := filepath.Join(root, "connection.json")
	if err := SaveConfig(config, Config{Server: srv.URL, Token: "fixture"}); err != nil {
		t.Fatal(err)
	}
	client, err := localConfiguredClient(config)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().Add(-2 * time.Hour)
	reads := 0
	makeBridge := func() *localBridge {
		return &localBridge{root: root, sessions: map[string]*localSession{"codex:session": {Attachment: AgentAttachment{ID: "lease", AgentID: "agent", Provider: "codex", SessionID: "session"}, HostKey: "host", ConfigPath: config, client: client}}, options: LocalBridgeOptions{
			HostAlive: func(string, string) bool { return true }, UsageNow: func() time.Time { return clock }, UsageRead: func(_ context.Context, a AgentAttachment, _ string, _ int64) (agentusage.Native, error) {
				if a.SessionID != "session" || a.ID != "lease" {
					t.Fatal("usage followed another session")
				}
				reads++
				return agentusage.Native{Counters: agentusage.Counters{Input: int64(reads * 100), Output: int64(reads * 10)}, Source: "Codex session total"}, nil
			},
		}}
	}
	b := makeBridge()
	if b.usagePass(context.Background()) {
		t.Fatal("default-off collector requested another background pass")
	}
	if reads != 0 || len(reports) != 0 {
		t.Fatal("default-off collector read native data")
	}
	if _, err = os.Stat(filepath.Join(root, "usage-state.json")); !os.IsNotExist(err) {
		t.Fatal("default-off collector created an empty state file")
	}
	if err = SetLocalUsage(root, true); err != nil {
		t.Fatal(err)
	}
	if !b.usagePass(context.Background()) {
		t.Fatal("enabled collector stopped its hourly worker")
	}
	if reads != 1 || len(reports) != 1 || !reports[0].Reading.Baseline {
		t.Fatal("enable did not establish one native baseline")
	}
	// Recreated listener restores next-sample time; no duplicate initial read.
	b = makeBridge()
	b.usagePass(context.Background())
	if reads != 1 {
		t.Fatal("listener restart resampled before its hour")
	}
	clock = clock.Add(time.Hour)
	b.usagePass(context.Background())
	if reads != 2 || len(reports) != 2 {
		t.Fatal("hourly native reading missing")
	}
	if err = SetLocalUsage(root, false); err != nil {
		t.Fatal(err)
	}
	state, err := readUsageState(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range state.Bindings {
		if x.Last.Native.Counters.Total() != 0 || x.Pending != nil {
			t.Fatal("off retained local numeric state")
		}
	}
	allowClear.Store(false)
	if !b.usagePass(context.Background()) {
		t.Fatal("pending remote clear stopped retrying")
	}
	allowClear.Store(true)
	if b.usagePass(context.Background()) {
		t.Fatal("disabled collector kept running after cleanup")
	}
	if reads != 2 || reports[len(reports)-1].Action != "off" {
		t.Fatal("off read data or failed to clear reporting")
	}
	status, err := LocalUsageStatus(root)
	if err != nil || status.Enabled || status.PendingClear != 0 {
		t.Fatalf("off status: %+v %v", status, err)
	}
	before, err := os.Stat(filepath.Join(root, "usage-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if b.usagePass(context.Background()) {
		t.Fatal("empty disabled collector requested background work")
	}
	after, err := os.Stat(filepath.Join(root, "usage-state.json"))
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("empty disabled collector rewrote saved state")
	}

	// The existing person-command refresh starts an idle worker immediately.
	b.usageWake = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.startUsage(ctx)
	b.mu.Lock()
	running := b.usageRunning
	b.mu.Unlock()
	if running {
		t.Fatal("disabled collector started a goroutine")
	}
	for len(reported) > 0 {
		<-reported
	}
	if err = SetLocalUsage(root, true); err != nil {
		t.Fatal(err)
	}
	if out := b.handle(ctx, ctx, LocalBridgeRequest{Action: "usage_refresh"}); out.Error != "" {
		t.Fatal(out.Error)
	}
	select {
	case action := <-reported:
		if action != "sample" {
			t.Fatal("refresh did not sample native usage")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("refresh waited for a background timer")
	}
	cancel()
	b.workers.Wait()
	if reads != 3 || !reports[len(reports)-1].Reading.Baseline {
		t.Fatal("refresh did not start one fresh baseline")
	}
}
