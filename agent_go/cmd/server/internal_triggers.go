package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"strings"
)

// Internal dispatch outcomes shared by both directions. Callers use these
// instead of HTTP status codes to fail fast on revoked or misbound triggers.
var (
	ErrInternalTriggerNotFound  = errors.New("internal trigger not found")
	ErrInternalTriggerDisabled  = errors.New("internal trigger is disabled")
	ErrInternalTriggerNotBound  = errors.New("trigger is not an internal trigger")
	ErrInternalCallerMismatch   = errors.New("internal trigger caller mismatch")
	ErrInternalTriggerRunGone   = errors.New("internal trigger run not found")
	ErrInternalTriggerPayload   = errors.New("internal trigger payload must be JSON within 1 MiB")
	ErrWebhookConcurrencyLimit  = errors.New("webhook concurrency limit reached")
	ErrWebhookRunStoreMissing   = errors.New("run storage is unavailable")
	ErrProductTriggerNotRecord  = errors.New("cannot record trigger run")
	ErrProductTriggerNotPersist = errors.New("cannot persist trigger payload")
	ErrProductTriggerNotStart   = errors.New("trigger run could not start")
)

// Internal trigger bindings connect a Workflow and a Crew on the same
// deployment without public URLs or secrets. A binding is kind "internal"
// plus a caller stamp on an ordinary trigger; the target is whichever
// manifest or schedule list holds the trigger.
const (
	triggerKindInternal = "internal"

	triggerCallerWorkflow = "workflow"
	triggerCallerCrew     = "crew"
	// triggerCallerUser is a signed-in user's external connection (MCP or the
	// agentworks CLI). Only the server stamps it, from the authenticated
	// request; agent tool schemas never offer it.
	triggerCallerUser = "user"
)

// triggerCaller stamps which resource may invoke an internal trigger.
// Workflow callers use the workflow manifest ID; crew callers use the project
// ID with its profile.
type triggerCaller struct {
	Type      string `json:"type"` // "workflow" or "crew"
	ID        string `json:"id"`
	ProfileID string `json:"profile_id,omitempty"`
}

func normalizeTriggerKind(kind string) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(kind), triggerKindInternal):
		return triggerKindInternal
	case isFunctionTriggerKind(kind):
		return triggerKindFunction
	}
	return ""
}

func isInternalTriggerKind(kind string) bool {
	return normalizeTriggerKind(kind) == triggerKindInternal
}

// validateTriggerCaller checks the shape of an internal trigger's caller
// stamp. wantType is the only caller type the owning side accepts.
func validateTriggerCaller(caller *triggerCaller, wantType string) error {
	if caller == nil {
		return fmt.Errorf("internal trigger requires a caller")
	}
	if got := strings.ToLower(strings.TrimSpace(caller.Type)); got != wantType {
		return fmt.Errorf("internal trigger caller type must be %q", wantType)
	}
	if strings.TrimSpace(caller.ID) == "" {
		return fmt.Errorf("internal trigger caller id is required")
	}
	return nil
}

// validateAnyTriggerCaller accepts either caller type: a Crew and a workflow
// may each call the other and themselves' kind (crew→crew, workflow→workflow).
func validateAnyTriggerCaller(caller *triggerCaller) error {
	if caller == nil {
		return fmt.Errorf("internal trigger requires a caller")
	}
	switch strings.ToLower(strings.TrimSpace(caller.Type)) {
	case triggerCallerWorkflow:
		return validateTriggerCaller(caller, triggerCallerWorkflow)
	case triggerCallerCrew:
		return validateTriggerCaller(caller, triggerCallerCrew)
	case triggerCallerUser:
		return validateTriggerCaller(caller, triggerCallerUser)
	default:
		return fmt.Errorf("internal trigger caller type must be %q or %q", triggerCallerWorkflow, triggerCallerCrew)
	}
}

// triggerCallerToolSchema describes the caller stamp for agent tools.
// validTypes restricts the accepted caller types when provided.
func triggerCallerToolSchema(validTypes ...string) map[string]interface{} {
	typeSchema := map[string]interface{}{"type": "string"}
	if len(validTypes) > 0 {
		typeSchema["enum"] = validTypes
	}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{
		"type": typeSchema, "id": map[string]interface{}{"type": "string"}, "profile_id": map[string]interface{}{"type": "string"},
	}}
}

// triggerCallerFromArgs reads a caller stamp from agent tool arguments.
func triggerCallerFromArgs(args map[string]interface{}) *triggerCaller {
	raw, ok := args["caller"].(map[string]interface{})
	if !ok {
		return nil
	}
	ctype, _ := raw["type"].(string)
	cid, _ := raw["id"].(string)
	profile, _ := raw["profile_id"].(string)
	if strings.TrimSpace(ctype) == "" && strings.TrimSpace(cid) == "" {
		return nil
	}
	return &triggerCaller{Type: ctype, ID: cid, ProfileID: profile}
}

// findWorkflowManifestByID resolves a workflow caller by manifest ID.
func findWorkflowManifestByID(ctx context.Context, workflowID string) (string, *WorkflowManifest, error) {
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return "", nil, err
	}
	id := strings.TrimSpace(workflowID)
	for _, item := range discovered {
		if item.Manifest != nil && strings.TrimSpace(item.Manifest.ID) == id {
			return item.WorkspacePath, item.Manifest, nil
		}
	}
	return "", nil, fmt.Errorf("workflow %q not found", id)
}

// crewProjectExists reports whether a crew project exists for the user.
func (s *ProductScheduleService) crewProjectExists(ctx context.Context, userID, profileID, projectID string) bool {
	if strings.TrimSpace(profileID) == "" {
		profileID = "work"
	}
	// A calling Code has no schedules, so it is verified the way a Code turn
	// is: the caller's own, or one shared with them.
	if strings.EqualFold(strings.TrimSpace(profileID), codeproduct.ProfileID) {
		if s.registry == nil {
			return false
		}
		profile, err := s.registry.Resolve(profileID, 0, userID)
		if err != nil {
			return false
		}
		_, err = resolveCrewProjectBinding(ctx, userID, profile, projectID, "")
		return err == nil
	}
	// Crews are shared server-wide, so a caller Crew may belong to any owner.
	_, _, _, _, err := s.projectManifestAnyOwner(ctx, userID, profileID, projectID)
	return err == nil
}

// internalTriggerDeliveryResult is the synchronous outcome of one internal
// trigger dispatch. Status is "accepted", "queued", or the existing run state
// on a duplicate delivery.
type internalTriggerDeliveryResult struct {
	RunID      string
	DeliveryID string
	Duplicate  bool
	Status     string
}

// internalCrewTriggerCall invokes a Crew trigger from a workflow step. No
// secret is presented; the binding caller stamp is the authorization.
// WorkflowRunID and WorkflowStepID stamp the run record for cost
// attribution alongside the caller workflow ID.
type internalCrewTriggerCall struct {
	UserID         string
	ProfileID      string
	ProjectID      string
	TriggerID      string
	Caller         triggerCaller
	WorkflowRunID  string
	WorkflowStepID string
	DeliveryID     string
	Event          string
	Payload        []byte
	// CallerLabel is the caller's display name for the turn's source note.
	CallerLabel string
}

// internalWorkflowTriggerCall invokes a workflow trigger from a Crew run.
type internalWorkflowTriggerCall struct {
	WorkflowID string
	TriggerID  string
	Caller     triggerCaller
	DeliveryID string
	Event      string
	Payload    []byte
}

// matchesPresented reports whether the binding stamp authorizes the presented
// caller. Both stamps must name wantType; IDs compare exact after trimming,
// and crew stamps must also agree on profile.
func (c *triggerCaller) matchesPresented(wantType string, presented triggerCaller) bool {
	if c == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.Type), wantType) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(presented.Type), wantType) {
		return false
	}
	if strings.TrimSpace(presented.ID) != strings.TrimSpace(c.ID) {
		return false
	}
	if wantType == triggerCallerCrew && strings.TrimSpace(presented.ProfileID) != strings.TrimSpace(c.ProfileID) {
		return false
	}
	return true
}

// matchesAnyPresented is matchesPresented for bindings that accept either
// caller type: the presented type must be a known caller type and equal the
// binding's own type.
func (c *triggerCaller) matchesAnyPresented(presented triggerCaller) bool {
	wantType := strings.ToLower(strings.TrimSpace(presented.Type))
	if wantType != triggerCallerWorkflow && wantType != triggerCallerCrew && wantType != triggerCallerUser {
		return false
	}
	return c.matchesPresented(wantType, presented)
}

// checkInternalPayload enforces the same 1 MiB JSON guardrail as the public
// endpoints so internal callers cannot smuggle larger inputs past the cap.
func checkInternalPayload(payload []byte) error {
	if len(payload) > maxWebhookBodyBytes || !json.Valid(payload) {
		return ErrInternalTriggerPayload
	}
	return nil
}

// normalizeInternalProfileID defaults an empty crew profile to "work".
func normalizeInternalProfileID(profileID string) string {
	if strings.TrimSpace(profileID) == "" {
		return "work"
	}
	return profileID
}
