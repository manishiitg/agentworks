// Package conditiontrigger implements condition callers independently of products.
// Hosts supply sandbox execution, durable storage and authorized target adapters.
package conditiontrigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	MaxOutput      = 64 * 1024
	MaxPayload     = 16 * 1024
	MaxEvents      = 20
	MaxPending     = 100
	MaxRows        = 4096
	RecoveryWindow = 6 * 24 * time.Hour
)

var ErrBacklog = errors.New("watcher dispatch backlog full")
var ErrChecking = errors.New("watcher check already running")
var ErrPaused = errors.New("watcher is paused")

// HoldError proves admission stopped before target execution.
type HoldError struct{ Err error }

func (e HoldError) Error() string { return e.Err.Error() }
func (e HoldError) Unwrap() error { return e.Err }

type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

type Target struct {
	Kind      string                 `json:"kind"` // agent, function, trigger
	Name      string                 `json:"name,omitempty"`
	Function  string                 `json:"function,omitempty"`
	TriggerID string                 `json:"trigger_id,omitempty"`
	ChatID    string                 `json:"chat_id,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Routes    map[string]string      `json:"route_selections,omitempty"`
	Groups    []string               `json:"group_names,omitempty"`
}

// Payload becomes named function arguments, or event data for messages/triggers.
// Target selection and constant arguments are never taken from script output.
type GitHubPR struct {
	Repository string   `json:"repository"`
	Number     int      `json:"number"`
	Mode       string   `json:"mode"` // checks, deployment, autofix
	Worktree   string   `json:"worktree,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Login      string   `json:"login,omitempty"`       // selected by the server
	HeadSHA    string   `json:"head_sha,omitempty"`    // fixed for a one-time wait
	ReviewBots []string `json:"review_bots,omitempty"` // explicitly chosen by the owner
}

type Config struct {
	GitHubPR       *GitHubPR `json:"github_pr,omitempty"`
	CheckMode      string    `json:"check_mode,omitempty"`
	EmailSource    string    `json:"email_source,omitempty"`
	EmailAccountID string    `json:"email_account_id,omitempty"`
	EmailSince     int64     `json:"email_since,omitempty"`
	EventAuthMode  string    `json:"event_auth_mode,omitempty"`
	EventSecret    string    `json:"event_secret_name,omitempty"`
	Once           bool      `json:"once,omitempty"`
	Script         string    `json:"script"`
	SHA256         string    `json:"sha256"`
	Secrets        []string  `json:"secrets,omitempty"`
	Target         Target    `json:"target"`
}

var secretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (c Config) PersonalGmail() bool {
	return c.CheckMode == "inbox" && c.EmailSource == "personal_gmail"
}

func (c Config) WakesOnChanges() bool { return c.WakesOnEvents() || c.CheckMode == "inbox" }

func (c Config) WakesOnEvents() bool { return c.CheckMode == "event" || c.CheckMode == "both" }

func (c Config) Validate() error {
	if c.EmailSource != "" && c.EmailSource != "crew_inbox" && c.EmailSource != "personal_gmail" {
		return errors.New("email_source must be crew_inbox or personal_gmail")
	}
	if c.EmailSource != "" && c.CheckMode != "inbox" {
		return errors.New("email_source requires check_mode=inbox")
	}
	if c.PersonalGmail() && c.EmailAccountID == "" {
		return errors.New("choose your Gmail inbox account in your project's Vault panel or in chat before creating this Watcher")
	}

	switch c.CheckMode {
	case "", "schedule", "event", "both", "inbox":
	default:
		return errors.New("check_mode must be schedule, event, both or inbox")
	}
	if c.EventAuthMode != "" && c.EventAuthMode != "bearer" && c.EventAuthMode != "github" {
		return errors.New("event_auth_mode must be bearer or github")
	}
	if c.WakesOnEvents() {
		selected := false
		for _, name := range c.Secrets {
			selected = selected || name == c.EventSecret
		}
		if !secretName.MatchString(c.EventSecret) || !selected {
			return errors.New("select an existing secret for webhook authentication and set event_secret_name to its name")
		}
	}
	if c.GitHubPR != nil {
		p := c.GitHubPR
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(p.Repository) || strings.Contains(p.Repository, "..") || p.Number < 1 {
			return errors.New("choose a GitHub repository and pull request number")
		}
		if p.Mode != "checks" && p.Mode != "deployment" && p.Mode != "autofix" {
			return errors.New("GitHub watcher mode must be checks, deployment or autofix")
		}
		if p.Login == "" || p.Branch == "" || p.HeadSHA == "" {
			return errors.New("save the GitHub account and PR head before enabling this watcher")
		}
		if len(p.Worktree) > 1024 {
			return errors.New("worktree path is too long")
		}
		if p.Worktree != "" && (path.IsAbs(p.Worktree) || path.Clean(p.Worktree) != p.Worktree || p.Worktree == ".." || strings.HasPrefix(p.Worktree, "../") || strings.ContainsAny(p.Worktree, "\\\x00\r\n")) {
			return errors.New("worktree must be a project-relative folder")
		}
		if c.CheckMode != "" && c.CheckMode != "schedule" || c.EmailSource != "" || len(c.Secrets) != 0 || c.Script != "" || c.Once || c.Target.Kind != "agent" || c.Target.ChatID == "" || c.Target.ChatID == "new" {
			return errors.New("a GitHub watcher checks on a schedule and sends to one existing Crew chat")
		}
		if len(p.ReviewBots) > 10 {
			return errors.New("choose at most ten review bots")
		}
		for _, bot := range p.ReviewBots {
			if !regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\[bot\])?$`).MatchString(bot) {
				return errors.New("use the exact GitHub login of each chosen review bot")
			}
		}
	} else {
		if !strings.HasPrefix(c.Script, "code/triggers/") || path.Clean(c.Script) != c.Script || !strings.HasSuffix(c.Script, ".py") || strings.ContainsAny(c.Script, "\\\x00\r\n") {
			return errors.New("script must be a Python file under code/triggers")
		}
		if b, err := hex.DecodeString(c.SHA256); err != nil || len(b) != sha256.Size {
			return errors.New("save the script's SHA256 before enabling the watcher")
		}
	}
	if len(c.Secrets) > 20 {
		return errors.New("select at most 20 secrets")
	}
	seen := map[string]bool{}
	for _, s := range c.Secrets {
		if !secretName.MatchString(s) || seen[s] {
			return errors.New("secret names must be unique environment variable names")
		}
		seen[s] = true
	}
	switch c.Target.Kind {
	case "agent":
		if strings.TrimSpace(c.Target.Message) == "" || len(c.Target.Message) > 8*1024 {
			return errors.New("agent target needs a saved message of at most 8 KiB")
		}
	case "function":
		if c.Target.Name == "" || c.Target.Function == "" {
			return errors.New("function target needs name and function")
		}
	case "trigger":
		if c.Target.Name == "" || c.Target.TriggerID == "" {
			return errors.New("route target needs name and an existing internal trigger_id")
		}
	case "route":
		if c.Target.Name == "" || len(c.Target.Groups) == 0 {
			return errors.New("route target needs a workflow name and saved group_names")
		}
	default:
		return errors.New("target kind must be agent, function, route or trigger")
	}
	return nil
}

func Hash(source string) string           { b := sha256.Sum256([]byte(source)); return hex.EncodeToString(b[:]) }
func (c Config) Revision() string         { b, _ := json.Marshal(c); return Hash(string(b)) }
func Submission(key, event string) string { return "condition-" + Hash(key+"\x00"+event) }

type Event struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
}
type Output struct {
	Done   bool            `json:"done,omitempty"`
	Events []Event         `json:"events"`
	State  json.RawMessage `json:"state"`
}
type Row struct {
	Event        Event     `json:"event"`
	Revision     string    `json:"revision"`
	Status       string    `json:"status"`
	Receipt      string    `json:"receipt,omitempty"`
	DispatchedAt time.Time `json:"dispatched_at,omitempty"`
	Error        string    `json:"error,omitempty"`
	SeenAt       time.Time `json:"seen_at"`
	AttemptAt    time.Time `json:"attempt_at,omitempty"`
	NextAttempt  time.Time `json:"next_attempt,omitempty"`
}
type Ledger struct {
	Done            bool            `json:"done,omitempty"`
	CompletedAt     time.Time       `json:"completed_at,omitempty"`
	OnceEventID     string          `json:"once_event_id,omitempty"`
	OnceRevision    string          `json:"once_revision,omitempty"`
	OnceCompletedAt time.Time       `json:"once_completed_at,omitempty"`
	State           json.RawMessage `json:"state"`
	Rows            []Row           `json:"events"`
	// Tombstones are compact identities, retained after full history is pruned.
	Tombstones map[string]time.Time `json:"tombstones,omitempty"`
}

type Store interface {
	Load(context.Context, string) (Ledger, error)
	Save(context.Context, string, Ledger) error
}
type Runner func(context.Context, Config, json.RawMessage) ([]byte, error)
type Dispatcher func(context.Context, Config, Event, string) (string, error)

// Engine locks belong to the service, not a model turn. A restarted host resumes
// dispatching rows with the exact persisted event and the same submission key.
type Engine struct {
	Store    Store
	Run      Runner
	Dispatch Dispatcher
	locks    sync.Map
	workers  sync.Map
}

func (e *Engine) lock(key string) *sync.Mutex {
	v, _ := e.locks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func pending(s string) bool { return s == "seen" || s == "dispatching" }
func Pending(l Ledger) int {
	n := 0
	for _, r := range l.Rows {
		if pending(r.Status) {
			n++
		}
	}
	return n
}

func Parse(raw []byte) (Output, error) {
	var o Output
	if len(raw) > MaxOutput {
		return o, errors.New("watcher output exceeds 64 KiB")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return o, fmt.Errorf("watcher output must be {events: [...], state: {...}}: %w", err)
	}
	// Reject trailing JSON/text as well as multiple documents.
	if json.Valid(raw) == false {
		return o, errors.New("watcher output must be one JSON document")
	}
	if o.Events == nil || len(o.Events) > MaxEvents {
		return o, errors.New("events must be an array of at most 20 items")
	}
	if len(o.State) == 0 {
		o.State = json.RawMessage(`{}`)
	}
	var state map[string]interface{}
	if json.Unmarshal(o.State, &state) != nil || state == nil {
		return o, errors.New("state must be a JSON object")
	}
	seen := map[string]bool{}
	for _, ev := range o.Events {
		if strings.TrimSpace(ev.ID) == "" || len(ev.ID) > 256 || seen[ev.ID] || ev.Payload == nil {
			return o, errors.New("each event needs a unique id (1–256 bytes) and an object payload")
		}
		seen[ev.ID] = true
		b, _ := json.Marshal(ev.Payload)
		if len(b) > MaxPayload {
			return o, errors.New("event payload exceeds 16 KiB")
		}
	}
	return o, nil
}

// Poll atomically records every new event and its cursor in one durable write.
// It never dispatches while holding the poll lock and never advances on failure.
func (e *Engine) Poll(ctx context.Context, key string, c Config, now time.Time) (int, error) {
	m := e.lock(key)
	if !m.TryLock() {
		return 0, ErrChecking
	}
	defer m.Unlock()
	l, err := e.Store.Load(ctx, key)
	if err != nil {
		return 0, err
	}
	// Selection survives failed delivery and restart; a one-time wait never
	// runs the detector again once its event is durably queued.
	if l.Done || c.Once && l.OnceEventID != "" {
		return 0, nil
	}
	if Pending(l) >= MaxPending {
		return 0, ErrBacklog
	}
	raw, err := e.Run(ctx, c, l.State)
	if err != nil {
		return 0, err
	}
	o, err := Parse(raw)
	if err != nil {
		return 0, err
	}
	if l.Tombstones == nil {
		l.Tombstones = map[string]time.Time{}
	}
	rows := l.Rows[:0]
	for index, r := range l.Rows {
		if !pending(r.Status) && r.Status != "needs_reconciliation" && (now.Sub(r.SeenAt) > 30*24*time.Hour || index < len(l.Rows)-200) {
			l.Tombstones[Hash(r.Event.ID)] = r.SeenAt.Add(120 * 24 * time.Hour)
		} else {
			rows = append(rows, r)
		}
	}
	l.Rows = rows
	for id, until := range l.Tombstones {
		if now.After(until) {
			delete(l.Tombstones, id)
		}
	}
	for len(l.Tombstones) > 8192 {
		var oldest string
		var at time.Time
		for id, until := range l.Tombstones {
			if oldest == "" || until.Before(at) {
				oldest, at = id, until
			}
		}
		delete(l.Tombstones, oldest)
	}
	known := map[string]bool{}
	for _, r := range l.Rows {
		known[r.Event.ID] = true
	}
	n := 0
	for _, ev := range o.Events {
		if known[ev.ID] || l.Tombstones[Hash(ev.ID)].After(now) {
			continue
		}
		if c.Once && l.OnceEventID != "" {
			l.Rows = append(l.Rows, Row{Event: ev, Revision: c.Revision(), Status: "skipped", Error: "not sent (one-time watcher)", SeenAt: now})
			continue
		}
		l.Rows = append(l.Rows, Row{Event: ev, Revision: c.Revision(), Status: "seen", SeenAt: now})
		n++
		if c.Once {
			l.OnceEventID, l.OnceRevision = ev.ID, c.Revision()
		}
	}
	if Pending(l) > MaxPending || len(l.Rows) > MaxRows {
		return 0, ErrBacklog
	}
	l.State = o.State
	l.Done = o.Done
	return n, e.Store.Save(ctx, key, l)
}

// Drain is separate from Poll. Dispatchers return durable acceptance receipts,
// not the target's final answer. One worker per caller supplies backpressure.
func (e *Engine) Drain(ctx context.Context, key string, c Config, now time.Time) error {
	if _, loaded := e.workers.LoadOrStore(key, true); loaded {
		return nil
	}
	defer e.workers.Delete(key)
	for i := 0; i < MaxEvents; i++ {
		m := e.lock(key)
		m.Lock()
		l, err := e.Store.Load(ctx, key)
		if err != nil {
			m.Unlock()
			return err
		}
		if c.Once && !l.OnceCompletedAt.IsZero() {
			m.Unlock()
			return nil
		}
		idx := -1
		for j, r := range l.Rows {
			if pending(r.Status) && !r.NextAttempt.After(now) && (!c.Once || l.OnceEventID == "" || r.Event.ID == l.OnceEventID) {
				idx = j
				break
			}
		}
		if idx == -1 {
			if l.Done && Pending(l) == 0 && l.CompletedAt.IsZero() {
				complete := true
				for _, row := range l.Rows {
					if row.Status == "failed" || row.Status == "needs_reconciliation" {
						complete = false
					}
				}
				if complete {
					l.CompletedAt = now
					err = e.Store.Save(ctx, key, l)
				}
			}
			m.Unlock()
			return err
		}
		r := l.Rows[idx]
		previousStatus, previousAttempt := r.Status, r.AttemptAt
		if r.Revision != c.Revision() {
			r.Status, r.Error = "needs_reconciliation", "trigger configuration changed before dispatch"
		} else if r.Status == "dispatching" && now.Sub(r.AttemptAt) >= RecoveryWindow {
			r.Status, r.Error = "needs_reconciliation", "uncertain call exceeded its safe retry window"
		} else {
			if c.Once && l.OnceEventID == "" {
				l.OnceEventID, l.OnceRevision = r.Event.ID, c.Revision()
			}
			r.Status = "dispatching"
			if r.AttemptAt.IsZero() {
				r.AttemptAt = now
			}
		}
		l.Rows[idx] = r
		if err = e.Store.Save(ctx, key, l); err != nil {
			m.Unlock()
			return err
		}
		m.Unlock()
		if r.Status == "needs_reconciliation" {
			continue
		}
		receipt, dispatchErr := e.Dispatch(ctx, c, r.Event, Submission(key, r.Event.ID))
		m.Lock()
		l, err = e.Store.Load(ctx, key)
		if err != nil {
			m.Unlock()
			return err
		}
		for j := range l.Rows {
			if l.Rows[j].Event.ID == r.Event.ID {
				if dispatchErr == nil {
					l.Rows[j].Status = "dispatched"
					l.Rows[j].Receipt = receipt
					l.Rows[j].DispatchedAt = now
					l.Rows[j].Error = ""
					if c.Once {
						l.OnceCompletedAt = now
					}
				} else {
					l.Rows[j].Error = dispatchErr.Error()
					l.Rows[j].NextAttempt = now.Add(5 * time.Minute)
					if errors.Is(dispatchErr, ErrPaused) {
						l.Rows[j].Status = previousStatus
						l.Rows[j].AttemptAt = previousAttempt
						l.Rows[j].NextAttempt = time.Time{}
					}
					var permanent PermanentError
					if errors.As(dispatchErr, &permanent) {
						l.Rows[j].Status = "failed"
					}
				}
				break
			}
		}
		if c.Once && !l.OnceCompletedAt.IsZero() {
			for j := range l.Rows {
				if pending(l.Rows[j].Status) && l.Rows[j].Event.ID != l.OnceEventID {
					l.Rows[j].Status, l.Rows[j].Error = "skipped", "not sent (one-time watcher)"
				}
			}
		}
		err = e.Store.Save(ctx, key, l)
		m.Unlock()
		if err != nil {
			return err
		}
		if dispatchErr != nil {
			return dispatchErr
		}
		if c.Once {
			return nil
		}
	}
	return nil
}

func (e *Engine) Read(ctx context.Context, key string) (Ledger, error) {
	m := e.lock(key)
	m.Lock()
	defer m.Unlock()
	return e.Store.Load(ctx, key)
}
