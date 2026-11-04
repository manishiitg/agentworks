package server

import (
	"context"
	"errors"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Only the WhatsApp pairing owner's own "Message yourself" chat reaches their Mac Crew (owner, 4 Nov): anyone else
// who messages that number gets the plain refusal, before the Mac is asked anything.
func TestWhatsAppMacCrewTakesOnlyTheOwnersSelfChat(t *testing.T) {
	api := &StreamingAPI{}
	for _, msg := range []services.BotIncomingMessage{
		{Platform: "whatsapp", Text: "hi", SelfChat: false},
		{Platform: "whatsapp", Text: "hi", SelfChat: true}, // no pairing owner
	} {
		owner := "owner-1"
		if msg.SelfChat {
			owner = ""
		}
		var reply services.BotReplyText
		if err := api.whatsappCrewOnMac(context.Background(), owner, "crew-1", msg); !errors.As(err, &reply) || reply.Text != crewOnMacSlackRefusal {
			t.Fatalf("self_chat=%v owner=%q: got %v, want the refusal", msg.SelfChat, owner, err)
		}
	}
}
