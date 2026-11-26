package llmtypes

import (
	"math"
	"time"
)

// AccountUsage is a subscription quota snapshot, separate from conversation
// tokens and estimated costs. An empty Windows list means unavailable, not zero.
type AccountUsage struct {
	Windows   []AccountUsageWindow `json:"windows"`
	CheckedAt time.Time            `json:"checked_at"`
}
type AccountUsageWindow struct {
	Label       string     `json:"label"`
	UsedPercent float64    `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

func (u *AccountUsage) AddWindow(label string, percent *float64, reset time.Time) {
	if percent == nil || math.IsNaN(*percent) || math.IsInf(*percent, 0) || *percent < 0 {
		return
	}
	w := AccountUsageWindow{Label: label, UsedPercent: math.Min(100, *percent)}
	if !reset.IsZero() {
		t := reset.UTC()
		w.ResetsAt = &t
	}
	u.Windows = append(u.Windows, w)
}

// AccountUsageFailure is a credential-free outcome suitable for server logs.
// Provider error messages and response bodies must never become this value.
type AccountUsageFailure string

func (e AccountUsageFailure) Error() string { return "account usage: " + string(e) }
