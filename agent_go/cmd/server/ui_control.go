package server

// The broker holds only allowlisted presentation state. It never accepts DOM,
// credentials, URLs, or caller-selected user/workflow/product identities.
import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

//go:embed ui_control_contract.json
var uiControlContractJSON []byte

type uiViewCapability struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Actions    []string `json:"actions"`
	Targets    []string `json:"targets"`
	TargetKind string   `json:"target_kind,omitempty"`
}
type uiContract struct {
	Version int                `json:"version"`
	Product string             `json:"product"`
	Views   []uiViewCapability `json:"views"`
}

var uiControlContract = func() uiContract {
	var c uiContract
	if err := json.Unmarshal(uiControlContractJSON, &c); err != nil {
		panic(err)
	}
	return c
}()

type uiSnapshot struct {
	ObservedAt time.Time `json:"observed_at"`
	View       string    `json:"view"`
	Revision   int64     `json:"revision"`
	Visible    bool      `json:"visible"`
	Target     string    `json:"target,omitempty"`
}
type uiAction struct {
	RequestID                          string    `json:"request_id"`
	View                               string    `json:"view"`
	Action                             string    `json:"action"`
	Target                             string    `json:"target,omitempty"`
	ExpectedRevision                   *int64    `json:"expected_state_revision,omitempty"`
	Status                             string    `json:"status"`
	Code                               string    `json:"code,omitempty"`
	Visible                            bool      `json:"visible"`
	Revision                           int64     `json:"state_revision"`
	ExpiresAt                          time.Time `json:"expires_at"`
	session, binding, key, fingerprint string
	done                               chan struct{}
}
type uiBinding struct {
	id, token, session string
	seen               time.Time
	state              uiSnapshot
}
type uiControlBroker struct {
	mu       sync.Mutex
	bindings map[string]*uiBinding
	actions  map[string]*uiAction
	scopes   map[string]string
	now      func() time.Time
}

func newUIControlBroker() *uiControlBroker {
	return &uiControlBroker{bindings: map[string]*uiBinding{}, actions: map[string]*uiAction{}, scopes: map[string]string{}, now: time.Now}
}
func (api *StreamingAPI) uiBroker() *uiControlBroker {
	api.uiControlOnce.Do(func() { api.uiControl = newUIControlBroker() })
	return api.uiControl
}
func (b *uiControlBroker) setScope(session, workspace string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if previous := b.scopes[session]; previous != "" && previous != workspace {
		for id, c := range b.bindings {
			if c.session == session {
				delete(b.bindings, id)
			}
		}
		for _, a := range b.actions {
			if a.session == session {
				b.finish(a, "cancelled", "inactive_scope")
			}
		}
	}
	// Registration is server-owned. Scope must never come from a browser body.
	if len(b.scopes) < 4096 || b.scopes[session] != "" {
		b.scopes[session] = workspace
	}
}
func (b *uiControlBroker) scope(session string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.scopes[session]
}

const uiControlBindingLease = 6 * time.Minute

func uiTerminal(status string) bool { return status != "accepted" && status != "applying" }
func (b *uiControlBroker) finish(a *uiAction, status, code string) {
	if uiTerminal(a.Status) {
		return
	}
	a.Status, a.Code = status, code
	log.Printf("[UI-CONTROL] request=%s view=%s action=%s status=%s code=%s", a.RequestID, a.View, a.Action, status, code)
	close(a.done)
}
func (b *uiControlBroker) prune() {
	now := b.now()
	for id, c := range b.bindings {
		// The pane renews every 5 minutes (not every 10s): the agent's
		// explicit commands arrive over the chat's SSE, so the lease only
		// needs to outlive the renewal interval.
		if now.Sub(c.seen) > uiControlBindingLease {
			delete(b.bindings, id)
			for _, a := range b.actions {
				if a.binding == id {
					b.finish(a, "expired", "browser_disconnected")
				}
			}
		}
	}
	for id, a := range b.actions {
		if !now.Before(a.ExpiresAt) {
			b.finish(a, "expired", "timeout")
		}
		if now.Sub(a.ExpiresAt) > 10*time.Minute {
			delete(b.actions, id)
		}
	}
}
func (b *uiControlBroker) bind(session string) (*uiBinding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	if len(b.bindings) >= 256 {
		return nil, fmt.Errorf("capacity_exceeded")
	}
	c := &uiBinding{id: uuid.NewString(), token: uuid.NewString(), session: session, seen: b.now()}
	b.bindings[c.id] = c
	copy := *c
	return &copy, nil
}
func (b *uiControlBroker) client(session, id, token string) (*uiBinding, error) {
	c := b.bindings[id]
	if c == nil || c.session != session || c.token != token {
		return nil, fmt.Errorf("inactive_scope")
	}
	return c, nil
}
func (b *uiControlBroker) unbind(session, id, token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.client(session, id, token); err != nil {
		return err
	}
	delete(b.bindings, id)
	for _, a := range b.actions {
		if a.binding == id {
			b.finish(a, "cancelled", "inactive_scope")
		}
	}
	return nil
}

// validObservedUIView accepts the view a page reports it is showing. It is an observation, not a command: a page on a tab the agent cannot open (Code's Terminal,
// Plan or Suggestions) must still register and renew, or the agent sees "browser_disconnected" for as long as the person looks at that tab (Code on Excellence,
// 2026-10-04). Which views an action may open stays with the contract (validateUIActionForContract); this only keeps the reported name well-formed.
var observedUIViewName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func validObservedUIView(view string) bool {
	return view == "" || observedUIViewName.MatchString(view)
}

func validUIViewForContract(contract uiContract, view string) bool {
	for _, v := range contract.Views {
		if v.ID == view {
			return true
		}
	}
	return false
}

func uiContractForScope(scope string) uiContract {
	// isProjectWorkspacePath already reads a project the same way with or without
	// its _users/<id>/ prefix; who may bind is checked by the route, not here.
	if clean := path.Clean(strings.Trim(strings.TrimSpace(scope), "/")); isProjectWorkspacePath(clean) {
		return projectUIControlContract(clean)
	}
	return uiControlContract
}
func validateUIAction(view, action, target string) error {
	return validateUIActionForContract(uiControlContract, view, action, target)
}

func validateUIActionForContract(contract uiContract, view, action, target string) error {
	for _, v := range contract.Views {
		if v.ID != view {
			continue
		}
		found := false
		for _, a := range v.Actions {
			if action == a {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("unsupported_action")
		}
		if (action == "open" || action == "refresh") && target == "" {
			return nil
		}
		if (action == "open" || action == "refresh") && strings.TrimSpace(target) != "" && len(target) <= 1024 {
			switch v.TargetKind {
			case "plan_step_id", "report_tab":
				return nil
			case "view_section":
				for _, t := range v.Targets {
					if t == target {
						return nil
					}
				}
			case "workspace_file_path":
				cleaned := path.Clean(strings.TrimSpace(target))
				if cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../") && !strings.HasPrefix(cleaned, "/") && !strings.Contains(cleaned, "\\") {
					return nil
				}
			}
		}
		if action == "expand" {
			for _, t := range v.Targets {
				if t == target {
					return nil
				}
			}
		}
		return fmt.Errorf("unsupported_target")
	}
	return fmt.Errorf("unsupported_view")
}
func (b *uiControlBroker) snapshot(session string) (uiSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	c, err := b.onlyClient(session)
	if err != nil {
		return uiSnapshot{}, err
	}
	return c.state, nil
}
func (b *uiControlBroker) onlyClient(session string) (*uiBinding, error) {
	var found *uiBinding
	for _, c := range b.bindings {
		if c.session == session {
			if found != nil {
				return nil, fmt.Errorf("ambiguous_client")
			}
			found = c
		}
	}
	if found == nil {
		return nil, fmt.Errorf("browser_disconnected")
	}
	return found, nil
}
func (b *uiControlBroker) submit(session, view, action, target, key string, revision *int64) (uiAction, bool, error) {
	return b.submitForContract(session, uiControlContract, view, action, target, key, revision)
}

func (b *uiControlBroker) submitForContract(session string, contract uiContract, view, action, target, key string, revision *int64) (uiAction, bool, error) {
	if err := validateUIActionForContract(contract, view, action, target); err != nil {
		return uiAction{}, false, err
	}
	if len(key) > 128 {
		return uiAction{}, false, fmt.Errorf("invalid_idempotency_key")
	}
	fp, _ := json.Marshal([]interface{}{view, action, target, revision})
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	if key != "" {
		for _, a := range b.actions {
			if a.session == session && a.key == key {
				if a.fingerprint != string(fp) {
					return uiAction{}, false, fmt.Errorf("idempotency_conflict")
				}
				return *a, false, nil
			}
		}
	}
	c, err := b.onlyClient(session)
	if err != nil {
		return uiAction{}, false, err
	}
	if revision != nil && *revision != c.state.Revision {
		return uiAction{}, false, fmt.Errorf("stale_state")
	}
	for _, a := range b.actions {
		if a.session == session && !uiTerminal(a.Status) {
			return uiAction{}, false, fmt.Errorf("action_in_progress")
		}
	}
	if len(b.actions) >= 2048 {
		return uiAction{}, false, fmt.Errorf("capacity_exceeded")
	}
	a := &uiAction{RequestID: uuid.NewString(), View: view, Action: action, Target: target, ExpectedRevision: revision, Status: "accepted", ExpiresAt: b.now().Add(10 * time.Second), session: session, binding: c.id, key: key, fingerprint: string(fp), done: make(chan struct{})}
	b.actions[a.RequestID] = a
	return *a, true, nil
}

// Sync atomically claims accepted commands. SSE is a wake-up only; historical
// presentations and duplicate events cannot re-execute an already claimed action.
func (b *uiControlBroker) syncClient(session, id, token string, state uiSnapshot) ([]uiAction, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	if state.Revision < 0 || !validObservedUIView(state.View) {
		return nil, fmt.Errorf("invalid_state")
	}
	c, err := b.client(session, id, token)
	if err != nil {
		return nil, err
	}
	if state.Revision < c.state.Revision {
		return nil, fmt.Errorf("stale_state")
	}
	c.seen = b.now()
	state.ObservedAt = c.seen
	c.state = state
	result := []uiAction{}
	for _, a := range b.actions {
		if a.binding == id && a.Status == "accepted" {
			if a.ExpectedRevision != nil && *a.ExpectedRevision != state.Revision {
				b.finish(a, "rejected", "stale_state")
				continue
			}
			a.Status = "applying"
			result = append(result, *a)
		}
	}
	return result, nil
}
func (b *uiControlBroker) ack(session, id, token, request, status, code string, state uiSnapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	if state.Revision < 0 || !validObservedUIView(state.View) {
		return fmt.Errorf("invalid_state")
	}
	c, err := b.client(session, id, token)
	if err != nil {
		return err
	}
	a := b.actions[request]
	if a == nil || a.session != session || a.binding != id {
		return fmt.Errorf("unknown_request")
	}
	if uiTerminal(a.Status) {
		return nil
	}
	if a.Status != "applying" {
		return fmt.Errorf("invalid_transition")
	}
	if state.Revision < c.state.Revision {
		return fmt.Errorf("stale_state")
	}
	if status == "applied" {
		if !state.Visible || state.View != a.View || code != "" || (a.View == "flow" && a.Target != "" && state.Target != a.Target) {
			return fmt.Errorf("invalid_receipt")
		}
	} else if status == "failed" || status == "rejected" || status == "cancelled" {
		switch code {
		case "target_not_found", "render_failed", "user_interrupted", "inactive_scope", "stale_state", "timeout":
		default:
			return fmt.Errorf("invalid_receipt")
		}
	} else {
		return fmt.Errorf("invalid_receipt")
	}
	a.Visible = status == "applied"
	a.Revision = state.Revision
	state.ObservedAt = b.now()
	c.state = state
	b.finish(a, status, code)
	return nil
}
func (b *uiControlBroker) result(session, id string) (uiAction, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune()
	a := b.actions[id]
	if a == nil || a.session != session {
		return uiAction{}, fmt.Errorf("unknown_request")
	}
	return *a, nil
}

// expirePendingAction makes the synchronous tool return a terminal receipt.
func (b *uiControlBroker) expirePendingAction(session, id, code string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a := b.actions[id]; a != nil && a.session == session {
		b.finish(a, "expired", code)
	}
}
