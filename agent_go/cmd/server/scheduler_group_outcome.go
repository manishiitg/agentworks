package server

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Mixed outcomes are not a new scheduler state: they settle as the existing
// partial result. A completed chat alone never supplies execution evidence.
type workshopPartialOutcome struct{ completed, failed int }

func (p *workshopPartialOutcome) Error() string {
	return fmt.Sprintf("%d groups completed; %d groups failed (saved run results)", p.completed, p.failed)
}
func partialWorkshopRun(err error) bool {
	var partial *workshopPartialOutcome
	return errors.As(err, &partial)
}

func reconcileWorkshopPartialOutcome(before map[string]bool, after []RunFolderInfo, since time.Time, expectedGroups int) *workshopPartialOutcome {
	result := &workshopPartialOutcome{}
	parent := ""
	seen := map[string]bool{}
	for _, folder := range after {
		if !workshopRunStartedSince(before, folder, since) {
			continue
		}
		// A root record is not another group, and different invocations may not
		// be combined into a partly successful run.
		groupParent, _ := path.Split(folder.Name)
		if groupParent == "" {
			continue
		}
		if parent != "" && parent != groupParent {
			return nil
		}
		parent = groupParent
		if seen[folder.Name] {
			continue
		}
		seen[folder.Name] = true
		if folder.Metadata == nil {
			return nil
		}
		if folder.Metadata.Recovery["status"] == "step_recovered_run_unverified" {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(folder.Metadata.Status)) {
		case "completed":
			result.completed++
		case "failed":
			result.failed++
		default:
			return nil // running, canceled, or unknown: keep their real outcome
		}
	}
	if len(seen) < expectedGroups || result.completed == 0 || result.failed == 0 {
		return nil
	}
	return result
}
