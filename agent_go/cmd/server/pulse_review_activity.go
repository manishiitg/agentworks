package server

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PulseReviewReport struct {
	Source     string `json:"source,omitempty"`
	Content    string `json:"content,omitempty"`
	Result     string `json:"result,omitempty"`
	Module     string `json:"module"`
	PulseRunID string `json:"pulse_run_id"`
	Path       string `json:"path"`
	UpdatedAt  string `json:"updated_at"`
}

// Return only files that actually exist, including checkpoints without a
// completed audit. A report's modified time is not a completed-review time.
func listPulseReviewReports(workspacePath, module string) ([]PulseReviewReport, error) {
	normalized, dbPath, err := reportHumanInputDBPath(workspacePath)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(dbPath)), "runs", "pulse")
	runs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []PulseReviewReport{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := map[string]string{"technical-review.md": pulseModuleTechnicalReview, "architecture-review.md": pulseModuleArchitectureReview, "strategic-review.md": pulseModuleStrategicReview, "plan-drift-review.md": "plan_drift_review"}
	out := []PulseReviewReport{}
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		for name, owner := range files {
			if module != "" && module != owner {
				continue
			}
			info, err := os.Lstat(filepath.Join(root, run.Name(), name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				continue
			}
			out = append(out, PulseReviewReport{Module: owner, PulseRunID: run.Name(), Path: filepath.ToSlash(filepath.Join(normalized, "runs", "pulse", run.Name(), name)), UpdatedAt: info.ModTime().UTC().Format(time.RFC3339)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].Path < out[j].Path
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Display the same durable notes the next reviewer reads; no second report write.
func pulseReportsWithNotes(reports []PulseReviewReport, notes []PulseReviewNote) []PulseReviewReport {
	for _, note := range notes {
		content := note.Conclusion
		if note.Content != "" && note.Content != note.Conclusion {
			content += "\n\n" + note.Content
		}
		if len(note.Evidence) > 0 {
			content += "\n\n### Evidence\n\n- " + strings.Join(note.Evidence, "\n- ")
		}
		reports = append(reports, PulseReviewReport{Module: note.Module, PulseRunID: note.PulseRunID, UpdatedAt: note.UpdatedAt, Source: "review_note", Content: strings.TrimSpace(content), Result: note.Result})
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].UpdatedAt == reports[j].UpdatedAt {
			return reports[i].Source == "review_note" && reports[j].Source != "review_note"
		}
		return reports[i].UpdatedAt > reports[j].UpdatedAt
	})
	return reports
}
