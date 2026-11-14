package server

// PLAT-746-971: one pending record, private to the maker and named recipient.
// Native bytes use the existing chunk transport; these records never grant
// authority to a filesystem path, provider login or another chat.
import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

const crewAgentTransferLifetime = 7 * 24 * time.Hour

type crewAgentTransfer struct {
	ID        string                  `json:"id"`
	Project   string                  `json:"project_id"`
	Sender    string                  `json:"sender_id"`
	Recipient string                  `json:"recipient_id"`
	Agent     crewTeamRegisteredAgent `json:"agent"`
	Created   time.Time               `json:"created_at"`
	Expires   time.Time               `json:"expires_at"`
	// Ready is set only after the staged conversation and all native chunks
	// have been validated. No screen may offer an incomplete upload for import.
	Ready bool `json:"ready"`
}

func crewAgentTransferDirectory(project, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("choose a valid transfer")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agent-transfers", id), nil
}

// The maker may cancel or inspect their own offer; only its named recipient
// may read its conversation/chunks or complete an import. Every operation
// checks the current registry, not the membership captured at upload time.
func (offer crewAgentTransfer) admit(team crewTeam, person string, download bool, now time.Time) error {
	if !team.On || team.ProjectID != offer.Project || !team.member(offer.Sender) || !team.member(offer.Recipient) ||
		offer.Sender == offer.Recipient || person == "" || !now.Before(offer.Expires) {
		return fmt.Errorf("this agent transfer is no longer available")
	}
	agent, ok := team.Agents[offer.Agent.ID]
	if !ok || agent.OwnerID != offer.Sender || agent.ChatKey != offer.Agent.ChatKey || agent.Tab != offer.Agent.Tab {
		return fmt.Errorf("this agent's owner or chat changed; start a new transfer")
	}
	if download {
		if person != offer.Recipient || !offer.Ready {
			return fmt.Errorf("only the named recipient can download this transfer when it is ready")
		}
	} else if person != offer.Sender && person != offer.Recipient {
		return fmt.Errorf("this agent transfer is not yours")
	}
	return nil
}

func newCrewAgentTransfer(team crewTeam, sender, recipient, agentID string, now time.Time) (crewAgentTransfer, error) {
	agent, ok := team.Agents[agentID]
	if !ok || agent.OwnerID != sender || !team.On || !team.member(sender) || !team.member(recipient) || sender == recipient {
		return crewAgentTransfer{}, fmt.Errorf("choose your own agent and another person in this Team")
	}
	return crewAgentTransfer{ID: uuid.NewString(), Project: team.ProjectID, Sender: sender, Recipient: recipient,
		Agent: agent, Created: now, Expires: now.Add(crewAgentTransferLifetime)}, nil
}

func saveCrewAgentTransfer(offer crewAgentTransfer) error {
	dir, err := crewAgentTransferDirectory(offer.Project, offer.ID)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "transfer.json"), offer, 0600)
}

// Called after successful import, decline/cancel, or expiry. Transfer-scoped
// chunks and history live under this same directory, so no private chat copy
// is retained after its pending record goes away. Never removes project files.
func removeCrewAgentTransfer(offer crewAgentTransfer) error {
	dir, err := crewAgentTransferDirectory(offer.Project, offer.ID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
