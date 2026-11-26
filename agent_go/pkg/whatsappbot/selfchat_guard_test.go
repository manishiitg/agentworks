package whatsappbot

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/whatsapptransport"
	"go.mau.fi/whatsmeow/types"
)

// #4269: a second AgentWorks linked to the same number sent its route menu 38 times into the self-chat within 30
// seconds; each one was queued as the person's message. Only the person's own, fresh, non-repeated messages may pass.
func TestSelfChatGuardDropsAnotherAgentWorksAndFloods(t *testing.T) {
	var g selfChatGuard
	chat := types.NewJID("15551234567", types.HiddenUserServer)
	now := time.Now()
	menu := "Choose a workflow or project for this chat:\n1. @one\n2. @two"

	tagged := whatsapptransport.TagMessageID("3EB0123456789ABCDEF012")
	if !whatsapptransport.IsAgentWorksMessageID(tagged) || len(tagged) != 22 {
		t.Fatalf("tagged id %q not recognised", tagged)
	}
	if got := g.admit(chat, tagged, menu, now, now); got != selfChatFromAgentWorks {
		t.Fatalf("message from another AgentWorks admitted: %q", got)
	}

	admitted := 0
	for i := 0; i < 38; i++ {
		at := now.Add(time.Duration(i) * 800 * time.Millisecond)
		if g.admit(chat, "3EB0FFFFFFFFFFFFFFFF00", menu, at, at) == "" {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("flood of identical menus: %d admitted, want 1", admitted)
	}

	later := now.Add(5 * time.Minute)
	if got := g.admit(chat, "3EB0FFFFFFFFFFFFFFFF01", "hi", later, later); got != "" {
		t.Fatalf("person's own message dropped: %q", got)
	}
	if got := g.admit(chat, "3EB0FFFFFFFFFFFFFFFF02", "an old one", later.Add(-time.Hour), later); got != selfChatOld {
		t.Fatalf("replayed old message admitted: %q", got)
	}
}
