package goalcheck

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Measurement is DB evidence, not an AI verdict about progress toward the goal.
// History includes unavailable readings; a missing baseline never becomes zero.
type Measurement struct {
	Metric         Metric        `json:"metric"`
	State          string        `json:"state"`
	FreshnessHours float64       `json:"freshness_hours"`
	Latest         *Observation  `json:"latest,omitempty"`
	Previous       *Observation  `json:"previous,omitempty"`
	Change         *float64      `json:"change_from_previous,omitempty"`
	Comparison     string        `json:"comparison"`
	History        []Observation `json:"history"`
}

// Numeric accepts the current recording contract and legacy criterion verdicts.
func Numeric(o Observation) bool {
	hasEvidence := false
	for _, evidence := range o.Evidence {
		if strings.TrimSpace(evidence) != "" {
			hasEvidence = true
			break
		}
	}
	if !hasEvidence || o.Value == nil || math.IsNaN(*o.Value) || math.IsInf(*o.Value, 0) {
		return false
	}
	switch o.Status {
	case "", "ok", "met", "not_met", "in_progress":
		return true
	}
	return false
}

// Comparable checks only mechanical preconditions. Agents still check method,
// source completeness, sampling and attribution before interpreting a change.
func Comparable(window string, previous, current Observation) bool {
	if !Numeric(previous) || !Numeric(current) || !previous.ObservedAt.Before(current.ObservedAt) {
		return false
	}
	if previous.WindowStart == "" && previous.WindowEnd == "" && current.WindowStart == "" && current.WindowEnd == "" {
		switch strings.ToLower(strings.TrimSpace(window)) {
		case "instant", "snapshot", "point_in_time":
			return true
		}
		return false
	}
	ps, e1 := time.Parse(time.RFC3339Nano, previous.WindowStart)
	pe, e2 := time.Parse(time.RFC3339Nano, previous.WindowEnd)
	cs, e3 := time.Parse(time.RFC3339Nano, current.WindowStart)
	ce, e4 := time.Parse(time.RFC3339Nano, current.WindowEnd)
	return e1 == nil && e2 == nil && e3 == nil && e4 == nil && pe.After(ps) && ce.After(cs) && pe.Sub(ps) == ce.Sub(cs) && !pe.After(cs) && !pe.After(previous.ObservedAt) && !ce.After(current.ObservedAt)
}

// Measurements receives rows already matched to immutable definition and scope.
// The caller supplies bounded history per metric, independently of global limits.
func Measurements(metrics []Metric, observations []Observation, now time.Time, defaultHours float64) []Measurement {
	result := make([]Measurement, 0, len(metrics))
	for _, metric := range metrics {
		m := Measurement{Metric: metric, State: "missing", FreshnessHours: metric.FreshnessHours, Comparison: "unknown: no comparable baseline", History: []Observation{}}
		if m.FreshnessHours <= 0 {
			m.FreshnessHours = defaultHours
		}
		for _, o := range observations {
			if o.Metric == metric.ID && !o.ObservedAt.IsZero() && !o.ObservedAt.After(now) {
				m.History = append(m.History, o)
			}
		}
		sort.SliceStable(m.History, func(i, j int) bool {
			if m.History[i].ObservedAt.Equal(m.History[j].ObservedAt) {
				return m.History[i].RecordedAt.Before(m.History[j].RecordedAt)
			}
			return m.History[i].ObservedAt.Before(m.History[j].ObservedAt)
		})
		if len(m.History) > 0 {
			m.Latest = &m.History[len(m.History)-1]
			m.State = "unavailable"
			if Numeric(*m.Latest) {
				m.State = "measured"
				if now.Sub(m.Latest.ObservedAt).Hours() > m.FreshnessHours {
					m.State = "stale"
				}
				for i := len(m.History) - 2; i >= 0; i-- {
					if m.History[i].ObservedAt.Before(m.Latest.ObservedAt) {
						m.Previous = &m.History[i]
						break
					}
				}
				if m.Previous != nil {
					m.Comparison = "unknown: unavailable baseline or incompatible measurement windows"
					if Comparable(metric.Window, *m.Previous, *m.Latest) {
						change := *m.Latest.Value - *m.Previous.Value
						m.Change = &change
						m.Comparison = "numeric difference; source quality and goal progress require agent review"
					}
				}
			}
		}
		result = append(result, m)
	}
	return result
}
