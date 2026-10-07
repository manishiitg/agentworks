// Package productschedule defines recurring jobs a product declares: on a
// cron or a simple cadence, run the product's agent profile for a user by
// sending a fixed list of messages one at a time into its conversation.
//
// The definition and the timing rule live here so that a product running
// on its own (SparkQuill's family server) and a product hosted by AgentWorks
// (whose scheduler executes the same definition) agree on what a schedule
// is. Execution belongs to whoever hosts the product; Runner is the
// standalone executor.
package productschedule

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule is one recurring job. Either CronExpression or CadenceHours must
// be set; the cadence form is the one a non-technical user configures
// ("every 24 hours, around 8am"), the cron form the one a product author
// writes in product.yaml.
type Schedule struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	// CreatedAt anchors the first cron occurrence. Without a stable anchor, a
	// never-run cron schedule would recompute "next" from every scheduler tick
	// and could therefore never become due.
	CreatedAt string `json:"created_at,omitempty" yaml:"created_at,omitempty"`

	// CronExpression is a standard five-field cron line, evaluated in Timezone
	// (local time when empty).
	CronExpression string `json:"cron_expression,omitempty" yaml:"cron_expression,omitempty"`
	Timezone       string `json:"timezone,omitempty" yaml:"timezone,omitempty"`

	// RunAt makes the schedule one-time: an absolute RFC3339 instant, run once. It is the third form
	// beside CronExpression and CadenceHours (exactly one of the three). A run that succeeded never
	// repeats; a failed one retries with the usual backoff, up to OneTimeMaxFailures times.
	RunAt string `json:"run_at,omitempty" yaml:"run_at,omitempty"`

	// CadenceHours runs the job this many hours after its last run.
	// PreferredHour (0-23, local) additionally holds a due run until the
	// clock has reached that hour, so a daily cadence lands at about the same
	// time each day instead of drifting.
	CadenceHours  int  `json:"cadence_hours,omitempty" yaml:"cadence_hours,omitempty"`
	PreferredHour *int `json:"preferred_hour,omitempty" yaml:"preferred_hour,omitempty"`

	// QuietMinutes is how long the user must have been idle before a scheduled
	// run may start; MaxDeferralHours caps that wait (past this much overdue it
	// runs regardless). Zero disables the rule.
	QuietMinutes     int `json:"quiet_minutes,omitempty" yaml:"quiet_minutes,omitempty"`
	MaxDeferralHours int `json:"max_deferral_hours,omitempty" yaml:"max_deferral_hours,omitempty"`

	// Messages are sent into the product conversation one at a time; each is
	// a full agent turn and the next waits for the previous reply.
	Messages []string `json:"messages" yaml:"messages"`

	// Isolated runs this schedule on its own dedicated conversation (session,
	// coding-CLI process, transcript) instead of the profile or Crew project's
	// normal conversation. The isolated conversation persists across runs, so
	// successive runs of one schedule remain continuous with each other without
	// steering a person's live chat. Its turns are visible from run history,
	// rather than in the main conversation transcript.
	Isolated bool `json:"isolated,omitempty" yaml:"isolated,omitempty"`

	// ChatKey is the chat (tab) of the project that created the schedule, as
	// "<projectId>:chat:<id>" (a Code side chat). Runs go to that chat while it
	// exists and to the project's main chat otherwise; empty means the main chat.
	ChatKey string `json:"chat_key,omitempty" yaml:"chat_key,omitempty"`
}

// Validate checks a definition is runnable.
func Validate(s Schedule) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("schedule id is required")
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("schedule %q: name is required", s.ID)
	}
	hasCron := strings.TrimSpace(s.CronExpression) != ""
	hasRunAt := strings.TrimSpace(s.RunAt) != ""
	forms := 0
	for _, present := range []bool{hasCron, s.CadenceHours > 0, hasRunAt} {
		if present {
			forms++
		}
	}
	if forms == 0 {
		return fmt.Errorf("schedule %q: cron_expression, cadence_hours or run_at is required", s.ID)
	}
	if forms > 1 {
		return fmt.Errorf("schedule %q: use only one of cron_expression, cadence_hours and run_at", s.ID)
	}
	if hasRunAt {
		if _, err := s.RunAtTime(); err != nil {
			return fmt.Errorf("schedule %q: %w", s.ID, err)
		}
	}
	if hasCron {
		if _, err := cronSchedule(s); err != nil {
			return fmt.Errorf("schedule %q: %w", s.ID, err)
		}
	}
	if s.PreferredHour != nil && (*s.PreferredHour < 0 || *s.PreferredHour > 23) {
		return fmt.Errorf("schedule %q: preferred_hour must be 0-23", s.ID)
	}
	if s.QuietMinutes < 0 || s.MaxDeferralHours < 0 {
		return fmt.Errorf("schedule %q: quiet_minutes and max_deferral_hours must not be negative", s.ID)
	}
	if len(s.Messages) == 0 {
		return fmt.Errorf("schedule %q: at least one message is required", s.ID)
	}
	for i, m := range s.Messages {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("schedule %q: message %d is empty", s.ID, i+1)
		}
	}
	return nil
}

// ValidateAll validates a product's schedule list and rejects duplicate IDs.
func ValidateAll(schedules []Schedule) error {
	seen := map[string]struct{}{}
	for _, s := range schedules {
		if err := Validate(s); err != nil {
			return err
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("duplicate schedule id %q", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return nil
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func cronSchedule(s Schedule) (cron.Schedule, error) {
	loc := time.Local
	if tz := strings.TrimSpace(s.Timezone); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("invalid timezone %q", tz)
		}
	}
	parsed, err := cronParser.Parse(strings.TrimSpace(s.CronExpression))
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", s.CronExpression, err)
	}
	return &tzSchedule{inner: parsed, loc: loc}, nil
}

type tzSchedule struct {
	inner cron.Schedule
	loc   *time.Location
}

func (t *tzSchedule) Next(at time.Time) time.Time { return t.inner.Next(at.In(t.loc)) }

// RunAtTime parses the one-time instant (RFC3339, any offset).
func (s Schedule) RunAtTime() (time.Time, error) {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(s.RunAt))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid run_at %q: use RFC3339, for example 2026-10-05T21:30:00+05:30", s.RunAt)
	}
	return at, nil
}

// OneTimeMaxFailures is how many failed attempts a one-time schedule makes before it gives up.
const OneTimeMaxFailures = 3

// Cadence returns the cadence form as a duration (zero for cron schedules).
func (s Schedule) Cadence() time.Duration { return time.Duration(s.CadenceHours) * time.Hour }

// Quiet returns the quiet-period rule as durations.
func (s Schedule) Quiet() (quiet, maxDeferral time.Duration) {
	return time.Duration(s.QuietMinutes) * time.Minute, time.Duration(s.MaxDeferralHours) * time.Hour
}

// Inputs are the facts a scheduled tick is judged by.
type Inputs struct {
	Now time.Time
	// ActivatedAt is the stable instant from which a never-run cron schedule
	// computes its first occurrence. Callers should persist it; zero preserves
	// the safe behavior of waiting for an occurrence after Now.
	ActivatedAt time.Time
	// LastRun is when the schedule last ran successfully (zero = never).
	LastRun time.Time
	// SinceInteractive is how long ago the user last used the product.
	SinceInteractive time.Duration
	// LastAttempt is when the schedule last started a run, successful or not
	// (zero = never). With ConsecutiveFailures it drives the retry backoff:
	// without it a broken check-in re-fires on every tick, because only a
	// success moves LastRun.
	LastAttempt         time.Time
	ConsecutiveFailures int
}

// Retry backoff after a failed run: 30 minutes, doubling per consecutive
// failure, capped at retryBackoffMax (and never longer than the cadence).
const (
	retryBackoffBase = 30 * time.Minute
	retryBackoffMax  = 6 * time.Hour
)

// RetryBackoff is how long a schedule waits after its n-th consecutive
// failure before it may try again.
func RetryBackoff(consecutiveFailures int, cadence time.Duration) time.Duration {
	if consecutiveFailures <= 0 {
		return 0
	}
	backoff := retryBackoffBase
	for i := 1; i < consecutiveFailures && backoff < retryBackoffMax; i++ {
		backoff *= 2
	}
	if backoff > retryBackoffMax {
		backoff = retryBackoffMax
	}
	if cadence > 0 && backoff > cadence {
		backoff = cadence
	}
	return backoff
}

// Decision is Decide's answer.
type Decision struct {
	Run bool
	// Deferred is true when the run was due but held back by the quiet rule.
	Deferred bool
	Reason   string
	// ScheduledFor is the occurrence being honored (cron form only).
	ScheduledFor time.Time
}

// Decide is the pure timing rule: is this schedule due now, given when it
// last ran and how recently the user was active?
func Decide(s Schedule, in Inputs) Decision {
	if !s.Enabled {
		return Decision{Reason: "disabled"}
	}
	var overdue time.Duration
	var scheduledFor time.Time
	if strings.TrimSpace(s.RunAt) != "" {
		at, err := s.RunAtTime()
		if err != nil {
			return Decision{Reason: err.Error()}
		}
		if !in.LastRun.IsZero() {
			return Decision{Reason: "one-time schedule already ran"}
		}
		if at.After(in.Now) {
			return Decision{Reason: fmt.Sprintf("runs once at %s", at.Format(time.RFC3339))}
		}
		if in.ConsecutiveFailures >= OneTimeMaxFailures {
			return Decision{Reason: fmt.Sprintf("one-time schedule gave up after %d failed attempts", in.ConsecutiveFailures)}
		}
		scheduledFor = at
		overdue = in.Now.Sub(at)
	} else if strings.TrimSpace(s.CronExpression) != "" {
		cs, err := cronSchedule(s)
		if err != nil {
			return Decision{Reason: err.Error()}
		}
		after := in.LastRun
		if after.IsZero() {
			// A never-run schedule must use a stable activation point. Computing
			// Next from Now on every tick perpetually moves the target forward.
			after = in.ActivatedAt
			if after.IsZero() {
				return Decision{Reason: fmt.Sprintf("next at %s", cs.Next(in.Now).Format(time.RFC3339))}
			}
		}
		next := cs.Next(after)
		if next.After(in.Now) {
			return Decision{Reason: fmt.Sprintf("next at %s", next.Format(time.RFC3339))}
		}
		scheduledFor = next
		overdue = in.Now.Sub(next)
	} else {
		cadence := s.Cadence()
		if cadence <= 0 {
			return Decision{Reason: "no cadence"}
		}
		if !in.LastRun.IsZero() {
			overdue = in.Now.Sub(in.LastRun) - cadence
			if overdue < 0 {
				return Decision{Reason: fmt.Sprintf("next due in %s", (-overdue).Round(time.Minute))}
			}
		}
		if s.PreferredHour != nil && in.Now.Hour() < *s.PreferredHour {
			return Decision{Reason: fmt.Sprintf("waiting for %02d:00", *s.PreferredHour)}
		}
	}
	if backoff := RetryBackoff(in.ConsecutiveFailures, s.Cadence()); backoff > 0 && !in.LastAttempt.IsZero() {
		if wait := backoff - in.Now.Sub(in.LastAttempt); wait > 0 {
			return Decision{Reason: fmt.Sprintf("last run failed (%d in a row); retrying in %s", in.ConsecutiveFailures, wait.Round(time.Minute))}
		}
	}
	quiet, maxDeferral := s.Quiet()
	if quiet > 0 && in.SinceInteractive < quiet {
		if maxDeferral <= 0 || overdue < maxDeferral {
			return Decision{Deferred: true, Reason: fmt.Sprintf("user active %s ago (overdue by %s, forcing after %s)",
				in.SinceInteractive.Round(time.Second), overdue.Round(time.Minute), maxDeferral)}
		}
		return Decision{Run: true, ScheduledFor: scheduledFor, Reason: fmt.Sprintf("running despite recent activity — overdue by %s", overdue.Round(time.Minute))}
	}
	return Decision{Run: true, ScheduledFor: scheduledFor, Reason: "due"}
}
