// Package schedulepolicy defines the shared schedule after-run authoring contract.
package schedulepolicy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const ExplicitAfterRunContractVersion = "1.0.41"

func RequiresExplicitAfterRun(version string) bool {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != 3 {
		return false
	}
	var values [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
		values[i] = n
	}
	return values[0] > 1 || (values[0] == 1 && (values[1] > 0 || values[2] >= 41))
}

// ValidateAfterRunStamp checks canonical after-run choices only when stamping an
// upgrade. Already-stamped workflows are not revalidated by runtime reads.
// Disabled and calendar schedules count; webhooks do not do housekeeping.
func ValidateAfterRunStamp(content []byte, target string) error {
	if !RequiresExplicitAfterRun(target) {
		return nil
	}
	var manifest struct {
		Kind      string `json:"kind"`
		Schedules []struct {
			ID       string `json:"id"`
			Type     string `json:"schedule_type"`
			AfterRun *struct {
				Backup  *bool `json:"backup"`
				Publish *bool `json:"publish"`
			} `json:"after_run"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		return err
	}
	if manifest.Kind == "relay" {
		return nil
	}
	for i, schedule := range manifest.Schedules {
		if schedule.Type == "webhook" {
			continue
		}
		a := schedule.AfterRun
		if a == nil || a.Backup == nil || a.Publish == nil {
			return fmt.Errorf("schedules[%d] (%s): set after_run backup and publish using update_schedule", i, schedule.ID)
		}
	}
	return nil
}
