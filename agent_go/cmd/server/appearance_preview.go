package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"
	"unicode/utf8"
)

const appearancePreviewTTL = 30 * time.Minute
const appearanceReportMaxBytes = 32 << 10

// appearanceUserState is one person's in-memory appearance state, never written
// to disk: the saved list this server last wrote (GitHub #2851: a stale or short
// read must not lose saved themes), a temporary preview theme and the app's
// last diagnostics report per source. Its lock serializes that person's
// read-modify-write of appearance.json without blocking other people.
type appearanceUserState struct {
	mu             sync.Mutex
	known          []appearanceTheme
	preview        *appearanceTheme
	previewExpires time.Time
	reports        map[string]appearanceReport
}

var appearanceStates sync.Map

// Keyed by the document service too, so a replaced service never inherits a
// saved list remembered from another one.
func appearanceStateFor(user string) *appearanceUserState {
	value, _ := appearanceStates.LoadOrStore(getWorkspaceAPIURL()+"\x00"+user, &appearanceUserState{})
	return value.(*appearanceUserState)
}

func (s *appearanceUserState) activePreview(now time.Time) *appearanceTheme {
	if s.preview != nil && now.After(s.previewExpires) {
		s.preview = nil
	}
	return s.preview
}

// appearanceRestoreMissing returns saved plus any theme of known that saved lacks (by name), and how many came back.
// It runs before every mutating operation, so an explicit delete or rename then acts on the complete list.
func appearanceRestoreMissing(saved, known []appearanceTheme) ([]appearanceTheme, int) {
	restored := 0
	for _, theme := range known {
		if len(saved) >= appearanceSavedLimit {
			break
		}
		if appearanceSavedIndex(appearanceDocument{Saved: saved}, theme.Name) < 0 {
			saved = append(saved, theme)
			restored++
		}
	}
	return saved, restored
}

type appearanceDropped struct {
	Text   string `json:"text"`
	Reason string `json:"reason"`
}
type appearanceReportBackground struct {
	Kind  string  `json:"kind"`
	State string  `json:"state"`
	FPS   float64 `json:"fps"`
	Error string  `json:"error,omitempty"`
}

// appearanceReport is what the person's own browser saw when it used a theme:
// declarations it dropped, how many elements each hook matched, and the
// background's runtime state. Agents read it back to fix a theme.
type appearanceReport struct {
	Theme      string                      `json:"theme"`
	Source     string                      `json:"source"`
	Mode       string                      `json:"mode"`
	At         string                      `json:"at"`
	Dropped    []appearanceDropped         `json:"dropped"`
	Hooks      map[string]int              `json:"hooks"`
	Background *appearanceReportBackground `json:"background,omitempty"`
}

func parseAppearanceReport(raw json.RawMessage) (appearanceReport, error) {
	var report appearanceReport
	if len(raw) == 0 || len(raw) > appearanceReportMaxBytes {
		return report, errors.New("Send one report up to 32 KiB.")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil {
		return report, errors.New("Send the offered report fields.")
	}
	runes := utf8.RuneCountInString
	if runes(report.Theme) > 60 || (report.Source != "app" && report.Source != "preview") || (report.Mode != "light" && report.Mode != "dark") || len(report.Dropped) > 50 || len(report.Hooks) > 200 {
		return report, errors.New("Send a report for app or preview, light or dark, with up to 50 dropped rules and 200 hooks.")
	}
	if _, err := time.Parse(time.RFC3339, report.At); err != nil {
		return report, errors.New("Send the report time as RFC3339.")
	}
	for _, dropped := range report.Dropped {
		if runes(dropped.Text) > 300 || runes(dropped.Reason) > 120 {
			return report, errors.New("Keep each dropped rule to 300 characters and its reason to 120.")
		}
	}
	for hook, count := range report.Hooks {
		if hook == "" || runes(hook) > 200 || count < 0 {
			return report, errors.New("Send hook selectors with match counts of 0 or more.")
		}
	}
	if b := report.Background; b != nil {
		if !slices.Contains([]string{"script", "shader", "video", "image", "none"}, b.Kind) || !slices.Contains([]string{"running", "throttled", "stopped", "paused", "error"}, b.State) || b.FPS < 0 || b.FPS > 240 || runes(b.Error) > 300 {
			return report, errors.New("Send the background kind, state, fps 0–240 and an error up to 300 characters.")
		}
	}
	if report.Dropped == nil {
		report.Dropped = []appearanceDropped{}
	}
	if report.Hooks == nil {
		report.Hooks = map[string]int{}
	}
	return report, nil
}
