package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// The transfer describes the hook, never its old credential (even encrypted).
// The normal destination hook issues its own credential inside the Team home.
type teamOnlineWebhook struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Message  string `json:"message"`
	AuthMode string `json:"auth_mode"`
}

func teamOnlineWebhookRevision(h teamOnlineWebhook) string {
	h.Enabled = false
	raw, _ := json.Marshal(h)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func teamOnlineWebhookID(source, id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("team-online-webhook\x00"+source+"\x00"+id)).String()
}

func exportTeamOnlineWebhook(ctx context.Context, source triggerTarget, id string) (teamOnlineWebhook, error) {
	var out teamOnlineWebhook
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != source.CrewOwner {
		return out, fmt.Errorf("choose a webhook in your own Crew")
	}
	if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows("webhooks.json", false) {
		return out, fmt.Errorf("this connection does not allow the source webhook")
	}
	hooks, err := readProjectWebhookDefinitions(ctx, source.Path)
	if err != nil {
		return out, err
	}
	i := slices.IndexFunc(hooks, func(h projectWebhookDefinition) bool { return h.ID == id })
	if i < 0 {
		return out, fmt.Errorf("a selected automation is unavailable")
	}
	h := hooks[i]
	if h.Project.Kind != "crew" || h.Project.ID != source.CrewID || h.RunAs != source.CrewOwner || h.Action.Type != "message" || h.Action.WorkflowID != "" {
		return out, fmt.Errorf("choose a webhook owned by this Crew")
	}
	trigger, err := crewWebhookExecutionView(h)
	if err != nil {
		return out, err
	}
	if err = validateProductWebhook(trigger); err != nil {
		return out, fmt.Errorf("prepare this webhook in its source settings first")
	}
	credential, err := decryptSecretValueWithAAD(h.Auth.EncryptedSecret, productWebhookAAD(source.CrewID, h.ID))
	if err != nil {
		return out, fmt.Errorf("the source webhook's authentication is unavailable; configure it before adding it to the Team")
	}
	if credential != "" && (strings.Contains(h.Name, credential) || strings.Contains(trigger.Message, credential)) {
		return out, fmt.Errorf("remove credential values from the webhook message before adding it to the Team")
	}

	return teamOnlineWebhook{ID: h.ID, Name: h.Name, Enabled: h.Enabled, Message: trigger.Message, AuthMode: h.Auth.Mode}, nil
}

func (api *StreamingAPI) installTeamOnlineWebhooks(ctx context.Context, source, destination triggerTarget, p *teamOnlinePackage, hooks []teamOnlineWebhook, enable bool) ([]teamOnlineItemMapping, []string, error) {
	if len(hooks) == 0 {
		return nil, nil, nil
	}
	claims := GetUserFromContext(ctx)
	if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows("webhooks.json", true) {
		return nil, nil, fmt.Errorf("this connection does not allow Team webhooks")
	}
	mainKey := crewOwnerMainChatKey(destination.CrewID)
	if mainKey == "" {
		return nil, nil, fmt.Errorf("choose the Team's Main agent before moving webhooks")
	}
	current, err := readProjectWebhookDefinitions(ctx, destination.Path)
	if err != nil {
		return nil, nil, err
	}
	mappings := []teamOnlineItemMapping{}
	notes := []string{}
	if p.webhookEnabled == nil {
		p.webhookEnabled = map[string]bool{}
	}
	for _, h := range hooks {
		if _, err = uuid.Parse(h.ID); err != nil {
			return nil, nil, fmt.Errorf("choose a valid source webhook")
		}
		id := teamOnlineWebhookID(source.CrewID, h.ID)
		index := slices.IndexFunc(current, func(v projectWebhookDefinition) bool { return v.ID == id })
		wanted, staged := p.webhookEnabled[id]
		prior, found, err := lookupTeamOnlineItem(ctx, p.request.TeamProjectID, source.CrewID, "automation", h.ID)
		if err != nil {
			return nil, nil, err
		}
		if found && (prior.SourceOwner != claims.UserID || prior.DestinationItemID != id) {
			return nil, nil, fmt.Errorf("update only webhooks from your own source Crew")
		}
		if index >= 0 && !found && !staged {
			return nil, nil, fmt.Errorf("the destination webhook ID is already in use")
		}
		if !staged {
			wanted = h.Enabled
			if index >= 0 {
				wanted = current[index].Enabled
			}
			p.webhookEnabled[id] = wanted
		}
		trigger := productWebhookTrigger{ID: id, Name: h.Name, Message: h.Message, Enabled: enable && wanted, ChatKey: mainKey, RunDestination: runDestinationChat, Webhook: &WorkflowWebhookConfig{AuthMode: h.AuthMode}, CreatedBy: automationActor(ctx), UpdatedBy: automationActor(ctx)}
		if index >= 0 {
			trigger.CreatedBy = current[index].CreatedBy
			// Updating code never rotates the Team's configured authentication.
			trigger.Webhook.AuthMode = current[index].Auth.Mode
			trigger.Webhook.EncryptedSecret = current[index].Auth.EncryptedSecret
		}
		if trigger.Webhook.EncryptedSecret == "" {
			random := make([]byte, 32)
			if _, err = rand.Read(random); err != nil {
				return nil, nil, fmt.Errorf("the Team webhook credential could not be created")
			}
			trigger.Webhook.EncryptedSecret, err = encryptSecretValueWithAAD(hex.EncodeToString(random), productWebhookAAD(destination.CrewID, id))
			if err != nil {
				return nil, nil, fmt.Errorf("the Team webhook credential could not be saved")
			}
		}
		if err = validateProductWebhook(trigger); err != nil {
			return nil, nil, err
		}
		record, err := crewWebhookDefinition(projectactions.Project{Kind: "crew", ID: destination.CrewID}, destination.CrewOwner, trigger)
		if err != nil {
			return nil, nil, err
		}
		if index < 0 {
			current = append(current, record)
		} else {
			current[index] = record
		}
		mappings = append(mappings, teamOnlineItemMapping{SourceProjectID: source.CrewID, Kind: "automation", AutomationKind: "webhook", ItemID: h.ID, DestinationProjectID: destination.CrewID, DestinationItemID: id, Revision: teamOnlineWebhookRevision(h), Name: h.Name, WhenRuns: "Webhook"})
		if enable {
			address := strings.TrimRight(effectiveShareBaseURL(), "/") + record.Path
			notes = append(notes, address+" — Update the sender (e.g. GitHub) to this new address; the old one keeps working until you turn it off. Rotate and copy its new credential in the Team's normal Webhooks settings; never send it in chat.")
		}
	}
	if err = writeProjectWebhookDefinitions(ctx, destination.Path, current); err != nil {
		return nil, nil, err
	}

	return mappings, notes, nil
}
