package server

import (
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"strings"
	"testing"
)

// Pin the approved one-time mapping: no combination of legacy switches may
// accidentally gain outward permission, and a new explicit OFF always wins.
func TestPulseSettingsMigrationKeepsOutwardAuthority(t *testing.T) {
	cases := []struct {
		autonomy, want string
		on             bool
	}{{`{"level":0}`, "ask", false}, {`{"level":1}`, "ask", false}, {`{"level":2}`, "ask", true}, {`{"level":3}`, "ask", true}, {`{"level":4}`, "allow", true}, {`{"level":5}`, "allow", true}, {`{"level":6}`, "ask", false}, {`{"level":"wrong"}`, "ask", false}, {`{"run":"wrong","change":"auto","outward":"auto"}`, "ask", true}, {`{}`, "ask", false}}
	for _, run := range []string{"auto", "ask"} {
		for _, change := range []string{"auto", "ask"} {
			for _, outward := range []string{"auto", "ask"} {
				want := "ask"
				if run == "auto" && change == "auto" && outward == "auto" {
					want = "allow"
				}
				cases = append(cases, struct {
					autonomy, want string
					on             bool
				}{fmt.Sprintf(`{"run":%q,"change":%q,"outward":%q}`, run, change, outward), want, run == "auto" && change == "auto"})
			}
		}
	}
	for _, c := range cases {
		raw := []byte(`{"pulse":{"enabled":true,"pace":"calm","autonomy":` + c.autonomy + `},"schedules":[{"id":"daily","enabled":true,"pulse_mode":"off"}]}`)
		migrated, changed := workflowtypes.NormalizePulseSettingsJSON(raw)
		if !changed {
			t.Fatalf("legacy data not normalized: %s", raw)
		}
		var decoded struct {
			Pulse struct {
				Enabled bool   `json:"enabled"`
				Outward string `json:"outward_actions"`
			}
			Schedules []struct {
				AfterRun ScheduleAfterRun `json:"after_run"`
			}
		}
		if err := json.Unmarshal(migrated, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Pulse.Enabled != c.on || decoded.Pulse.Outward != c.want || len(decoded.Schedules) != 1 || decoded.Schedules[0].AfterRun != (ScheduleAfterRun{}) {
			t.Fatalf("normalization changed authority/housekeeping: %s", migrated)
		}
		for _, retired := range []string{`"pace"`, `"autonomy"`, `"pulse_mode"`} {
			if strings.Contains(string(migrated), retired) {
				t.Fatalf("retired setting retained: %s", migrated)
			}
		}
		if _, again := workflowtypes.NormalizePulseSettingsJSON(migrated); again {
			t.Fatalf("not idempotent: %s", migrated)
		}
	}
	raw := []byte(`{"pulse":{"enabled":false,"outward_actions":"ask"},"schedules":[{"id":"legacy","enabled":true,"pulse_mode":"full"},{"id":"review","enabled":true,"pulse_review_only":true}]}`)
	migrated, _ := workflowtypes.NormalizePulseSettingsJSON(raw)
	var result struct {
		Pulse     struct{ Enabled bool }
		Schedules []struct {
			AfterRun ScheduleAfterRun `json:"after_run"`
		}
	}
	if err := json.Unmarshal(migrated, &result); err != nil {
		t.Fatal(err)
	}
	if result.Pulse.Enabled || len(result.Schedules) != 1 || !result.Schedules[0].AfterRun.Backup {
		t.Fatalf("explicit OFF or old housekeeping lost: %s", migrated)
	}
	for _, prior := range []struct {
		pulse string
		on    bool
	}{
		{`{}`, false}, {`{"enabled":true,"outward_actions":"ask"}`, true}, {`{"enabled":false,"outward_actions":"ask"}`, false},
	} {
		migrated, _ := workflowtypes.NormalizePulseSettingsJSON([]byte(`{"post_run_monitor":true,"post_run_monitor_mode":"per_run","pulse":` + prior.pulse + `}`))
		var doc struct{ Pulse struct{ Enabled bool } }
		if err := json.Unmarshal(migrated, &doc); err != nil || doc.Pulse.Enabled != prior.on || strings.Contains(string(migrated), "post_run_monitor") {
			t.Fatalf("old monitor conversion: %s %v", migrated, err)
		}
	}

	missingEnabled, _ := workflowtypes.NormalizePulseSettingsJSON([]byte(`{"pulse":{"outward_actions":"ask"},"schedules":[{"enabled":true,"pulse_mode":"full"}]}`))
	if !strings.Contains(string(missingEnabled), `"enabled":false`) {
		t.Fatalf("missing canonical enabled revived old ON: %s", missingEnabled)
	}

}
