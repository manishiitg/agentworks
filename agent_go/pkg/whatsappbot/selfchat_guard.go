package whatsappbot

import (
	"context"
	"crypto/sha256"
	"log"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/whatsapptransport"
	"go.mau.fi/whatsmeow/types"
)

// Self-chat guard (#4269). The person talks to AgentWorks in WhatsApp's "Message yourself" chat, so every from-me
// message there counts as the person typing. When two copies of AgentWorks are linked to the same number (a server and
// a laptop), each copy's replies arrive in the other as from-me messages, and a newly linked device can replay old
// messages. Without this guard, one copy's 38 menu replies became 38 queued turns in the other.
const (
	selfChatFromAgentWorks = "sent_by_agentworks"
	selfChatOld            = "old"
	selfChatRepeat         = "repeat"
	selfChatBurst          = "burst"

	selfChatMaxAge       = 10 * time.Minute
	selfChatRepeatWindow = 20 * time.Second
	selfChatBurstWindow  = 30 * time.Second
	selfChatBurstLimit   = 6
	selfChatWarnEvery    = time.Hour
)

type selfChatGuard struct {
	mu     sync.Mutex
	recent map[string][]selfChatArrival // by chat
	warned map[string]time.Time
}

type selfChatArrival struct {
	at  time.Time
	key [32]byte
}

// admit returns "" when a from-me self-chat message is the person typing, or the reason to drop it.
func (g *selfChatGuard) admit(chat types.JID, id types.MessageID, text string, sent, now time.Time) string {
	if whatsapptransport.IsAgentWorksMessageID(id) {
		return selfChatFromAgentWorks
	}
	if !sent.IsZero() && now.Sub(sent) > selfChatMaxAge {
		return selfChatOld
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.recent == nil {
		g.recent = map[string][]selfChatArrival{}
	}
	chatKey := chat.ToNonAD().String()
	kept := g.recent[chatKey][:0]
	for _, a := range g.recent[chatKey] {
		if now.Sub(a.at) < selfChatBurstWindow {
			kept = append(kept, a)
		}
	}
	key := sha256.Sum256([]byte(text))
	reason := ""
	if len(kept) >= selfChatBurstLimit {
		reason = selfChatBurst
	} else if text != "" {
		for _, a := range kept {
			if a.key == key && now.Sub(a.at) < selfChatRepeatWindow {
				reason = selfChatRepeat
				break
			}
		}
	}
	g.recent[chatKey] = append(kept, selfChatArrival{at: now, key: key})
	return reason
}

func (g *selfChatGuard) shouldWarn(chat types.JID, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.warned == nil {
		g.warned = map[string]time.Time{}
	}
	k := chat.ToNonAD().String()
	if last, ok := g.warned[k]; ok && now.Sub(last) < selfChatWarnEvery {
		return false
	}
	g.warned[k] = now
	return true
}

// warnSelfChatOnce tells the person, at most once an hour, why messages in their own chat are being ignored. The notice
// carries the AgentWorks tag, so another linked copy ignores it too.
func (c *Connector) warnSelfChatOnce(acct *whatsapptransport.Account, chat types.JID, reason string) {
	if acct == nil || !c.selfGuard.shouldWarn(chat, time.Now()) {
		return
	}
	text := "This WhatsApp number is also connected to AgentWorks on another computer or server, so its replies were reaching me as if you had typed them. I'm ignoring them. Keep AgentWorks connected in one place: disconnect WhatsApp in the other AgentWorks, or remove it under WhatsApp › Linked devices on your phone."
	if reason == selfChatBurst {
		text = "Many messages arrived in this chat within a few seconds, so I'm ignoring the extra ones. If another computer or server also runs AgentWorks on this WhatsApp number, keep it connected in one place only."
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := c.sendTextID(ctx, acct, chat.ToNonAD(), text); err != nil {
			log.Printf("[WHATSAPP] self-chat warning not sent: %v", err)
		}
	}()
}
