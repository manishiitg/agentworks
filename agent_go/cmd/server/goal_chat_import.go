package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/spf13/cobra"
)

// Operator-only, stopped-app migration. Reuse the canonical dialogue projector;
// do not add a transcript fallback to ordinary event polling.
type goalChatImport struct {
	Session string `json:"session_id"`
	Owner   string `json:"owner_id"`
	Archive string `json:"archive"`
}

func importGoalChatArchives(journalPath string, imports []goalChatImport) (int, error) {
	journal, err := events.OpenSQLiteEventJournal(journalPath)
	if err != nil {
		return 0, err
	}
	defer journal.Close()
	store := events.NewEventStore(100)
	store.SetDurableJournal(journal)
	count := 0
	for _, item := range imports {
		if strings.TrimSpace(item.Owner) == "" || strings.TrimSpace(item.Session) == "" {
			return count, fmt.Errorf("chat owner and session are required")
		}
		owner, err := journal.Owner(item.Session)
		if err != nil {
			return count, err
		}
		if owner != "" && owner != item.Owner {
			return count, fmt.Errorf("chat ownership differs from its archive")
		}
		_, sequence, err := journal.KnownSession(item.Session)
		if err != nil {
			return count, err
		}
		if sequence > 0 {
			continue
		} // Existing journals remain canonical, byte for byte.
		raw, err := os.ReadFile(item.Archive)
		if err != nil {
			return count, err
		}
		var archive builderConversationLog
		if err = json.Unmarshal(raw, &archive); err != nil {
			return count, err
		}
		if archive.SessionID != item.Session || archive.UserID != item.Owner {
			return count, fmt.Errorf("chat archive identity differs from its registry")
		}
		var dialogue []events.Event
		for _, row := range builderConversationToRawEventsLimit(archive, 0) {
			var event events.Event
			if err = json.Unmarshal(row, &event); err != nil {
				return count, err
			}
			dialogue = append(dialogue, event)
		}
		// The installation is stopped during conversion. An unanswered saved
		// question is history, not a live turn; otherwise the Crew shows an
		// endless working indicator after import. Do not fabricate an answer.
		if len(dialogue) > 0 && dialogue[len(dialogue)-1].Type == "user_message" {
			dialogue = append(dialogue, events.Event{
				ID: "goal-import-stopped-" + item.Session, Type: "context_cancelled",
				SessionID: item.Session, Timestamp: dialogue[len(dialogue)-1].Timestamp,
			})
		}
		store.SetSessionOwner(item.Session, item.Owner)
		if err = store.SetSessionPersistenceClass(item.Session, events.SessionPersistenceInteractiveChat); err != nil {
			return count, err
		}
		if err = store.ImportDurableChatEvents(item.Session, dialogue); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func init() {
	var plan, journal string
	command := &cobra.Command{Use: "import-goal-chats", Short: "Import selected Goal chat archives while the app is stopped", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := os.ReadFile(plan)
			if err != nil {
				return err
			}
			var imports []goalChatImport
			if err = json.Unmarshal(raw, &imports); err != nil {
				return err
			}
			count, err := importGoalChatArchives(journal, imports)
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Imported %d chat archives; existing journals retained.\n", count)
			}
			return err
		}}
	command.Flags().StringVar(&plan, "plan", "", "Selected private archive manifest")
	command.Flags().StringVar(&journal, "journal", "", "This stopped instance's structured chat journal")
	_ = command.MarkFlagRequired("plan")
	_ = command.MarkFlagRequired("journal")
	ServerCmd.AddCommand(command)
}
