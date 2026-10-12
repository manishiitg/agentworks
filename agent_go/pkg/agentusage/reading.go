package agentusage

import "time"

// Reading is an hourly observation, not a reconstruction of minute usage.
// Native session IDs, local paths and deduplication IDs never cross this API.
type Reading struct {
	ID            string    `json:"reading_id"`
	AgentID       string    `json:"agent_id"`
	AttachmentID  string    `json:"attachment_id"`
	Generation    string    `json:"generation"`
	Sequence      int64     `json:"sequence"`
	At            time.Time `json:"observed_at"`
	Since         time.Time `json:"window_start"`
	Native        Native    `json:"session"`
	Delta         Counters  `json:"delta"`
	Baseline      bool      `json:"baseline"`
	Discontinuity bool      `json:"discontinuity,omitempty"`
}

type Current struct {
	Reading
	Reporting bool `json:"reporting"`
}
