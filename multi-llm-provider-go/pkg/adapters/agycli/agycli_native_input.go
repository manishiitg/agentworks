package agycli

import (
	"fmt"
	"time"
)

func AdoptNativeInput(owner, message string, acceptedAt time.Time) error {
	session, ok := activeAgyInteractiveSession(owner)
	if !ok {
		return fmt.Errorf("no Agy session for %q", owner)
	}
	id := session.getConversationID()
	indices, err := agyMatchingUserStepIndices(id, -1, message)
	if err != nil {
		return err
	}
	if len(indices) == 0 {
		return fmt.Errorf("accepted Agy prompt not found")
	}
	// Scope the retained answer to the newest matching user step. Repeated
	// prompts remain distinct, with the native idx as their boundary.
	index := indices[len(indices)-1]
	session.durableMu.Lock()
	session.pendingDurable = append(session.pendingDurable, agyPendingDurableAck{
		message: message, conversationID: id, baselineIdx: index - 1,
		occurrence: 1, sentAt: acceptedAt, claimed: true,
	})
	if len(session.pendingDurable) > 16 {
		session.pendingDurable = session.pendingDurable[len(session.pendingDurable)-16:]
	}
	session.durableMu.Unlock()
	return nil
}
