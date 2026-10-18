package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Pin the approved one-time mapping: no combination of legacy switches may
// accidentally gain outward permission, and a new explicit OFF always wins.
func TestPulseSettingsMigrationKeepsOutwardAuthority(t *testing.T) {
	cases := []struct{ autonomy, want string }{{`{"level":0}`, "ask"}, {`{"level":1}`, "ask"}, {`{"level":2}`, "ask"}, {`{"level":3}`, "ask"}, {`{"level":4}`, "allow"}, {`{"level":5}`, "allow"}, {`{"level":6}`, "ask"}, {`{"level":"wrong"}`, "ask"}, {`{"run":"wrong","change":"auto","outward":"auto"}`, "ask"}}
	for _, run := range []string{"auto", "ask"} {
		for _, change := range []string{"auto", "ask"} {
			for _, outward := range []string{"auto", "ask"} {
				want := "ask"
				if run == "auto" && change == "auto" && outward == "auto" {
					want = "allow"
				}
				cases = append(cases, struct{ autonomy, want string }{fmt.Sprintf(`{"run":%q,"change":%q,"outward":%q}`, run, change, outward), want})
			}
		}
	}
	for _, c := range cases {
		raw := []byte(`{"pulse":{"enabled":true,"pace":"calm","autonomy":` + c.autonomy + `},"schedules":[{"id":"daily","enabled":true,"pulse_mode":"off"}]}`)
		migrated, changed := normalizePulseSettingsJSON(raw)
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
		if !decoded.Pulse.Enabled || decoded.Pulse.Outward != c.want || len(decoded.Schedules) != 1 || decoded.Schedules[0].AfterRun != (ScheduleAfterRun{}) {
			t.Fatalf("normalization changed authority/housekeeping: %s", migrated)
		}
		for _, retired := range []string{`"pace"`, `"autonomy"`, `"pulse_mode"`} {
			if strings.Contains(string(migrated), retired) {
				t.Fatalf("retired setting retained: %s", migrated)
			}
		}
		if _, again := normalizePulseSettingsJSON(migrated); again {
			t.Fatalf("not idempotent: %s", migrated)
		}
	}
	raw := []byte(`{"pulse":{"enabled":false,"outward_actions":"ask"},"schedules":[{"id":"legacy","enabled":true,"pulse_mode":"full"},{"id":"review","enabled":true,"pulse_review_only":true}]}`)
	migrated, _ := normalizePulseSettingsJSON(raw)
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
}
