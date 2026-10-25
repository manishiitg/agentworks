package server

import (
	"path/filepath"
	"time"
)

func (s *SchedulerService) nextWorkflowScheduleRunAt(workspacePath, scheduleID string, now time.Time) *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next *time.Time
	for _, job := range s.jobs {
		if job == nil || job.sctx == nil || !job.sctx.Schedule.Enabled || job.sctx.Schedule.ID != scheduleID || filepath.Clean(job.sctx.WorkspacePath) != filepath.Clean(workspacePath) {
			continue
		}
		var candidate time.Time
		if job.cronSched != nil {
			candidate = job.cronSched.Next(now)
		} else if job.runAt != nil {
			candidate = *job.runAt
		}
		if candidate.After(now) && (next == nil || candidate.Before(*next)) {
			value := candidate
			next = &value
		}
	}
	return next
}
