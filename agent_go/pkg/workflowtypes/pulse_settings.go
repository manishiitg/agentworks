package workflowtypes

import (
	"encoding/json"
	"strings"
)

// NormalizePulseSettingsJSON retires saved settings once, before decoding the
// current schema. New outward_actions marks the canonical shape; an explicit
// OFF in that shape cannot be overridden by an old schedule. The existing
// manifest migration writer persists the returned document through its normal
// locks and changelog. No workflow execution or contract-version upgrade runs.
func NormalizePulseSettingsJSON(raw []byte) ([]byte, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return raw, false
	}
	var kind string
	_ = json.Unmarshal(doc["kind"], &kind)
	// Relay releases and product-owned manifests have different owners/schemas.
	if kind == "relay" {
		return raw, false
	}
	pulse := map[string]json.RawMessage{}
	if v := doc["pulse"]; len(v) > 0 && string(v) != "null" {
		if json.Unmarshal(v, &pulse) != nil {
			return raw, false
		}
	}
	var schedules []map[string]json.RawMessage
	if v := doc["schedules"]; len(v) > 0 && json.Unmarshal(v, &schedules) != nil {
		return raw, false
	}
	var enabled bool
	_ = json.Unmarshal(pulse["enabled"], &enabled)
	_, canonical := pulse["outward_actions"]
	var postRunMonitor bool
	_ = json.Unmarshal(doc["post_run_monitor"], &postRunMonitor)
	legacyEnabled := enabled || postRunMonitor
	for _, schedule := range schedules {
		var on, review bool
		var mode string
		_ = json.Unmarshal(schedule["enabled"], &on)
		_ = json.Unmarshal(schedule["pulse_review_only"], &review)
		_ = json.Unmarshal(schedule["pulse_mode"], &mode)
		if on && (review || strings.EqualFold(strings.TrimSpace(mode), "full")) {
			legacyEnabled = true
		}
	}
	_, explicitEnabled := pulse["enabled"]
	changed := !canonical || !explicitEnabled
	if !canonical || !explicitEnabled {
		if !canonical {
			enabled = legacyEnabled && legacyPulseCanImprove(pulse["autonomy"])
		} else {
			enabled = false
		}
		pulse["enabled"], _ = json.Marshal(enabled)
	}
	if !canonical {
		pulse["outward_actions"], _ = json.Marshal(legacyPulseOutwardActions(pulse["autonomy"]))
	}

	for _, key := range []string{"autonomy", "pace", "schedule", "disabled_review_modules"} {
		if _, ok := pulse[key]; ok {
			delete(pulse, key)
			changed = true
		}
	}
	for _, key := range []string{"post_run_monitor", "post_run_monitor_mode"} {
		if _, ok := doc[key]; ok {
			delete(doc, key)
			changed = true
		}
	}
	kept := make([]map[string]json.RawMessage, 0, len(schedules))
	for _, schedule := range schedules {
		var review bool
		_ = json.Unmarshal(schedule["pulse_review_only"], &review)
		if review {
			changed = true
			continue
		}
		// Save exactly the housekeeping this legacy schedule had before dropping
		// its Pulse mode. Empty inherited the old effective workflow toggle.
		if prior, ok := schedule["after_run"]; !ok || string(prior) == "null" {
			var mode, scheduleType string
			_ = json.Unmarshal(schedule["pulse_mode"], &mode)
			_ = json.Unmarshal(schedule["schedule_type"], &scheduleType)
			on := legacyEnabled
			switch strings.ToLower(strings.TrimSpace(mode)) {
			case "off":
				on = false
			case "basic", "full":
				on = true
			}
			if strings.EqualFold(scheduleType, "webhook") {
				on = false
			}
			schedule["after_run"], _ = json.Marshal(map[string]bool{"backup": on, "publish": on, "notify": on})
			changed = true
		}
		for _, key := range []string{"pulse_mode", "pulse_mode_reason", "pulse_review_only"} {
			if _, ok := schedule[key]; ok {
				delete(schedule, key)
				changed = true
			}
		}
		kept = append(kept, schedule)
	}
	if !changed {
		return raw, false
	}
	doc["pulse"], _ = json.Marshal(pulse)
	if schedules != nil {
		doc["schedules"], _ = json.Marshal(kept)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw, false
	}
	return out, true
}

// Preserve ON only when the old setting already allowed workflow changes.
// Advise/Measure (including the former default) migrate to OFF, per the owner.
func legacyPulseCanImprove(raw json.RawMessage) bool {
	var a struct {
		Level  *int   `json:"level"`
		Run    string `json:"run"`
		Change string `json:"change"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return false
	}
	if a.Level != nil {
		return *a.Level >= 2
	}
	return !strings.EqualFold(strings.TrimSpace(a.Run), "ask") && strings.EqualFold(strings.TrimSpace(a.Change), "auto")
}

// This is a one-time data conversion, not an autonomy ladder at runtime.
// Keep old effective outward authority: levels 4–5, or all three old switches
// auto. Unknown values and malformed settings never acquire outward authority.
func legacyPulseOutwardActions(raw json.RawMessage) string {
	var a struct {
		Level   *int   `json:"level"`
		Run     string `json:"run"`
		Outward string `json:"outward"`
		Change  string `json:"change"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return "ask"
	}
	if a.Level != nil {
		if *a.Level >= 4 && *a.Level <= 5 {
			return "allow"
		}
		return "ask"
	}
	normalize := func(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
	run, change, outward := normalize(a.Run), normalize(a.Change), normalize(a.Outward)
	if (run == "" || run == "auto") && change == "auto" && outward == "auto" {
		return "allow"
	}
	return "ask"
}
